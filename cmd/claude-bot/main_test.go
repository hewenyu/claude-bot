package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// fakeChannel records calls; it stands in for a real platform adapter.
type fakeChannel struct {
	mu    sync.Mutex
	acked []string
}

func (f *fakeChannel) Name() string { return "fake" }

func (f *fakeChannel) Listen(context.Context, func(channel.Message) error, func()) error { return nil }

func (f *fakeChannel) Reply(context.Context, channel.Message, string) error { return nil }

func (f *fakeChannel) Send(context.Context, string, string) error { return nil }

func (f *fakeChannel) Get(context.Context, string) (channel.Message, error) {
	return channel.Message{}, nil
}

func (f *fakeChannel) Download(context.Context, channel.Message, channel.Attachment, string) (string, error) {
	return "", nil
}

func (f *fakeChannel) Ack(_ context.Context, m channel.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.acked = append(f.acked, m.ID)

	return nil
}

func (f *fakeChannel) ackCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.acked)
}

func TestDeliverer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fc := &fakeChannel{}
	a := &app{dir: dir, inbox: inbox.New(filepath.Join(dir, "inbox")), log: slog.New(slog.DiscardHandler)}
	deliver := a.deliverer(t.Context(), fc)

	m := channel.Message{ID: "fake:1", Channel: "fake", ChatType: channel.ChatDirect, CreatedAt: time.Now()}

	for range 3 { // the platform re-delivers unacknowledged events
		if err := deliver(m); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for fc.ackCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(20 * time.Millisecond)

	if n := fc.ackCount(); n != 1 {
		t.Errorf("acked %d times, want exactly once", n)
	}

	pending, err := a.inbox.Pending()
	if err != nil || len(pending) != 1 {
		t.Errorf("pending = %d, %v; want 1", len(pending), err)
	}
}

func TestEnsureLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("bin/"), 0o600); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := ensureLine(path, ".claude-bot/"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if want := "bin/\n.claude-bot/\n"; string(got) != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
}

func TestWriteIfAbsent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".claude", "commands", "bot.md")

	if err := writeIfAbsent(path, []byte("v1"), false); err != nil {
		t.Fatal(err)
	}

	if err := writeIfAbsent(path, []byte("v2"), false); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(path); string(got) != "v1" {
		t.Errorf("existing file overwritten without --force: %q", got)
	}

	if err := writeIfAbsent(path, []byte("v3"), true); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(path); string(got) != "v3" {
		t.Errorf("--force did not overwrite: %q", got)
	}
}

func TestTextArg(t *testing.T) {
	t.Parallel()

	got, err := textArg([]string{"fixed", "in", "v1.2.3"})
	if err != nil || got != "fixed in v1.2.3" {
		t.Errorf("textArg(args) = %q, %v", got, err)
	}
}

func TestRun_usage(t *testing.T) {
	t.Parallel()

	if code := run(t.Context(), []string{"no-such-command"}); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}

	if code := run(t.Context(), []string{"done"}); code != 2 {
		t.Errorf("done without ids exit = %d, want 2", code)
	}
}

func TestBotCommandEmbedded(t *testing.T) {
	t.Parallel()

	if len(botCommand) == 0 {
		t.Fatal("bot.md slash command not embedded")
	}
}

func TestParseReplyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want replyArgs
	}{
		{name: "flag first", args: []string{"--keep", "lark:om_1", "on it"}, want: replyArgs{id: "lark:om_1", text: []string{"on it"}, keep: true}},
		{name: "flag after id", args: []string{"lark:om_1", "--keep", "on it"}, want: replyArgs{id: "lark:om_1", text: []string{"on it"}, keep: true}},
		{name: "markdown list is text", args: []string{"lark:om_1", "- fixed"}, want: replyArgs{id: "lark:om_1", text: []string{"- fixed"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseReplyArgs(tt.args)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseReplyArgs(%q) = %+v, %v; want %+v", tt.args, got, err, tt.want)
			}
		})
	}
}

func TestDone_continuesPastUnknownID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LARK_APP_ID", "cli_test")
	t.Setenv("LARK_APP_SECRET", "secret")

	b := inbox.New(filepath.Join(root, ".claude-bot", "inbox"))
	if _, err := b.Put(channel.Message{ID: "lark:om_b", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if code := run(t.Context(), []string{"-C", root, "done", "lark:om_missing", "lark:om_b"}); code != 1 {
		t.Errorf("exit = %d, want 1 for the unknown id", code)
	}

	if pending, err := b.Pending(); err != nil || len(pending) != 0 {
		t.Errorf("pending = %d, %v; the known id must still be marked done", len(pending), err)
	}
}

func TestBeat_waitsForReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := assemble(dir, slog.New(slog.DiscardHandler), &fakeChannel{})
	ready := make(chan struct{})

	go a.beat(t.Context(), ready)

	time.Sleep(20 * time.Millisecond)

	if _, alive := readHeartbeat(dir); alive {
		t.Fatal("heartbeat written before every channel was ready")
	}

	close(ready)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hb, alive := readHeartbeat(dir); alive {
			if !reflect.DeepEqual(hb.Channels, []string{"fake"}) {
				t.Errorf("heartbeat channels = %v", hb.Channels)
			}

			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Error("no heartbeat after ready")
}

func TestParseInitFlags_settlesChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args    []string
		channel string // "" with ok: chosen later (menu or Lark)
		ok      bool
	}{
		{args: nil, ok: true},
		{args: []string{"--channel", "slack"}, channel: "slack", ok: true},
		{args: []string{"--telegram-token", "1:t"}, channel: "telegram", ok: true},
		{args: []string{"--slack-app-token", "xapp-1", "--slack-bot-token", "xoxb-1"}, channel: "slack", ok: true},
		{args: []string{"--app-id", "cli_1", "--app-secret", "s"}, channel: "lark", ok: true},
		{args: []string{"--channel", "telegram", "--telegram-token", "1:t"}, channel: "telegram", ok: true},
		{args: []string{"--bot-md"}, ok: true},
		{args: []string{"--channel", "wechat"}},
		{args: []string{"--channel", "discord", "--telegram-token", "1:t"}},
		{args: []string{"--slack-bot-token", "xoxb-1"}},
		{args: []string{"--telegram-token", "1:t", "--discord-token", "d"}},
		{args: []string{"--app-id", "cli_1", "--app-secret", "s", "--slack-app-token", "xapp-1", "--slack-bot-token", "xoxb-1"}},
		{args: []string{"--bot-md", "--channel", "lark"}},
	}

	for _, tt := range tests {
		o, err := parseInitFlags(tt.args)
		if (err == nil) != tt.ok {
			t.Errorf("parseInitFlags(%q) error = %v, want ok %v", tt.args, err, tt.ok)

			continue
		}

		if err != nil && !errors.Is(err, errUsage) {
			t.Errorf("parseInitFlags(%q) error = %v, want a usage error", tt.args, err)
		}

		if err == nil && o.channel != tt.channel {
			t.Errorf("parseInitFlags(%q) channel = %q, want %q", tt.args, o.channel, tt.channel)
		}
	}
}

func testPrompter(input string, secrets ...string) (*prompter, *strings.Builder) {
	out := &strings.Builder{}

	return &prompter{
		in:  bufio.NewReader(strings.NewReader(input)),
		out: out,
		secret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}

			s := secrets[0]
			secrets = secrets[1:]

			return s, nil
		},
	}, out
}

func TestChooseChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input, want string
	}{
		{input: "\n", want: "lark"},
		{input: "3\n", want: "telegram"},
		{input: "4", want: "slack"}, // no trailing newline
		{input: "x\n9\n2\n", want: "discord"},
	}

	for _, tt := range tests {
		p, _ := testPrompter(tt.input)

		got, err := p.chooseChannel(config.Config{})
		if err != nil || got != tt.want {
			t.Errorf("chooseChannel(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
		}
	}

	p, _ := testPrompter("x\n")
	if _, err := p.chooseChannel(config.Config{}); !errors.Is(err, errNoInput) {
		t.Errorf("chooseChannel at end of input = %v, want errNoInput", err)
	}
}

func TestChooseChannel_marksConfigured(t *testing.T) {
	t.Parallel()

	p, out := testPrompter("\n")
	if _, err := p.chooseChannel(config.Config{Telegram: &telegram.Config{Token: "t"}}); err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(line, "Telegram") != strings.Contains(line, "已配置") {
			t.Errorf("menu line %q: only Telegram should be marked configured", line)
		}
	}
}

func TestAskTokens(t *testing.T) {
	t.Parallel()

	p, _ := testPrompter("", " xapp-1 ", "xoxb-1")

	o := initOptions{channel: "slack"}
	if err := askTokens(&o, p); err != nil {
		t.Fatal(err)
	}

	if o.slackAppToken != "xapp-1" || o.slackBotToken != "xoxb-1" {
		t.Errorf("tokens = %q, %q", o.slackAppToken, o.slackBotToken)
	}

	given := initOptions{channel: "telegram", telegramToken: "1:t"}
	if err := askTokens(&given, nil); err != nil || given.telegramToken != "1:t" {
		t.Errorf("askTokens with the flag given = %v, %q", err, given.telegramToken)
	}

	if err := askTokens(&initOptions{channel: "discord"}, nil); !errors.Is(err, errUsage) {
		t.Errorf("askTokens without terminal or flag = %v, want a usage error naming the flag", err)
	}

	empty, _ := testPrompter("", "  ")
	if err := askTokens(&initOptions{channel: "telegram"}, empty); !errors.Is(err, errUsage) {
		t.Errorf("askTokens with an empty answer = %v, want a usage error", err)
	}
}

func TestRewriteBotCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := botCommandPath(root)

	if err := writeIfAbsent(path, []byte("edited by hand"), false); err != nil {
		t.Fatal(err)
	}

	if err := rewriteBotCommand(root); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, botCommand) {
		t.Errorf("bot.md after rewrite = %.40q…, %v; want the embedded command", got, err)
	}
}
