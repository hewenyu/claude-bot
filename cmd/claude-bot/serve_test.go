package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

func TestDeliverer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fc := &fakeChannel{}
	a := &app{dir: dir, inbox: inbox.New(filepath.Join(dir, "inbox")), log: slog.New(slog.DiscardHandler)}
	deliver := a.deliverer(t.Context(), fc, nil)

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

	incoming, err := a.inbox.Incoming()
	if err != nil || len(incoming) != 1 {
		t.Errorf("incoming = %d, %v; want 1, held for screening", len(incoming), err)
	}
}

func TestBeat_waitsForReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := assemble(dir, slog.New(slog.DiscardHandler), &fakeChannel{})
	ready := make(chan struct{})

	go a.beat(t.Context(), ready, nil)

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

// liveChannel is a channel whose Listen connects and then waits for ctx or fail.
type liveChannel struct {
	fakeChannel

	name string
	fail chan error
}

func (l *liveChannel) Name() string { return l.name }

func (l *liveChannel) Listen(ctx context.Context, _ func(channel.Message) error, ready func()) error {
	ready()

	select {
	case <-ctx.Done():
		return nil
	case err := <-l.fail:
		return err
	}
}

func TestListen_stopsOnlyTheFailedChannel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lark := &liveChannel{name: "lark", fail: make(chan error)}
	gh := &liveChannel{name: "github", fail: make(chan error)}
	a := assemble(dir, slog.New(slog.DiscardHandler), lark, gh)

	done := make(chan error, 1)

	go func() { done <- a.listen(t.Context(), new(sync.WaitGroup), nil) }()

	waitBeat := func(want heartbeat) {
		t.Helper()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if hb, alive := readHeartbeat(dir); alive && reflect.DeepEqual(hb.Channels, want.Channels) && reflect.DeepEqual(hb.Stopped, want.Stopped) {
				return
			}

			time.Sleep(5 * time.Millisecond)
		}

		hb, _ := readHeartbeat(dir)
		t.Fatalf("heartbeat channels = %v, stopped = %v; want %v, %v", hb.Channels, hb.Stopped, want.Channels, want.Stopped)
	}

	waitBeat(heartbeat{Channels: []string{"github", "lark"}})

	gh.fail <- errors.New("401 Bad credentials")

	waitBeat(heartbeat{Channels: []string{"lark"}, Stopped: []string{"github"}})

	select {
	case err := <-done:
		t.Fatalf("listen returned %v after one of two channels failed", err)
	case <-time.After(20 * time.Millisecond):
	}

	lark.fail <- errors.New("lark long connection: closed")

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "github: 401") || !strings.Contains(err.Error(), "lark: lark long connection") {
			t.Errorf("listen() = %v, want both channel errors", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listen still running after every channel failed")
	}
}

// A channel that fails before it ever connects must not hold up the
// heartbeat of the others.
func TestListen_failsBeforeConnecting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bad := &failing{liveChannel{name: "github"}}
	a := assemble(dir, slog.New(slog.DiscardHandler), &liveChannel{name: "lark"}, bad)

	go func() { _ = a.listen(t.Context(), new(sync.WaitGroup), nil) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hb, alive := readHeartbeat(dir); alive {
			if !reflect.DeepEqual(hb.Channels, []string{"lark"}) || !reflect.DeepEqual(hb.Stopped, []string{"github"}) {
				t.Errorf("heartbeat channels = %v, stopped = %v; want [lark], [github]", hb.Channels, hb.Stopped)
			}

			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Error("no heartbeat while lark is connected")
}

// failing is a channel whose Listen fails at once, e.g. on a revoked token.
type failing struct{ liveChannel }

func (f *failing) Listen(context.Context, func(channel.Message) error, func()) error {
	return errors.New("401 Bad credentials")
}
