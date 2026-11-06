package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/config"
)

// fakeChannel records calls; it stands in for a real platform adapter.
type fakeChannel struct {
	mu      sync.Mutex
	acked   []string
	replies []string // "<id>: <text>"
}

func (f *fakeChannel) Name() string { return "fake" }

func (f *fakeChannel) Listen(context.Context, func(channel.Message) error, func()) error { return nil }

func (f *fakeChannel) Reply(_ context.Context, m channel.Message, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.replies = append(f.replies, m.ID+": "+text)

	return nil
}

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

// listenerHelperEnv makes the test binary act as a listener: it holds the
// lock named by the variable, with its pid in it, until SIGTERM.
const listenerHelperEnv = "CLAUDE_BOT_TEST_LISTENER"

func TestMain(m *testing.M) {
	if lock := os.Getenv(listenerHelperEnv); lock != "" {
		os.Exit(fakeListener(lock))
	}

	// No test may touch the user's gateway: they all share an empty home,
	// unless they set one of their own.
	home, err := os.MkdirTemp("", "claude-bot-home-")
	if err != nil {
		panic(err)
	}

	os.Setenv(config.HomeEnv, home) //nolint:errcheck // before any test runs

	code := m.Run()

	_ = os.RemoveAll(home)

	os.Exit(code)
}

// fakeListener holds each lock in locks (a path list), with its pid in it.
func fakeListener(locks string) int {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)

	for lock := range strings.SplitSeq(locks, string(os.PathListSeparator)) {
		unlock, err := lockFile(lock)
		if err != nil {
			return 1
		}
		defer unlock()

		if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), privatePerm); err != nil {
			return 1
		}
	}

	os.Stdout.WriteString("ready\n") //nolint:errcheck // the test waits for it
	<-term

	return 0
}
