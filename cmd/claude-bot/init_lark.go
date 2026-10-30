package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/config"
	"golang.org/x/term"
)

// A freshly registered app can take a few seconds before its token works.
const (
	registerChecks = 5
	registerDelay  = 2 * time.Second // grows linearly between checks
)

// larkApp is a set of credentials proven to work, with the bot they belong to.
type larkApp struct {
	cfg lark.Config
	ch  *lark.Channel
	bot lark.Bot
}

// setupLark obtains a working Lark app (registering one through the browser
// when needed) and records it in cfg.
func setupLark(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	la, err := obtainApp(ctx, cfg.Lark, o)
	if err != nil {
		return setup{}, err
	}

	cfg.Lark = &la.cfg

	return setup{
		ch:     la.ch,
		name:   la.bot.AppName,
		prompt: fmt.Sprintf("请在飞书里搜索「%s」，给它发一条私聊消息", la.bot.AppName),
		report: func(ctx context.Context) { reportScopes(ctx, la.ch) },
	}, nil
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
func obtainApp(ctx context.Context, existing *lark.Config, o initOptions) (larkApp, error) {
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

	lc, err := register(ctx, o, upgrade)
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

func register(ctx context.Context, o initOptions, upgradeAppID string) (lark.Config, error) {
	proxy, err := (lark.Config{Proxy: o.proxy}).ProxyFunc()
	if err != nil {
		return lark.Config{}, err
	}

	r := lark.NewRegistrar(proxy)
	r.Name = o.name
	// Kept short (the name already says which project): it travels in the
	// link, and a longer link makes the QR code too wide for the terminal.
	r.Desc = "Claude Code 值守机器人"
	r.AppID = upgradeAppID
	r.OnLink = func(link string, expires time.Time) {
		action := "创建飞书应用"
		if upgradeAppID != "" {
			action = "为已有应用补充权限"
		}

		fmt.Printf("请在浏览器中登录飞书并确认，以%s（%s 前有效）：\n\n  %s\n\n", action, expires.Format("15:04"), link)

		printLinkQR(link)

		if !o.noOpen {
			openBrowser(ctx, link)
		}

		fmt.Println("等待确认 …")
	}
	r.OnTenantSwitch = func(string) {
		fmt.Println("检测到 Lark 国际版租户，切换到 larksuite.com …")
	}

	reg, err := r.Register(ctx)
	if err != nil {
		return lark.Config{}, fmt.Errorf("register app: %w", err)
	}

	return lark.Config{AppID: reg.AppID, AppSecret: reg.AppSecret, Domain: reg.Domain, Proxy: o.proxy}, nil
}

// printLinkQR shows link as a QR code for the Lark mobile app, when stdout is
// a terminal wide enough to draw it without wrapping.
func printLinkQR(link string) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return
	}

	code, width, err := renderQR(link)
	if err != nil {
		fmt.Printf("（二维码生成失败：%v）\n\n", err)

		return
	}

	if cols, _, err := term.GetSize(fd); err == nil && cols < width {
		fmt.Printf("（终端宽度不足 %d 列，放不下二维码；拉宽窗口后重新运行可用手机扫码）\n\n", width)

		return
	}

	fmt.Printf("也可以用飞书 App 扫码：\n\n%s\n", code)
}

// connect builds a channel for cfg and fetches its bot, which proves the
// credentials work.
func connect(ctx context.Context, cfg lark.Config) (larkApp, error) {
	ch, err := lark.New(cfg, quiet)
	if err != nil {
		return larkApp{}, err
	}

	bot, err := withTimeout(ctx, ch.Bot)

	return larkApp{cfg: cfg, ch: ch, bot: bot}, err
}

func missingScopes(ctx context.Context, ch *lark.Channel) ([]string, error) {
	return withTimeout(ctx, ch.MissingScopes)
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
