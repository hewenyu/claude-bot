package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	heartbeatEvery  = 15 * time.Second
	heartbeatStale  = 45 * time.Second
	listenerStartup = 30 * time.Second
)

// heartbeat is written to .claude-bot/serve.json while the listener runs.
type heartbeat struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	BeatAt    time.Time `json:"beat_at"`
	Channels  []string  `json:"channels"`
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	unlock, err := lockFile(filepath.Join(a.dir, "serve.lock"))
	if err != nil {
		return fmt.Errorf("listener already running for %s: %w", filepath.Dir(a.dir), err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The heartbeat means "connected": it starts once every channel is ready
	// and disappears when the listener exits.
	defer func() { _ = os.Remove(filepath.Join(a.dir, "serve.json")) }()

	var ready atomic.Int32

	go a.beat(ctx, func() bool { return int(ready.Load()) == len(a.channels) })

	errs := make(chan error, len(a.channels))
	for _, ch := range a.channels {
		var once sync.Once

		markReady := func() { once.Do(func() { ready.Add(1) }) }

		go func() { errs <- ch.Listen(ctx, a.deliverer(ctx, ch), markReady) }()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errs:
		return err
	}
}

func (a *app) deliverer(ctx context.Context, ch channel.Channel) func(channel.Message) error {
	return func(m channel.Message) error {
		fresh, err := a.inbox.Put(m)
		if err != nil {
			a.log.Error("store message", "id", m.ID, "err", err)

			return err
		}

		if !fresh {
			return nil
		}

		a.log.Info("message", "id", m.ID, "chat", m.ChatType, "from", m.Sender.ID)

		go func() {
			ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()

			if err := ch.Ack(ackCtx, m); err != nil {
				a.log.Warn("ack failed", "id", m.ID, "err", err)
			}
		}()

		return nil
	}
}

func (a *app) beat(ctx context.Context, ready func() bool) {
	hb := heartbeat{PID: os.Getpid(), StartedAt: time.Now().UTC(), Channels: a.names()}
	t := time.NewTicker(time.Second)

	defer t.Stop()

	for {
		if ready() && time.Since(hb.BeatAt) >= heartbeatEvery {
			hb.BeatAt = time.Now().UTC()
			if err := writeJSON(filepath.Join(a.dir, "serve.json"), hb); err != nil {
				a.log.Warn("write heartbeat", "err", err)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func readHeartbeat(dir string) (heartbeat, bool) {
	var hb heartbeat

	raw, err := os.ReadFile(filepath.Join(dir, "serve.json")) //nolint:gosec // fixed file in the bot dir
	if err != nil || json.Unmarshal(raw, &hb) != nil {
		return hb, false
	}

	return hb, time.Since(hb.BeatAt) < heartbeatStale
}

// ensureListener starts `claude-bot serve` in the background unless a
// healthy one is already running, so a bare `claude-bot inbox` just works.
func (a *app) ensureListener(ctx context.Context) error {
	if _, alive := readHeartbeat(a.dir); alive {
		return nil
	}

	logPath := filepath.Join(a.dir, "serve.log")

	if err := os.MkdirAll(a.dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", a.dir, err)
	}

	// A listener holding the lock without a heartbeat is still connecting.
	unlock, err := lockFile(filepath.Join(a.dir, "serve.lock"))
	if err != nil {
		return fmt.Errorf("listener is running but not connected yet; recent log (%s):\n%s", logPath, tailFile(logPath, 10))
	}

	unlock()

	cmd, err := a.spawnListener(logPath)
	if err != nil {
		return err
	}

	pid := cmd.Process.Pid

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(listenerStartup)
	defer deadline.Stop()

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("listener exited (%w):\n%s", err, tailFile(logPath, 10))
		case <-deadline.C:
			return fmt.Errorf("listener did not connect within %s; recent log (%s):\n%s", listenerStartup, logPath, tailFile(logPath, 10))
		case <-tick.C:
			if hb, alive := readHeartbeat(a.dir); alive && hb.PID == pid {
				fmt.Fprintf(os.Stderr, "claude-bot: started listener (pid %d), log: %s\n", pid, logPath)

				return nil
			}
		}
	}
}

// spawnListener starts `claude-bot serve` detached from this process, with
// output appended to logPath.
func (a *app) spawnListener(logPath string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate own binary: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // fixed file in the bot dir
	if err != nil {
		return nil, fmt.Errorf("open listener log: %w", err)
	}
	defer logFile.Close() //nolint:errcheck // the child holds its own descriptor

	root := filepath.Dir(a.dir)
	cmd := exec.Command(exe, "-C", root, "serve") //nolint:gosec,noctx // re-executes this binary; must outlive ctx
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start listener: %w", err)
	}

	return cmd, nil
}

// tailFile returns the last n lines of a file, for error reports.
func tailFile(path string, n int) string {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed file in the bot dir
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}

	return nil
}
