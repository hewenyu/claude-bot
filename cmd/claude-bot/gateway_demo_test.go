//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/screen"
	"github.com/hewenyu/claude-bot/internal/store"
)

// pageDemoEnv names the address to serve a demo of the gateway's page on:
//
//	CLAUDE_BOT_PAGE_DEMO=127.0.0.1:7601 go test -run TestPageDemo -timeout 0 ./cmd/claude-bot
//
// then open http://127.0.0.1:7601/#token=de30de30. The demo has a project in
// every state, with fake channels and made-up conversations, in a
// temporary home: nothing connects anywhere.
const (
	pageDemoEnv = "CLAUDE_BOT_PAGE_DEMO"
	demoToken   = "de30de30"
)

// demoChannel is a fake connection: it stays up, or fails with err.
type demoChannel struct {
	fakeChannel

	name string
	err  error
}

func (d *demoChannel) Name() string { return d.name }

func (d *demoChannel) Listen(ctx context.Context, _ func(channel.Message) error, ready func()) error {
	if d.err != nil {
		return d.err
	}

	ready()
	<-ctx.Done()

	return nil
}

// demoFailures are the channels that fail in the demo, as they would.
var demoFailures = map[string]error{
	"dingtalk": errors.New("dingtalk: open stream: 403 Forbidden: the app has no Stream mode robot; turn it on in the developer console"),
	"slack":    errors.New("slack: auth.test: invalid_auth"),
}

func demoBuild(_ string, cfg config.Config, log *slog.Logger) (*app, error) {
	var chans []channel.Channel

	for name, set := range map[string]bool{
		"lark": cfg.Lark != nil, "github": cfg.GitHub != nil, "dingtalk": cfg.DingTalk != nil,
		"telegram": cfg.Telegram != nil, "discord": cfg.Discord != nil, "slack": cfg.Slack != nil,
	} {
		if !set {
			continue
		}

		if name == "slack" {
			return nil, demoFailures[name] // fails before it connects
		}

		chans = append(chans, &demoChannel{name: name, err: demoFailures[name]})
	}

	if len(chans) == 0 {
		return nil, errNotConfigured
	}

	return assemble("", log, chans...), nil
}

func TestPageDemo(t *testing.T) { //nolint:paralleltest // serves until killed
	addr := os.Getenv(pageDemoEnv)
	if addr == "" {
		t.Skip("set " + pageDemoEnv + " to serve a demo of the page")
	}

	ctx := t.Context()

	home, err := config.Home()
	if err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, home)
	if err != nil {
		t.Fatal(err)
	}

	for key, v := range map[string]string{store.SettingTypeSafeKey: "ts-demo-key-7f3a", store.SettingUIToken: demoToken} {
		if err := s.SetSetting(ctx, key, v); err != nil {
			t.Fatal(err)
		}
	}

	seedDemo(t, s)

	g := newGateway(s, slog.New(slog.DiscardHandler))
	g.build = func(dir string, cfg config.Config, log *slog.Logger) (*app, error) {
		a, err := demoBuild(dir, cfg, log)
		if a != nil {
			a.dir, a.inbox, a.typesafe = dir, inbox.New(filepath.Join(dir, inboxDir)), cfg.TypeSafe
		}

		return a, err
	}
	g.ask = func(string) screen.Asker { return stuffing{} }

	if g.ui, err = g.serveWeb(ctx, addr, demoToken); err != nil {
		t.Fatal(err)
	}

	t.Logf("demo page: %s#token=%s (home %s)", g.ui, demoToken, home)

	_ = g.loop(ctx, filepath.Join(home, gatewayBeat))
}

// demoProject registers a project under root with channels.
func demoProject(t *testing.T, s *store.Store, root, name string, chans map[string]string) store.Project {
	t.Helper()

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, config.DirName), 0o750); err != nil {
		t.Fatal(err)
	}

	rows := make([]store.Channel, 0, len(chans))

	for ch, raw := range chans {
		cfg, err := config.ChannelConfig(ch, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}

		row, err := cfg.Channels()
		if err != nil {
			t.Fatal(err)
		}

		rows = append(rows, row...)
	}

	p, err := s.SaveProject(t.Context(), dir, rows)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func seedDemo(t *testing.T, s *store.Store) {
	t.Helper()

	root := t.TempDir()

	if err := s.SetSetting(t.Context(), store.SettingGitHubToken, "ghp_demo-not-a-token-c9d2"); err != nil {
		t.Fatal(err)
	}

	shop := demoProject(t, s, root, "shop-api", map[string]string{
		"lark":   `{"app_id":"cli_a5f1d2e3b4c5","app_secret":"demo-lark-secret-91ab"}`,
		"github": `{"repo":"acme/shop-api"}`,
	})
	demoShopTasks(t, inbox.New(filepath.Join(shop.Dir, config.DirName, inboxDir)))

	docs := demoProject(t, s, root, "docs-site", map[string]string{
		"telegram": `{"token":"7012345678:AAH-demo-telegram-token-x81Q"}`,
		"dingtalk": `{"client_id":"dingdemo8k2","client_secret":"demo-ding-secret-77fe"}`,
	})
	demoDocsTasks(t, inbox.New(filepath.Join(docs.Dir, config.DirName, inboxDir)))

	demoProject(t, s, root, "ml-pipeline", map[string]string{
		"slack": `{"app_token":"xapp-1-A0DEMO-1-abcd","bot_token":"xoxb-demo-slack-e5f6"}`,
	})

	mobile := demoProject(t, s, root, "mobile-app", map[string]string{
		"discord": `{"token":"MTIzNDU2Nzg5.demo.discord-token-0a1b"}`,
	})

	// mobile-app still has a listener of its own: the gateway waits for it.
	lock := filepath.Join(mobile.Dir, config.DirName, listenerLock)

	unlock, err := lockFile(lock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(unlock)

	if err := os.WriteFile(lock, []byte("48213"), privatePerm); err != nil {
		t.Fatal(err)
	}

	demoProject(t, s, root, "legacy-cron", nil)

	// A project of another account, on a GitHub token of its own.
	if err := s.SetSetting(t.Context(), store.SettingGitHubTokenPrefix+"personal", "ghp_demo-personal-not-a-token-41f0"); err != nil {
		t.Fatal(err)
	}

	oss := demoProject(t, s, root, "oss-tools", map[string]string{"github": `{"repo":"me/oss-tools","token_name":"personal"}`})

	for _, p := range []store.Project{shop, oss} {
		repo := map[string]string{"shop-api": "acme/shop-api", "oss-tools": "me/oss-tools"}[p.Name]
		demoGit(t, p.Dir, "init", "-q")
		demoGit(t, p.Dir, "remote", "add", "origin", "git@github.com:"+repo+".git")
	}
}

func demoGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// demoTask builds a task from lines: "who: text" for users, "bot: text" for
// replies, at minutes after start.
type demoLine struct {
	who  string
	min  int
	text string
	file string
}

func saveDemoTask(t *testing.T, box *inbox.Inbox, tk inbox.Task, lines []demoLine, handled int, pending bool) {
	t.Helper()

	start := tk.CreatedAt

	for i, l := range lines {
		at := start.Add(time.Duration(l.min) * time.Minute)
		if l.who == "bot" {
			tk.Entries = append(tk.Entries, inbox.Entry{Kind: inbox.EntryBot, At: at, Text: l.text})

			continue
		}

		msg := fmt.Sprintf("%s:om_%d", tk.Channel, i)
		tk.Append(msg, []inbox.Entry{{Kind: inbox.EntryUser, Message: msg, Sender: &channel.User{ID: "ou_" + l.who, Name: l.who}, At: at, Text: l.text, File: l.file}})
	}

	tk.Handled = min(handled, len(tk.Entries))
	if handled < 0 {
		tk.Handled = len(tk.Entries)
	}

	if err := box.Locked(func() error { return box.SaveTask(tk, pending) }); err != nil {
		t.Fatal(err)
	}
}

func demoShopTasks(t *testing.T, box *inbox.Inbox) {
	t.Helper()

	now := time.Now().UTC()
	task := func(id, ch, chat string, typ channel.ChatType, ago time.Duration) inbox.Task {
		return inbox.Task{ID: "task:" + id, Channel: ch, ChatID: chat, ChatType: typ, CreatedAt: now.Add(-ago)}
	}

	saveDemoTask(t, box, task("mz1a01", "lark", "oc_ops", channel.ChatGroup, 3*time.Hour), []demoLine{
		{who: "王芳", text: "@bot 支付回调这两天偶尔 502，帮忙看看是哪里的问题"},
		{who: "王芳", min: 1, text: "这是昨晚的网关日志", file: "/Users/demo/code/shop-api/.claude-bot/files/gateway-0412.log"},
		{who: "bot", min: 9, text: "找到了：`/pay/notify` 在验签时同步调用风控服务，风控超过 **5s** 没响应时网关就返回 502。\n\n已经在 PR #214 里：\n- 把风控调用改成异步，验签后立即返回 200\n- 风控结果写回订单，失败的进重试队列\n\n本地压测 2000 次回调没有再出现 502。"},
		{who: "王芳", min: 15, text: "好的，我去 review"},
		{who: "bot", min: 16, text: "收到，合并后我再跟进一下线上的错误率。"},
	}, -1, false)

	saveDemoTask(t, box, task("mz1b02", "github", "acme/shop-api/231", channel.ChatGroup, 50*time.Minute), []demoLine{
		{who: "octocat", text: "Coupon stacking lets a user apply the same code twice\n\nSteps: add code SAVE10, refresh the cart, add it again."},
		{who: "bot", min: 12, text: "Reproduced. The cart dedupes codes in memory only, so a refresh loses it. Fixing it in the database with a unique index; PR coming."},
		{who: "octocat", min: 31, text: "Thanks! Can you also check the checkout page? It shows the discount twice."},
	}, 2, true)

	saveDemoTask(t, box, task("mz1c03", "lark", "oc_dm_li", channel.ChatDirect, 20*time.Minute), []demoLine{
		{who: "李雷", text: "订单导出 CSV 能不能加一列「优惠金额」？财务月底要用"},
	}, 0, true)

	saveDemoTask(t, box, task("mz1d04", "lark", "oc_ops", channel.ChatGroup, 26*time.Hour), []demoLine{
		{who: "张伟", text: "收到，谢谢"},
	}, -1, false)

	saveDemoTask(t, box, task("mz1e05", "github", "acme/shop-api/228", channel.ChatGroup, 30*time.Hour), []demoLine{
		{who: "dependabot", text: "Bump golang.org/x/net from 0.38.0 to 0.41.0"},
		{who: "bot", min: 25, text: "CI 全绿，变更只涉及 http2 的修复，已合并。"},
	}, -1, false)

	// Shown so the page reports it as being worked on.
	tk, _, err := box.Task("task:mz1b02")
	if err != nil {
		t.Fatal(err)
	}

	tk.Shown = len(tk.Entries)

	if err := box.Locked(func() error { return box.SaveTask(tk, true) }); err != nil {
		t.Fatal(err)
	}
}

func demoDocsTasks(t *testing.T, box *inbox.Inbox) {
	t.Helper()

	now := time.Now().UTC()

	saveDemoTask(t, box, inbox.Task{ID: "task:mz2a01", Channel: "telegram", ChatID: "-100223", ChatType: channel.ChatGroup, CreatedAt: now.Add(-5 * time.Hour)}, []demoLine{
		{who: "alice", text: "The search box on the docs site returns nothing for Chinese queries"},
		{who: "bot", min: 18, text: "The index was built with an English-only tokenizer. Switched it to a CJK bigram tokenizer and rebuilt: `支付` now finds 14 pages."},
	}, -1, false)
}
