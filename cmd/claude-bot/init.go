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

const verifyTimeout = 3 * time.Minute

type initOptions struct {
	appID, secret, domain, proxy, name  string
	reregister, noOpen, noVerify, force bool
}

func cmdInit(ctx context.Context, args []string) error {
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
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	if (o.appID == "") != (o.secret == "") {
		return fmt.Errorf("%w: --app-id and --app-secret go together", errUsage)
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

	lc, bot, err := obtainApp(ctx, cfg.Lark, o, root)
	if err != nil {
		return err
	}

	cfg.Lark = &lc
	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	if err := ensureLine(filepath.Join(root, ".gitignore"), config.DirName+"/"); err != nil {
		return err
	}

	if err := writeIfAbsent(filepath.Join(root, ".claude", "commands", "bot.md"), botCommand, o.force); err != nil {
		return err
	}

	fmt.Printf("✓ bot「%s」已接入 %s\n", bot.AppName, root)

	if missing := missingScopes(ctx, lc); len(missing) > 0 {
		fmt.Printf("! 以下权限尚未生效（如果企业需要管理员审批，请在飞书里通过）：%s\n", strings.Join(missing, ", "))
	}

	if !o.noVerify {
		if err := verifyConnection(ctx, bot); err != nil {
			return err
		}
	}

	fmt.Println("\n下一步：在这个仓库里打开 Claude Code，运行  /loop /bot")
	fmt.Println("把 bot 拉进项目群，大家私聊它或在群里 @ 它即可。")

	return nil
}

// obtainApp returns working credentials: from flags, from the existing
// config, or by registering a new app through the browser.
func obtainApp(ctx context.Context, existing *lark.Config, o initOptions, root string) (lark.Config, lark.Bot, error) {
	if o.appID != "" {
		lc := lark.Config{AppID: o.appID, AppSecret: o.secret, Domain: o.domain, Proxy: o.proxy}

		bot, err := checkLark(ctx, lc)
		if err != nil {
			return lc, bot, fmt.Errorf("credentials check failed (nothing written): %w", err)
		}

		return lc, bot, nil
	}

	var upgrade string // existing app that only lacks permissions

	if existing != nil && !o.reregister {
		if bot, err := checkLark(ctx, *existing); err == nil {
			if len(missingScopes(ctx, *existing)) == 0 {
				fmt.Printf("✓ 已配置 bot「%s」，跳过注册（重新创建请加 --reregister）\n", bot.AppName)

				return *existing, bot, nil
			}

			upgrade = existing.AppID
		}
	}

	lc, err := register(ctx, o, root, upgrade)
	if err != nil {
		return lc, lark.Bot{}, err
	}

	// A freshly created app can take a few seconds before its token works.
	var bot lark.Bot
	for attempt := range 5 {
		if bot, err = checkLark(ctx, lc); err == nil {
			return lc, bot, nil
		}

		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}

	return lc, bot, fmt.Errorf("app registered (%s) but credentials check failed: %w", lc.AppID, err)
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

func checkLark(ctx context.Context, cfg lark.Config) (lark.Bot, error) {
	ch, err := lark.New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return lark.Bot{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	return ch.Bot(ctx)
}

func missingScopes(ctx context.Context, cfg lark.Config) []string {
	ch, err := lark.New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return lark.Scopes
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	missing, err := ch.MissingScopes(ctx)
	if err != nil {
		return []string{"(无法查询: " + err.Error() + ")"}
	}

	return missing
}

// verifyConnection proves the round trip: the user sends the bot a direct
// message, the bot answers it. Anything else that arrives meanwhile is kept
// in the inbox.
func verifyConnection(ctx context.Context, bot lark.Bot) error {
	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	if _, alive := readHeartbeat(a.dir); alive {
		fmt.Println("✓ 监听进程已在运行，跳过连接验证")

		return nil
	}

	ch := a.channels[lark.Name]

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	got := make(chan channel.Message, 1)
	store := a.deliverer(ctx, ch)
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
	go func() { listenErr <- ch.Listen(ctx, deliver, func() {}) }()

	fmt.Printf("\n连接验证：请在飞书里搜索「%s」，给它发一条私聊消息（%s 内）…\n", bot.AppName, verifyTimeout)

	select {
	case m := <-got:
		if err := ch.Reply(ctx, m, "✅ 连接成功！在仓库里运行 `/loop /bot` 后，我就会处理发给我的消息。"); err != nil {
			return fmt.Errorf("received your message but replying failed: %w", err)
		}

		if err := a.inbox.Done(m.ID); err != nil {
			return err
		}

		fmt.Println("✓ 收发都已打通")

		return nil
	case err := <-listenErr:
		return fmt.Errorf("connection check: %w", err)
	case <-ctx.Done():
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
