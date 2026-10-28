package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
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
