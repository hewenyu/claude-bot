package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/jev"
)

const (
	heartbeatEvery   = 15 * time.Second
	heartbeatStale   = 45 * time.Second // three missed beats
	listenerStartup  = 30 * time.Second
	listenerShutdown = 30 * time.Second
	startupPoll      = 200 * time.Millisecond
	ackTimeout       = 10 * time.Second
	logTailLines     = 10 // log lines quoted when the listener fails
)

// heartbeat is written to .claude-bot/serve.json while the listener runs.
type heartbeat struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	BeatAt    time.Time `json:"beat_at"`
	Channels  []string  `json:"channels"`
	// Stopped lists channels that failed while the others kept running.
	Stopped []string `json:"stopped,omitempty"`
	Version string   `json:"version"`
}

func cmdServe(ctx context.Context, project string, args []string) error {
	if _, err := parseArgs("serve", args); err != nil {
		return err
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	if a.typesafe == nil {
		return errNoJev
	}

	unlock, err := lockFile(a.path(listenerLock))
	if err != nil {
		return fmt.Errorf("listener already running for %s: %w", filepath.Dir(a.dir), err)
	}
	defer unlock()

	// stop finds the listener by the pid in the lock it holds.
	if err := os.WriteFile(a.path(listenerLock), []byte(strconv.Itoa(os.Getpid())), privatePerm); err != nil {
		return fmt.Errorf("write listener pid: %w", err)
	}

	// The heartbeat means "connected": it starts once every channel has
	// connected and disappears when the listener exits. Deferred before
	// cancel, so it runs after beat has been told to stop.
	defer func() { _ = os.Remove(a.path(heartbeatFile)) }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Messages left pending by a version before tasks are screened into
	// tasks like new ones.
	if n, err := a.inbox.Migrate(); err != nil {
		return err
	} else if n > 0 {
		a.log.Info("screening messages from before tasks", "count", n)
	}

	// Every message waits in incoming/ until Jev has screened it; leftovers
	// from a previous run are screened first.
	a.screen = make(chan struct{}, 1)
	a.screen <- struct{}{}

	go a.screener(ctx, jev.New(a.typesafe.APIKey), a.screen)

	return a.listen(ctx)
}

// listen runs every channel until ctx ends. A channel that fails is logged
// and dropped from the heartbeat, which lists it as stopped so that inbox
// asks for a restart; the other channels keep running. listen returns only
// once every channel has stopped.
func (a *app) listen(ctx context.Context) error {
	type result struct {
		name string
		err  error
	}

	allReady := make(chan struct{})

	var waiting atomic.Int64

	waiting.Store(int64(len(a.channels)))

	readies := make(map[string]func(), len(a.channels))
	results := make(chan result, len(a.channels))
	stopped := make(chan string, len(a.channels))

	for name, ch := range a.channels {
		ready := sync.OnceFunc(func() { // Listen calls it on every reconnect
			if waiting.Add(-1) == 0 {
				close(allReady)
			}
		})
		readies[name] = ready

		go func() { results <- result{name, ch.Listen(ctx, a.deliverer(ctx, ch), ready)} }()
	}

	go a.beat(ctx, allReady, stopped)

	var errs []error

	for len(errs) < len(a.channels) {
		select {
		case <-ctx.Done():
			return nil
		case r := <-results:
			if ctx.Err() != nil {
				return nil //nolint:nilerr // shutting down: the channel error is just the cancellation
			}

			if r.err == nil {
				r.err = errors.New("stopped")
			}

			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))

			if len(errs) < len(a.channels) {
				a.log.Error("channel stopped; the others keep running", "channel", r.name, "err", r.err)

				stopped <- r.name

				readies[r.name]() // a channel that never connected no longer holds up the heartbeat
			}
		}
	}

	return errors.Join(errs...)
}

func (a *app) deliverer(ctx context.Context, ch channel.Channel) func(channel.Message) error {
	return func(m channel.Message) error {
		fresh, err := a.inbox.Hold(m)
		if err != nil {
			a.log.Error("store message", "id", m.ID, "err", err)

			return err
		}

		if !fresh {
			return nil
		}

		a.log.Info("message", "id", m.ID, "chat", m.ChatType, "from", m.Sender.ID)

		select {
		case a.screen <- struct{}{}:
		default: // a wake-up is already due
		}

		go func() {
			ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
			defer cancel()

			if err := ch.Ack(ackCtx, m); err != nil {
				a.log.Warn("ack failed", "id", m.ID, "err", err)
			}
		}()

		return nil
	}
}

// beat writes the heartbeat every heartbeatEvery once ready is closed, and
// at once when a channel arrives on stopped.
func (a *app) beat(ctx context.Context, ready <-chan struct{}, stopped <-chan string) {
	hb := heartbeat{PID: os.Getpid(), StartedAt: time.Now().UTC(), Channels: a.names(), Version: Version}

	stop := func(name string) {
		hb.Channels = slices.DeleteFunc(hb.Channels, func(c string) bool { return c == name })
		hb.Stopped = append(hb.Stopped, name)
	}

	for ready != nil {
		select {
		case <-ctx.Done():
			return
		case name := <-stopped:
			stop(name)
		case <-ready:
			ready = nil
		}
	}

	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()

	for {
		// A channel that stopped before ready was closed is already queued.
		for len(stopped) > 0 {
			stop(<-stopped)
		}

		hb.BeatAt = time.Now().UTC()
		if err := writeJSON(a.path(heartbeatFile), hb); err != nil {
			a.log.Warn("write heartbeat", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case name := <-stopped:
			stop(name)
		case <-t.C:
		}
	}
}

func readHeartbeat(dir string) (heartbeat, bool) {
	var hb heartbeat

	raw, err := os.ReadFile(filepath.Join(dir, heartbeatFile)) //nolint:gosec // fixed file in the bot dir
	if err != nil || json.Unmarshal(raw, &hb) != nil {
		return hb, false
	}

	return hb, time.Since(hb.BeatAt) < heartbeatStale
}

// The listener runs in the background and is started and stopped by the
// user, from their own terminal: started by Claude Code it would inherit the
// session's environment (proxy settings and the like).
func cmdStart(ctx context.Context, project string, args []string) error {
	a, err := listenerApp(ctx, project, "start", args)
	if err != nil {
		return err
	}

	if pid, err := a.listenerPID(); pid != 0 || err != nil {
		if err == nil {
			fmt.Printf("listener already running (pid %d); `claude-bot restart` replaces it\n", pid)
		}

		return err
	}

	return a.startListener(ctx)
}

func cmdStop(ctx context.Context, project string, args []string) error {
	a, err := listenerApp(ctx, project, "stop", args)
	if err != nil {
		return err
	}

	pid, err := a.listenerPID()
	if pid == 0 || err != nil {
		if err == nil {
			fmt.Println("listener not running")
		}

		return err
	}

	if err := a.stopListener(ctx, pid); err != nil {
		return err
	}

	fmt.Printf("stopped listener (pid %d)\n", pid)

	return nil
}

// cmdRestart replaces the running listener, e.g. with a newly installed
// version or after init added a channel.
func cmdRestart(ctx context.Context, project string, args []string) error {
	if err := cmdStop(ctx, project, args); err != nil {
		return err
	}

	return cmdStart(ctx, project, args)
}

func listenerApp(ctx context.Context, project, name string, args []string) (*app, error) {
	if _, err := parseArgs(name, args); err != nil {
		return nil, err
	}

	return newApp(ctx, project)
}

// errListenerDown tells Claude what to ask of the user.
var errListenerDown = errors.New("listener not running: ask the user to run `claude-bot start` in a terminal of their own")

// checkListener fails unless a listener is connected, and warns when it
// predates this version or the configured channels.
func (a *app) checkListener() error {
	hb, alive := readHeartbeat(a.dir)
	if !alive {
		pid, err := a.listenerPID()
		switch {
		case err != nil:
			return err
		case pid != 0:
			logPath := a.path(listenerLog)

			return fmt.Errorf("listener (pid %d) is running but not connected; recent log (%s):\n%s", pid, logPath, tailFile(logPath, logTailLines))
		}

		return errListenerDown
	}

	if len(hb.Stopped) > 0 {
		a.warnStopped(hb)
	} else if !slices.Equal(hb.Channels, a.names()) {
		fmt.Fprintf(os.Stderr, "claude-bot: the listener serves %s but %s are configured; ask the user to run `claude-bot restart`\n",
			strings.Join(hb.Channels, ", "), strings.Join(a.names(), ", "))
	}

	if hb.Version != Version {
		fmt.Fprintf(os.Stderr, "claude-bot: the listener is version %q, this is %q; ask the user to run `claude-bot restart`\n", hb.Version, Version)
	}

	return nil
}

// warnStopped says which channels of the listener failed and where to look.
func (a *app) warnStopped(hb heartbeat) {
	fmt.Fprintf(os.Stderr, "claude-bot: channel %s stopped after an error (see \"channel stopped\" in %s) while the others keep running; ask the user to fix it and run `claude-bot restart`\n",
		strings.Join(hb.Stopped, ", "), a.path(listenerLog))
}

// listenerPID returns the pid of the running listener, 0 if there is none.
// The listener holds its lock while it runs and writes its pid into it.
func (a *app) listenerPID() (int, error) {
	lock := a.path(listenerLock)
	if _, err := os.Stat(lock); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}

	unlock, err := lockFile(lock)
	if err == nil {
		unlock()

		return 0, nil
	}

	raw, _ := os.ReadFile(lock) //nolint:gosec // fixed file in the bot dir
	if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
		return pid, nil
	}

	// Listeners before v0.1.15 left the lock empty; a connected one beats.
	if hb, alive := readHeartbeat(a.dir); alive {
		return hb.PID, nil
	}

	return 0, fmt.Errorf("a listener holds %s but its pid is unknown", lock)
}

// startListener starts `claude-bot serve` in the background and waits until
// it has connected.
func (a *app) startListener(ctx context.Context) error {
	logPath := a.path(listenerLog)

	cmd, err := a.spawnListener(logPath)
	if err != nil {
		return err
	}

	pid := cmd.Process.Pid

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(listenerStartup)
	defer deadline.Stop()

	tick := time.NewTicker(startupPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("listener exited (%w):\n%s", err, tailFile(logPath, logTailLines))
		case <-deadline.C:
			return fmt.Errorf("listener did not connect within %s; recent log (%s):\n%s", listenerStartup, logPath, tailFile(logPath, logTailLines))
		case <-tick.C:
			if hb, alive := readHeartbeat(a.dir); alive && hb.PID == pid {
				fmt.Printf("started listener (pid %d), log: %s\n", pid, logPath)

				if len(hb.Stopped) > 0 {
					a.warnStopped(hb)
				}

				return nil
			}
		}
	}
}

// stopListener shuts the listener pid down and waits until it has released
// the lock.
func (a *app) stopListener(ctx context.Context, pid int) error {
	if err := terminate(pid); err != nil {
		return err
	}

	deadline := time.NewTimer(listenerShutdown)
	defer deadline.Stop()

	tick := time.NewTicker(startupPoll)
	defer tick.Stop()

	for {
		if unlock, err := lockFile(a.path(listenerLock)); err == nil {
			unlock()

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("listener (pid %d) did not stop within %s", pid, listenerShutdown)
		case <-tick.C:
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

	if err := os.MkdirAll(a.dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create %s: %w", a.dir, err)
	}

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, privatePerm) //nolint:gosec // fixed file in the bot dir
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
