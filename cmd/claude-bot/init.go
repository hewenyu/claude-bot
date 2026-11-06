package main

import (
	"bytes"
	"context"
	_ "embed"
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
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
)

//go:embed bot.md
var botCommand []byte

const (
	verifyTimeout = 3 * time.Minute
	apiTimeout    = 15 * time.Second
	// verifyStop bounds the wait for the check's connection to close.
	verifyStop = 5 * time.Second
)

// quiet is the logger of channels built only to check credentials.
var quiet = slog.New(slog.DiscardHandler)

type initOptions struct {
	channel                             string // lark, discord, telegram, slack, dingtalk or github
	appID, secret, domain, proxy, name  string
	discordToken, telegramToken         string
	slackAppToken, slackBotToken        string
	githubToken, githubRepo             string
	githubTokenName                     string // the gateway's token to use; "" is the default one
	githubRemote                        string // <owner>/<name> of the repository's GitHub remote, "" if none
	dingtalkID, dingtalkSecret          string
	typesafeKey                         string
	reregister, noOpen, noVerify, force bool
	botMD                               bool   // only rewrite the /bot command
	sharedGitHubToken                   string // the gateway's token of githubTokenName, used unless --github-token gives another

	root string  // the repository
	out  printer // tells the person running init
}

// channelInit is how init sets up one kind of channel (see config.Kinds).
type channelInit struct {
	hint  string                              // after its label in the menu
	given func(*initOptions) bool             // its credential flags are given
	ask   func(*initOptions, *prompter) error // asks for the tokens the flags did not give; nil asks none
	setup func(context.Context, *config.Config, initOptions) (setup, error)
}

// channelInits are the kinds of channel init sets up.
var channelInits = map[string]channelInit{
	lark.Name: {
		hint:  "在浏览器里确认即可创建应用",
		given: func(o *initOptions) bool { return o.appID != "" },
		setup: setupLark,
	},
	discord.Name: {
		hint:  "需要 bot token",
		given: func(o *initOptions) bool { return o.discordToken != "" },
		ask: func(o *initOptions, p *prompter) error {
			return askToken(p, o.channel, "--discord-token", "", "Discord bot token（Developer Portal → Bot → Reset Token）", &o.discordToken)
		},
		setup: setupDiscord,
	},
	telegram.Name: {
		hint:  "需要 @BotFather 给的 token",
		given: func(o *initOptions) bool { return o.telegramToken != "" },
		ask: func(o *initOptions, p *prompter) error {
			return askToken(p, o.channel, "--telegram-token", "", "Telegram bot token（@BotFather → /newbot）", &o.telegramToken)
		},
		setup: setupTelegram,
	},
	slack.Name: {
		hint:  "需要 app token 和 bot token",
		given: func(o *initOptions) bool { return o.slackBotToken != "" },
		ask:   askSlackTokens,
		setup: setupSlack,
	},
	github.Name: {
		hint:  "仓库的 issue、PR 和评论，需要 token",
		given: func(o *initOptions) bool { return o.githubToken != "" || o.githubTokenName != "" },
		ask:   askGitHubToken,
		setup: setupGitHub,
	},
	dingtalk.Name: {
		hint:  "企业内部应用的机器人，需要 Client ID 和 Client Secret",
		given: func(o *initOptions) bool { return o.dingtalkID != "" },
		ask:   askDingTalkCredentials,
		setup: setupDingTalk,
	},
}

func cmdInit(ctx context.Context, project string, args []string) error {
	o, err := parseInitFlags(args)
	if err != nil {
		return err
	}

	o.out = printer{os.Stdout}

	dir, err := config.Dir(ctx, project)
	if err != nil {
		return err
	}

	o.root = filepath.Dir(dir)
	if o.botMD {
		return rewriteBotCommand(o.root, o.out)
	}

	if o.name == "" {
		o.name = filepath.Base(o.root) + " Bot"
	}

	cfg, err := config.Read(ctx, dir)
	if err != nil {
		return err
	}

	// Projects share the gateway's GitHub tokens: the default one, or the
	// one named for an account of their own.
	if o.githubTokenName == "" {
		o.githubTokenName = cfg.GitHubTokenName
	}

	if o.sharedGitHubToken, err = config.GatewayGitHubToken(ctx, o.githubTokenName); err != nil {
		return err
	}

	o.githubRemote = githubRemote(ctx, o.root)

	// One prompter for the whole of init: it buffers what it reads. Off a
	// terminal (scripts, Claude Code) there is none, and init asks nothing.
	p := terminalPrompter(o.out)

	// Jev comes before anything else: every message goes through it.
	if err := setupJev(ctx, &cfg, o, p); err != nil {
		return err
	}

	if keyOnly(o, cfg) {
		return saveKey(ctx, dir, cfg, o.out)
	}

	if err := pickChannel(&o, cfg, p); err != nil {
		return err
	}

	s, err := channelInits[o.channel].setup(ctx, &cfg, o)
	if err != nil {
		return err
	}

	return finishInit(ctx, dir, cfg, s, o)
}

// saveProject saves cfg as the configuration of the project in the bot
// directory dir, after making sure git ignores the directory: secrets go
// into it.
func saveProject(ctx context.Context, dir string, cfg config.Config) (store.Project, error) {
	if err := ensureLine(filepath.Join(filepath.Dir(dir), ".gitignore"), config.DirName+"/"); err != nil {
		return store.Project{}, err
	}

	return config.Save(ctx, dir, cfg)
}

// finishInit saves what init set up, writes the /bot command, checks the
// connection and says what to do next.
func finishInit(ctx context.Context, dir string, cfg config.Config, s setup, o initOptions) error {
	// The gateway picks the saved configuration up within seconds; checking
	// the connection here as well would put a second listener on the app.
	gw, err := gatewayPID()
	if err != nil {
		return err
	}

	p, err := saveProject(ctx, dir, cfg)
	if err != nil {
		return err
	}

	if err := writeIfAbsent(botCommandPath(o.root), botCommand, o.force); err != nil {
		return err
	}

	out := o.out
	out.printf("✓ bot「%s」已接入 %s（gateway 里的项目名：%s）\n", s.name, o.root, p.Name)
	s.report(ctx)

	switch {
	case o.noVerify:
	case gw != 0:
		out.printf("✓ gateway 正在运行（pid %d），几秒内会接上这个项目，跳过连接验证\n", gw)
	default:
		if err := verifyConnection(ctx, dir, s.ch, s.prompt, out); err != nil {
			return err
		}
	}

	out.println("\n下一步：")

	if gw == 0 {
		out.println("  1. 在终端里运行  " + lineGatewayStart + "  启动 gateway（一个进程服务所有项目）")
	} else {
		out.println("  1. gateway 已在运行，会自动用上新配置；" + hintGatewayStatus + " 可以看它接上没有")
	}

	out.println("  2. 在 gateway 的网页（claude-bot gateway open）上打开这个项目的「自动处理」；或者自己在这个仓库里的 Claude Code 运行  /bot")
	warnOwnListener(dir, out)
	out.println("把 bot 拉进项目群（Discord 是邀请进服务器，Slack 是在频道里 /invite），大家私聊它或在群里 @ 它即可。")
	out.printf("要再接入其他渠道，运行  claude-bot init --channel <%s>\n", config.KindNames("|"))

	return nil
}

// pickChannel settles which channel to set up and asks for its tokens. With
// no choice made by flags it shows a menu on a terminal (p is not nil);
// elsewhere (scripts, Claude Code) it keeps the old default, Lark.
func pickChannel(o *initOptions, cfg config.Config, p *prompter) error {
	if o.channel == "" {
		o.channel = config.Kinds()[0].Name

		if p != nil {
			name, err := p.chooseChannel(cfg, o.githubRemote != "")
			if err != nil {
				return err
			}

			o.channel = name
		}
	}

	return askTokens(o, p)
}

// jevGuide is where to get a TypeSafe API key.
const jevGuide = "https://console.typesafe.ai/keys"

// setupJev checks o's TypeSafe key and records it in cfg. Without a key it
// keeps the one set up before, or asks p for one, if there is a terminal.
func setupJev(ctx context.Context, cfg *config.Config, o initOptions, p *prompter) error {
	key := o.typesafeKey
	if key == "" && cfg.TypeSafe != nil {
		return nil
	}

	if key == "" {
		if p == nil {
			return fmt.Errorf("%w: claude-bot needs a TypeSafe API key for Jev: --typesafe-key <key> (from %s)", errUsage, jevGuide)
		}

		p.say("每条消息都先交给 TypeSafe 的 Jev 裁剪归组、拦下非法请求，再给 Claude。API key 在这里创建：%s\n", jevGuide)

		var err error
		if key, err = p.ask("TypeSafe API key"); err != nil {
			return err
		}
	}

	if _, err := withTimeout(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, jev.New(key).Check(ctx)
	}); err != nil {
		return fmt.Errorf("TypeSafe API key not accepted: %w", err)
	}

	cfg.TypeSafe = &jev.Config{APIKey: key}

	o.out.println("✓ Jev 已接入（TypeSafe API key 有效）")

	return nil
}

// keyOnly reports whether init was given only a TypeSafe key, for a project
// with channels already: then saving the key is all there is to do.
func keyOnly(o initOptions, cfg config.Config) bool {
	return o.channel == "" && o.typesafeKey != "" && cfg.HasAny()
}

// saveKey saves the project's configuration with its new TypeSafe key.
func saveKey(ctx context.Context, dir string, cfg config.Config, out printer) error {
	if _, err := saveProject(ctx, dir, cfg); err != nil {
		return err
	}

	out.println("✓ 已保存，所有项目共用这个 key；gateway 在运行的话几秒内自动用上，没在运行就运行  " + lineGatewayStart)
	warnOwnListener(dir, out)

	return nil
}

// warnOwnListener tells the user to stop a listener the project ran before
// the gateway, which keeps the gateway out.
func warnOwnListener(dir string, out printer) {
	if pid := ownListener(dir); pid != 0 {
		out.printf("! 这个项目自己的监听进程（pid %d）还在运行，gateway 要等它停掉才接手：在这里运行  %s\n", pid, lineStop)
	}
}

// rewriteBotCommand replaces the /bot command with this version's, leaving
// the bot configuration alone.
func rewriteBotCommand(root string, out printer) error {
	if err := writeIfAbsent(botCommandPath(root), botCommand, true); err != nil {
		return err
	}

	out.printf("✓ 已用 %s 版本的值守流程覆盖 %s\n", Version, botCommandPath(root))

	return nil
}

// setup is a channel whose credentials proved to work.
type setup struct {
	ch     channel.Channel
	name   string                // the bot's display name
	prompt string                // asks the user to send the bot a direct message; "" skips the check
	report func(context.Context) // prints what is left to do on the platform
}

func parseInitFlags(args []string) (initOptions, error) {
	var o initOptions

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&o.channel, "channel", "", config.KindNames(", ")+" (default: ask on a terminal, else "+config.Kinds()[0].Name+")")
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
	fs.StringVar(&o.typesafeKey, "typesafe-key", "", "TypeSafe API key for Jev, which screens every message (asked on a terminal if none is set up)")
	fs.StringVar(&o.githubRepo, "github-repo", "", "with --github-token: <owner>/<name> (default: from the repository's GitHub remote)")
	fs.StringVar(&o.githubTokenName, "github-token-name", "", "use the gateway's GitHub token of this name, for a project of another account than the default token's; with --github-token, save that token under this name")
	fs.StringVar(&o.proxy, "proxy", "", "direct, env, or a proxy URL (default: direct for feishu and dingtalk, env for the others)")
	fs.StringVar(&o.name, "name", "", "bot name shown in Lark (default: <repo> Bot)")
	fs.BoolVar(&o.reregister, "reregister", false, "create a new app even if one is configured")
	fs.BoolVar(&o.noOpen, "no-open", false, "print the authorization link without opening a browser")
	fs.BoolVar(&o.noVerify, "no-verify", false, "skip the send-a-message connection check")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing .claude/commands/bot.md")

	if _, err := parseCommand(fs, args, exactly(0), "init [flags]"); err != nil {
		return o, err
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

	if o.githubTokenName != "" {
		if err := config.CheckTokenName(o.githubTokenName); err != nil {
			return o, fmt.Errorf("%w: %w", errUsage, err)
		}
	}

	return o, settleChannel(&o)
}

// settleChannel derives o.channel from the credential flags and rejects
// flags that ask for different channels.
func settleChannel(o *initOptions) error {
	implied := impliedChannels(o)

	switch {
	case o.botMD && (o.channel != "" || len(implied) > 0):
		return fmt.Errorf("%w: --bot-md only rewrites the /bot command; run it without channel flags", errUsage)
	case len(implied) > 1:
		return fmt.Errorf("%w: --app-id, --discord-token, --telegram-token, --slack-*-token, --dingtalk-client-* and --github-token set up different channels; run init once for each", errUsage)
	case len(implied) == 1 && o.channel != "" && o.channel != implied[0]:
		return fmt.Errorf("%w: --channel %s does not match the %s credentials given", errUsage, o.channel, implied[0])
	case len(implied) == 1:
		o.channel = implied[0]
	case o.channel != "":
		if _, ok := config.KindOf(o.channel); !ok {
			return fmt.Errorf("%w: unknown --channel %q: want one of %s", errUsage, o.channel, config.KindNames(", "))
		}
	}

	return nil
}

// impliedChannels are the channels the credential flags set up.
func impliedChannels(o *initOptions) []string {
	var implied []string

	for _, k := range config.Kinds() {
		if channelInits[k.Name].given(o) {
			implied = append(implied, k.Name)
		}
	}

	return implied
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
func verifyConnection(ctx context.Context, dir string, ch channel.Channel, prompt string, out printer) error {
	if prompt == "" {
		return nil
	}

	a := assemble(dir, newLogger(), ch)

	if _, alive := readHeartbeat(a.dir); alive {
		out.println("✓ 监听进程已在运行，跳过连接验证")

		return nil
	}

	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	got := make(chan channel.Message, 1)
	hold := a.deliverer(vctx, ch, nil)
	deliver := func(m channel.Message) error {
		if err := hold(m); err != nil {
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

	// The connection is closed before verifyConnection returns: the
	// gateway may connect to the app next.
	listenErr, stop := listenBriefly(vctx, ch, deliver)
	defer stop()

	out.printf("\n连接验证：%s（%s 内）…\n", prompt, verifyTimeout)

	select {
	case m := <-got:
		if err := ch.Reply(vctx, m, "✅ 连接成功！在 gateway 的网页上打开这个项目的「自动处理」后，我就会处理发给我的消息。"); err != nil {
			return fmt.Errorf("received your message but replying failed: %w", err)
		}

		// Answered here; it must not become a task.
		if err := a.inbox.Archive(m); err != nil {
			return err
		}

		out.println("✓ 收发都已打通")

		return nil
	case err := <-listenErr:
		return fmt.Errorf("connection check: %w", err)
	case <-vctx.Done():
		if err := ctx.Err(); err != nil {
			return err // interrupted, not timed out
		}

		out.println("! 没有收到消息，跳过验证。可以稍后运行 `claude-bot status` 检查，或重新运行 init。")

		return nil
	}
}

// listenBriefly runs ch's Listen with deliver until ctx ends or stop is
// called, which waits, up to verifyStop, until Listen has returned. Listen's
// error, if it returns before, arrives on errs.
func listenBriefly(ctx context.Context, ch channel.Channel, deliver func(channel.Message) error) (errs <-chan error, stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	listenErr := make(chan error, 1)
	listened := make(chan struct{})

	go func() {
		defer close(listened)

		listenErr <- ch.Listen(ctx, deliver, func() {})
	}()

	return listenErr, func() {
		cancel()

		select {
		case <-listened:
		case <-time.After(verifyStop):
		}
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
