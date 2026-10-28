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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/config"
)

//go:embed bot.md
var botCommand []byte

const (
	verifyTimeout = 3 * time.Minute
	apiTimeout    = 15 * time.Second
	// A freshly registered app can take a few seconds before its token works.
	registerChecks = 5
	registerDelay  = 2 * time.Second // grows linearly between checks
)

// larkApp is a set of credentials proven to work, with the bot they belong to.
type larkApp struct {
	cfg lark.Config
	ch  *lark.Channel
	bot lark.Bot
}

type initOptions struct {
	appID, secret, domain, proxy, name  string
	reregister, noOpen, noVerify, force bool
}

func cmdInit(ctx context.Context, args []string) error {
	o, err := parseInitFlags(args)
	if err != nil {
		return err
	}

	dir, err := config.Dir(ctx)
	if err != nil {
		return err
	}

	root := filepath.Dir(dir)
	if o.name == "" {
		o.name = filepath.Base(root) + " Bot"
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	la, err := obtainApp(ctx, cfg.Lark, o, root)
	if err != nil {
		return err
	}

	// Ignore the directory before the secret is written into it.
	if err := ensureLine(filepath.Join(root, ".gitignore"), config.DirName+"/"); err != nil {
		return err
	}

	cfg.Lark = &la.cfg
	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	if err := writeIfAbsent(filepath.Join(root, ".claude", "commands", "bot.md"), botCommand, o.force); err != nil {
		return err
	}

	fmt.Printf("✓ bot「%s」已接入 %s\n", la.bot.AppName, root)
	reportScopes(ctx, la.ch)

	if !o.noVerify {
		if err := verifyConnection(ctx, dir, la); err != nil {
			return err
		}
	}

	fmt.Println("\n下一步：在这个仓库里打开 Claude Code，运行  /loop /bot")
	fmt.Println("把 bot 拉进项目群，大家私聊它或在群里 @ 它即可。")

	return nil
}

func parseInitFlags(args []string) (initOptions, error) {
	var o initOptions

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&o.appID, "app-id", "", "use an existing app instead of creating one (cli_...)")
	fs.StringVar(&o.secret, "app-secret", "", "secret of --app-id")
	fs.StringVar(&o.domain, "domain", "feishu", "with --app-id: feishu (China) or lark (international)")
	fs.StringVar(&o.proxy, "proxy", "", "direct, env, or a proxy URL (default: direct for feishu, env for lark)")
	fs.StringVar(&o.name, "name", "", "bot name shown in Lark (default: <repo> Bot)")
	fs.BoolVar(&o.reregister, "reregister", false, "create a new app even if one is configured")
	fs.BoolVar(&o.noOpen, "no-open", false, "print the authorization link without opening a browser")
	fs.BoolVar(&o.noVerify, "no-verify", false, "skip the send-a-message connection check")
	fs.BoolVar(&o.force, "force", false, "overwrite an existing .claude/commands/bot.md")

	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("%w: %w", errUsage, err)
	}

	if (o.appID == "") != (o.secret == "") {
		return o, fmt.Errorf("%w: --app-id and --app-secret go together", errUsage)
	}

	return o, nil
}

func reportScopes(ctx context.Context, ch *lark.Channel) {
	switch missing, err := missingScopes(ctx, ch); {
	case err != nil:
		fmt.Printf("! 无法查询权限状态：%v\n", err)
	case len(missing) > 0:
		fmt.Printf("! 以下权限尚未生效（如果企业需要管理员审批，请在飞书里通过）：%s\n", strings.Join(missing, ", "))
	}
}

// obtainApp returns working credentials: from flags, from the existing
// config, or by registering a new app through the browser.
func obtainApp(ctx context.Context, existing *lark.Config, o initOptions, root string) (larkApp, error) {
	if o.appID != "" {
		la, err := connect(ctx, lark.Config{AppID: o.appID, AppSecret: o.secret, Domain: o.domain, Proxy: o.proxy})
		if err != nil {
			return la, fmt.Errorf("credentials check failed (nothing written): %w", err)
		}

		return la, nil
	}

	var upgrade string // existing app that only lacks permissions

	if existing != nil && !o.reregister {
		la, ready, err := checkExisting(ctx, *existing)
		if ready {
			fmt.Printf("✓ 已配置 bot「%s」，跳过注册（重新创建请加 --reregister）\n", la.bot.AppName)

			return la, nil
		}

		if err == nil {
			upgrade = existing.AppID
		}
	}

	lc, err := register(ctx, o, root, upgrade)
	if err != nil {
		return larkApp{}, err
	}

	return connectRegistered(ctx, lc)
}

// checkExisting reports whether the configured app is ready to use. A nil
// error with ready false means it works but lacks permissions.
func checkExisting(ctx context.Context, cfg lark.Config) (la larkApp, ready bool, err error) {
	la, err = connect(ctx, cfg)
	if err != nil {
		return la, false, err
	}

	return la, !lacksScopes(ctx, la.ch), nil
}

// lacksScopes reports whether ch is known to miss permissions. When the
// scopes cannot be queried it says no, rather than sending the user through
// the browser again for a working app.
func lacksScopes(ctx context.Context, ch *lark.Channel) bool {
	missing, err := missingScopes(ctx, ch)

	return err == nil && len(missing) > 0
}

// connectRegistered waits for a freshly registered app's credentials to work.
func connectRegistered(ctx context.Context, cfg lark.Config) (larkApp, error) {
	for check := 1; ; check++ {
		la, err := connect(ctx, cfg)
		if err == nil {
			return la, nil
		}

		if check == registerChecks {
			return la, fmt.Errorf("app registered (%s) but credentials check failed: %w", cfg.AppID, err)
		}

		select {
		case <-ctx.Done():
			return la, fmt.Errorf("app registered (%s), credentials not saved: %w", cfg.AppID, ctx.Err())
		case <-time.After(time.Duration(check) * registerDelay):
		}
	}
}

func register(ctx context.Context, o initOptions, root, upgradeAppID string) (lark.Config, error) {
	proxy, err := (lark.Config{Proxy: o.proxy}).ProxyFunc()
	if err != nil {
		return lark.Config{}, err
	}

	r := lark.NewRegistrar(proxy)
	r.Name = o.name
	r.Desc = "项目 " + filepath.Base(root) + " 的 Claude Code 值守机器人：私聊或在群里 @ 它，提问题、报 bug、提需求。"
	r.AppID = upgradeAppID
	r.OnLink = func(link string, expires time.Time) {
		action := "创建飞书应用"
		if upgradeAppID != "" {
			action = "为已有应用补充权限"
		}

		fmt.Printf("请在浏览器中登录飞书并确认，以%s（%s 前有效）：\n\n  %s\n\n", action, expires.Format("15:04"), link)

		if !o.noOpen {
			openBrowser(ctx, link)
		}

		fmt.Println("等待确认 …")
	}
	r.OnStatus = func(s string) {
		if s == "lark" {
			fmt.Println("检测到 Lark 国际版租户，切换到 larksuite.com …")
		}
	}

	reg, err := r.Register(ctx)
	if err != nil {
		return lark.Config{}, fmt.Errorf("register app: %w", err)
	}

	return lark.Config{AppID: reg.AppID, AppSecret: reg.AppSecret, Domain: reg.Domain, Proxy: o.proxy}, nil
}

// connect builds a channel for cfg and fetches its bot, which proves the
// credentials work.
func connect(ctx context.Context, cfg lark.Config) (larkApp, error) {
	ch, err := lark.New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return larkApp{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	bot, err := ch.Bot(ctx)

	return larkApp{cfg: cfg, ch: ch, bot: bot}, err
}

func missingScopes(ctx context.Context, ch *lark.Channel) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	return ch.MissingScopes(ctx)
}

// verifyConnection proves the round trip: the user sends the bot a direct
// message, the bot answers it. Anything else that arrives meanwhile is kept
// in the inbox.
func verifyConnection(ctx context.Context, dir string, la larkApp) error {
	a := assemble(dir, newLogger(), la.ch)

	if _, alive := readHeartbeat(a.dir); alive {
		fmt.Println("✓ 监听进程已在运行，跳过连接验证")

		return nil
	}

	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	got := make(chan channel.Message, 1)
	store := a.deliverer(vctx, la.ch)
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
	go func() { listenErr <- la.ch.Listen(vctx, deliver, func() {}) }()

	fmt.Printf("\n连接验证：请在飞书里搜索「%s」，给它发一条私聊消息（%s 内）…\n", la.bot.AppName, verifyTimeout)

	select {
	case m := <-got:
		if err := la.ch.Reply(vctx, m, "✅ 连接成功！在仓库里运行 `/loop /bot` 后，我就会处理发给我的消息。"); err != nil {
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

func openBrowser(ctx context.Context, link string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", link) //nolint:gosec // link was validated as an official Lark URL
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", link) //nolint:gosec // link was validated as an official Lark URL
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", link) //nolint:gosec // link was validated as an official Lark URL
	}

	_ = cmd.Start() // best effort; the link is printed too
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

	if err := os.WriteFile(path, raw, 0o644); err != nil { //nolint:gosec // .gitignore is meant to be world-readable
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

func writeIfAbsent(path string, content []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(os.Stderr, "claude-bot: kept existing %s (use --force to overwrite)\n", path)

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // slash command is shared with the team
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
