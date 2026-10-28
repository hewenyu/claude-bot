package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

const (
	verifyTimeout = 3 * time.Minute
	apiTimeout    = 15 * time.Second
)

// quiet is the logger of channels built only to check credentials.
var quiet = slog.New(slog.DiscardHandler)

type initOptions struct {
	channel                             string // lark, discord, telegram, slack, dingtalk or github
	appID, secret, domain, proxy, name  string
	discordToken, telegramToken         string
	slackAppToken, slackBotToken        string
	githubToken, githubRepo             string
	dingtalkID, dingtalkSecret          string
	reregister, noOpen, noVerify, force bool
	botMD                               bool // only rewrite the /bot command
	pi                                  bool // only install the pi /loop extension
}

func cmdInit(ctx context.Context, project string, args []string) error {
	o, err := parseInitFlags(args)
	if err != nil {
		return err
	}

	dir, err := config.Dir(ctx, project)
	if err != nil {
		return err
	}

	root := filepath.Dir(dir)
	if o.agentFilesOnly() {
		return writeAgentFiles(root, o)
	}

	if o.name == "" {
		o.name = filepath.Base(root) + " Bot"
	}

	cfg, err := config.Read(dir)
	if err != nil {
		return err
	}

	if err := pickChannel(&o, cfg); err != nil {
		return err
	}

	s, err := setupChannel(ctx, &cfg, o, root)
	if err != nil {
		return err
	}

	// Ignore the directory before the secret is written into it.
	if err := ensureLine(filepath.Join(root, ".gitignore"), config.DirName+"/"); err != nil {
		return err
	}

	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	if err := writeWorkflow(root, o.force); err != nil {
		return err
	}

	fmt.Printf("✓ bot「%s」已接入 %s\n", s.name, root)
	s.report(ctx)

	if !o.noVerify {
		if err := verifyConnection(ctx, dir, s.ch, s.prompt); err != nil {
			return err
		}
	}

	fmt.Println("\n下一步：")
	fmt.Println("  1. 在终端里运行  claude-bot start  启动监听进程（已经在运行的话用  claude-bot restart  接上新渠道）")
	fmt.Println("  2. 在这个仓库里打开 Claude Code，运行  /loop /bot")
	fmt.Println("把 bot 拉进项目群（Discord 是邀请进服务器，Slack 是在频道里 /invite），大家私聊它或在群里 @ 它即可。")
	fmt.Printf("要再接入其他渠道，运行  claude-bot init --channel <%s>\n", channelNames("|"))

	return nil
}

// pickChannel settles which channel to set up and asks for its tokens. With
// no choice made by flags it shows a menu on a terminal; elsewhere (scripts,
// Claude Code) it keeps the old default, Lark.
func pickChannel(o *initOptions, cfg config.Config) error {
	p, interactive := terminalPrompter()
	if !interactive {
		p = nil
	}

	if o.channel == "" {
		o.channel = lark.Name

		if p != nil {
			name, err := p.chooseChannel(cfg)
			if err != nil {
				return err
			}

			o.channel = name
		}
	}

	return askTokens(o, p)
}

// setup is a channel whose credentials proved to work.
type setup struct {
	ch     channel.Channel
	name   string                // the bot's display name
	prompt string                // asks the user to send the bot a direct message; "" skips the check
	report func(context.Context) // prints what is left to do on the platform
}

// setupChannel obtains working credentials for o.channel and records them
// in cfg.
func setupChannel(ctx context.Context, cfg *config.Config, o initOptions, root string) (setup, error) {
	switch o.channel {
	case discord.Name:
		return setupDiscord(ctx, cfg, o)
	case telegram.Name:
		return setupTelegram(ctx, cfg, o)
	case slack.Name:
		return setupSlack(ctx, cfg, o)
	case github.Name:
		return setupGitHub(ctx, cfg, o, root)
	case dingtalk.Name:
		return setupDingTalk(ctx, cfg, o)
	}

	return setupLark(ctx, cfg, o, root)
}

func parseInitFlags(args []string) (initOptions, error) {
	var o initOptions

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&o.channel, "channel", "", channelNames(", ")+" (default: ask on a terminal, else "+channelChoices[0].name+")")
	fs.BoolVar(&o.botMD, "bot-md", false, "only rewrite .claude/commands/bot.md with this version's (overwrites it), nothing else")
	fs.StringVar(&o.appID, "app-id", "", "use an existing app instead of creating one (cli_...)")
	fs.StringVar(&o.secret, "app-secret", "", "secret of --app-id")
	fs.StringVar(&o.domain, "domain", lark.DomainFeishu, "with --app-id: feishu (China) or lark (international)")
	fs.StringVar(&o.discordToken, "discord-token", "", "set up a Discord bot with this bot token instead of a Lark app")
	fs.StringVar(&o.telegramToken, "telegram-token", "", "set up a Telegram bot with this token from @BotFather instead of a Lark app")
	fs.StringVar(&o.slackAppToken, "slack-app-token", "", "set up a Slack app: its app-level token (xapp-…), with --slack-bot-token")
	fs.StringVar(&o.slackBotToken, "slack-bot-token", "", "set up a Slack app: its bot token (xoxb-…), with --slack-app-token")
	fs.StringVar(&o.githubToken, "github-token", "", "set up the GitHub channel with this token (issues, PRs, comments and reviews of the repository)")
	fs.StringVar(&o.dingtalkID, "dingtalk-client-id", "", "set up a DingTalk app: its Client ID (AppKey), with --dingtalk-client-secret")
	fs.StringVar(&o.dingtalkSecret, "dingtalk-client-secret", "", "set up a DingTalk app: its Client Secret (AppSecret), with --dingtalk-client-id")
	fs.StringVar(&o.githubRepo, "github-repo", "", "with --github-token: <owner>/<name> (default: from the origin remote)")
	fs.StringVar(&o.proxy, "proxy", "", "direct, env, or a proxy URL (default: direct for feishu and dingtalk, env for the others)")
	fs.StringVar(&o.name, "name", "", "bot name shown in Lark (default: <repo> Bot)")
	fs.BoolVar(&o.reregister, "reregister", false, "create a new app even if one is configured")
	fs.BoolVar(&o.noOpen, "no-open", false, "print the authorization link without opening a browser")
	fs.BoolVar(&o.noVerify, "no-verify", false, "skip the send-a-message connection check")
	fs.BoolVar(&o.pi, "pi", false, "only install the /loop extension for pi into .pi/extensions (and the /bot workflow if missing), nothing else")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing .claude/commands/bot.md")

	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w: %w", errUsage, err)
	}

	if (o.appID == "") != (o.secret == "") {
		return o, fmt.Errorf("%w: --app-id and --app-secret go together", errUsage)
	}

	if (o.slackAppToken == "") != (o.slackBotToken == "") {
		return o, fmt.Errorf("%w: --slack-app-token and --slack-bot-token go together", errUsage)
	}

	if (o.dingtalkID == "") != (o.dingtalkSecret == "") {
		return o, fmt.Errorf("%w: --dingtalk-client-id and --dingtalk-client-secret go together", errUsage)
	}

	return o, settleChannel(&o)
}

// settleChannel derives o.channel from the credential flags and rejects
// flags that ask for different channels.
func settleChannel(o *initOptions) error {
	var implied []string

	for name, set := range map[string]bool{
		lark.Name:     o.appID != "",
		discord.Name:  o.discordToken != "",
		telegram.Name: o.telegramToken != "",
		slack.Name:    o.slackBotToken != "",
		github.Name:   o.githubToken != "",
		dingtalk.Name: o.dingtalkID != "",
	} {
		if set {
			implied = append(implied, name)
		}
	}

	switch {
	case o.agentFilesOnly() && (o.channel != "" || len(implied) > 0):
		return fmt.Errorf("%w: --bot-md and --pi only write the agent files; run them without channel flags", errUsage)
	case len(implied) > 1:
		return fmt.Errorf("%w: --app-id, --discord-token, --telegram-token, --slack-*-token, --dingtalk-client-* and --github-token set up different channels; run init once for each", errUsage)
	case len(implied) == 1 && o.channel != "" && o.channel != implied[0]:
		return fmt.Errorf("%w: --channel %s does not match the %s credentials given", errUsage, o.channel, implied[0])
	case len(implied) == 1:
		o.channel = implied[0]
	case o.channel != "" && !knownChannel(o.channel):
		return fmt.Errorf("%w: unknown --channel %q: want one of %s", errUsage, o.channel, channelNames(", "))
	}

	return nil
}

// withTimeout runs an API check under apiTimeout.
func withTimeout[T any](ctx context.Context, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	return f(ctx)
}

// verifyConnection proves the round trip: the user sends the bot a direct
// message, the bot answers it. Anything else that arrives meanwhile is kept
// in the inbox. Without a prompt (no direct messages, as on GitHub) there is
// nothing to check.
func verifyConnection(ctx context.Context, dir string, ch channel.Channel, prompt string) error {
	if prompt == "" {
		return nil
	}

	a := assemble(dir, newLogger(), ch)

	if _, alive := readHeartbeat(a.dir); alive {
		fmt.Println("✓ 监听进程已在运行，跳过连接验证")

		return nil
	}

	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	got := make(chan channel.Message, 1)
	store := a.deliverer(vctx, ch)
	deliver := func(m channel.Message) error {
		if err := store(m); err != nil {
			return err
		}

		if m.ChatType == channel.ChatDirect {
			select {
			case got <- m:
			default:
			}
		}

		return nil
	}

	listenErr := make(chan error, 1)
	go func() { listenErr <- ch.Listen(vctx, deliver, func() {}) }()

	fmt.Printf("\n连接验证：%s（%s 内）…\n", prompt, verifyTimeout)

	select {
	case m := <-got:
		if err := ch.Reply(vctx, m, "✅ 连接成功！在仓库里运行 `/loop /bot` 后，我就会处理发给我的消息。"); err != nil {
			return fmt.Errorf("received your message but replying failed: %w", err)
		}

		if err := a.inbox.Done(m.ID); err != nil {
			return err
		}

		fmt.Println("✓ 收发都已打通")

		return nil
	case err := <-listenErr:
		return fmt.Errorf("connection check: %w", err)
	case <-vctx.Done():
		if err := ctx.Err(); err != nil {
			return err // interrupted, not timed out
		}

		fmt.Println("! 没有收到消息，跳过验证。可以稍后运行 `claude-bot status` 检查，或重新运行 init。")

		return nil
	}
}

func ensureLine(path, line string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // project .gitignore
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	for l := range bytes.SplitSeq(raw, []byte("\n")) {
		if string(bytes.TrimSpace(l)) == line {
			return nil
		}
	}

	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		raw = append(raw, '\n')
	}

	raw = append(raw, line+"\n"...)

	if err := os.WriteFile(path, raw, sharedPerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func writeIfAbsent(path string, content []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(os.Stderr, "claude-bot: kept existing %s (use --force to overwrite)\n", path)

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, content, sharedPerm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
