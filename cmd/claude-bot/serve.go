package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/screen"
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

// channelResult is how a channel's Listen ended.
type channelResult struct {
	name string
	err  error
}

// heartbeat is written to .claude-bot/serve.json while the listener runs.
type heartbeat struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	BeatAt    time.Time `json:"beat_at"`
	Channels  []string  `json:"channels"`
	// Stopped lists channels that failed while the others kept running;
	// Errors says why, by channel.
	Stopped []string          `json:"stopped,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
	Version string            `json:"version"`
}

func cmdServe(ctx context.Context, project string, args []string) error {
	if _, err := parseArgs("serve", args); err != nil {
		return err
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	if a.gateway != nil {
		return errServedByGateway
	}

	if a.typesafe == nil {
		return errNoJev
	}

	return a.serve(ctx, jev.New(a.typesafe.APIKey))
}

// busyError reports a project whose listener lock another process holds:
// a listener of its own, or the gateway.
type busyError struct {
	Dir string
	PID int // 0 if unknown
}

func (e *busyError) Error() string {
	return fmt.Sprintf("listener already running for %s (pid %d)", e.Dir, e.PID)
}

// serve runs the project's listener until ctx ends or every channel has
// failed: it holds the project's lock, screens incoming messages with ask
// and keeps the heartbeat. Both `claude-bot serve` and the gateway run it.
func (a *app) serve(ctx context.Context, ask screen.Asker) error {
	if err := os.MkdirAll(a.dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", a.dir, err)
	}

	unlock, err := lockFile(a.path(listenerLock))
	if err != nil {
		pid, _ := a.listenerPID()

		return &busyError{Dir: filepath.Dir(a.dir), PID: pid}
	}
	defer unlock()

	// stop finds the listener by the pid in the lock it holds.
	if err := os.WriteFile(a.path(listenerLock), []byte(strconv.Itoa(os.Getpid())), privatePerm); err != nil {
		return fmt.Errorf("write listener pid: %w", err)
	}

	a.log.Info("listening", "channels", a.names())

	if a.locked != nil {
		a.locked()
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

	go a.screener(ctx, ask, a.screen)

	return a.listen(ctx)
}

// listen runs every channel until ctx ends. A channel that fails is logged
// and dropped from the heartbeat, which lists it as stopped so that inbox
// asks for a restart; the other channels keep running. listen returns only
// once every channel has stopped.
func (a *app) listen(ctx context.Context) error {
	allReady := make(chan struct{})

	var waiting atomic.Int64

	waiting.Store(int64(len(a.channels)))

	readies := make(map[string]func(), len(a.channels))
	results := make(chan channelResult, len(a.channels))
	stopped := make(chan channelResult, len(a.channels))

	for name, ch := range a.channels {
		ready := sync.OnceFunc(func() { // Listen calls it on every reconnect
			if waiting.Add(-1) == 0 {
				close(allReady)
			}
		})
		readies[name] = ready

		go func() { results <- channelResult{name, ch.Listen(ctx, a.deliverer(ctx, ch), ready)} }()
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

				stopped <- r

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
func (a *app) beat(ctx context.Context, ready <-chan struct{}, stopped <-chan channelResult) {
	hb := heartbeat{PID: os.Getpid(), StartedAt: time.Now().UTC(), Channels: a.names(), Version: Version}

	stop := func(r channelResult) {
		hb.Channels = slices.DeleteFunc(hb.Channels, func(c string) bool { return c == r.name })
		hb.Stopped = append(hb.Stopped, r.name)

		if hb.Errors == nil {
			hb.Errors = make(map[string]string)
		}

		hb.Errors[r.name] = r.err.Error()
	}

	for ready != nil {
		select {
		case <-ctx.Done():
			return
		case r := <-stopped:
			stop(r)
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
		case r := <-stopped:
			stop(r)
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
// session's environment (proxy settings and the like). A project registered
// with the gateway has no listener of its own: the gateway serves it.
func cmdStart(ctx context.Context, project string, args []string) error {
	a, err := listenerApp(ctx, project, "start", args)
	if err != nil {
		return err
	}

	if a.gateway != nil {
		return a.servedByGateway()
	}

	if pid, err := a.listenerPID(); pid != 0 || err != nil {
		if err == nil {
			fmt.Printf("listener already running (pid %d); `claude-bot restart` replaces it\n", pid)
		}

		return err
	}

	return a.startListener(ctx)
}

// errServedByGateway is what start and serve say in a registered project
// while the gateway is down.
var errServedByGateway = errors.New("this project is registered with the gateway, which serves every registered project: run `claude-bot gateway start` in a terminal of your own")

// servedByGateway reports, for start in a registered project, whether the
// gateway serves it.
func (a *app) servedByGateway() error {
	gw, err := gatewayPID()
	if err != nil {
		return err
	}

	if gw == 0 {
		return errServedByGateway
	}

	if own, err := a.listenerPID(); err == nil && own != 0 && own != gw {
		fmt.Printf("the gateway (pid %d) serves this project once its own listener (pid %d) stops: run `claude-bot stop` here\n", gw, own)

		return nil
	}

	fmt.Printf("this project is served by the gateway (pid %d), which picks up configuration changes by itself; `claude-bot gateway status` shows how it is doing\n", gw)

	return nil
}

// cmdStop stops the project's own listener. In a registered project that is
// one left from before the gateway took over; the gateway itself is stopped
// with `claude-bot gateway stop`.
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

	if gw, err := gatewayPID(); err == nil && gw == pid {
		return fmt.Errorf("the gateway (pid %d) serves this project: `claude-bot gateway stop` stops it for every project, `claude-bot gateway remove %s` takes only this one out",
			pid, a.gateway.Name)
	}

	if err := a.stopListener(ctx, pid); err != nil {
		return err
	}

	fmt.Printf("stopped listener (pid %d)\n", pid)

	return nil
}

// cmdRestart replaces the running listener, e.g. with a newly installed
// version or after init added a channel. In a registered project it stops
// a listener left from before the gateway, which then takes over.
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

// Errors that tell Claude what to ask of the user.
var (
	errListenerDown = errors.New("listener not running: ask the user to run `claude-bot start` in a terminal of their own")
	errGatewayDown  = errors.New("gateway not running: ask the user to run `claude-bot gateway start` in a terminal of their own")
)

// checkListener fails unless a listener (the project's own, or the gateway)
// is connected, and warns when it predates this version or the configured
// channels.
func (a *app) checkListener() error {
	gw, err := gatewayPID()
	if err != nil {
		return err
	}

	hb, alive := readHeartbeat(a.dir)
	if !alive {
		return a.listenerDown(gw)
	}

	restart := "`claude-bot restart`"

	switch {
	case a.gateway != nil && hb.PID == gw:
		restart = "`claude-bot gateway restart`"
	case a.gateway != nil:
		fmt.Fprintf(os.Stderr, "claude-bot: a listener of this project's own (pid %d) still serves it, though it is registered with the gateway; ask the user to run `claude-bot stop` here so that the gateway takes over\n", hb.PID)
	}

	if len(hb.Stopped) > 0 {
		a.warnStopped(hb, restart)
	} else if !slices.Equal(hb.Channels, a.names()) && hb.PID != gw {
		// The gateway picks up configuration changes by itself.
		fmt.Fprintf(os.Stderr, "claude-bot: the listener serves %s but %s are configured; ask the user to run %s\n",
			strings.Join(hb.Channels, ", "), strings.Join(a.names(), ", "), restart)
	}

	if hb.Version != Version {
		fmt.Fprintf(os.Stderr, "claude-bot: the listener is version %q, this is %q; ask the user to run %s\n", hb.Version, Version, restart)
	}

	return nil
}

// listenerDown explains why no listener beats for the project; gw is the
// gateway's pid, 0 if it is not running.
func (a *app) listenerDown(gw int) error {
	pid, err := a.listenerPID()

	switch {
	case err != nil:
		return err
	case pid != 0 && pid == gw:
		return fmt.Errorf("the gateway (pid %d) serves this project but has not connected it yet; `claude-bot gateway status` and %s say why",
			gw, gatewayLogPath())
	case pid != 0:
		logPath := a.path(listenerLog)

		return fmt.Errorf("listener (pid %d) is running but not connected; recent log (%s):\n%s", pid, logPath, tailFile(logPath, logTailLines))
	case a.gateway == nil:
		return errListenerDown
	case gw == 0:
		return errGatewayDown
	}

	return fmt.Errorf("the gateway (pid %d) is running but does not serve this project; `claude-bot gateway status` says why", gw)
}

// warnStopped says which channels of the listener failed and where to look.
func (a *app) warnStopped(hb heartbeat, restart string) {
	fmt.Fprintf(os.Stderr, "claude-bot: channel %s stopped after an error (see \"channel stopped\" in the log) while the others keep running; ask the user to fix it and run %s\n",
		strings.Join(hb.Stopped, ", "), restart)
}

// listenerPID returns the pid of the running listener, 0 if there is none.
// The listener holds its lock while it runs and writes its pid into it.
func (a *app) listenerPID() (int, error) {
	return lockHolder(a.path(listenerLock), func() int {
		// Listeners before v0.1.15 left the lock empty; a connected one beats.
		if hb, alive := readHeartbeat(a.dir); alive {
			return hb.PID
		}

		return 0
	})
}

// startListener starts `claude-bot serve` in the background and waits until
// it has connected.
func (a *app) startListener(ctx context.Context) error {
	root := filepath.Dir(a.dir)
	logPath := a.path(listenerLog)

	if err := os.MkdirAll(a.dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", a.dir, err)
	}

	cmd, err := spawn(root, logPath, "-C", root, "serve")
	if err != nil {
		return err
	}

	pid := cmd.Process.Pid

	err = waitStarted(ctx, cmd, logPath, "listener did not connect", func() bool {
		hb, alive := readHeartbeat(a.dir)
		if alive && hb.PID == pid && len(hb.Stopped) > 0 {
			a.warnStopped(hb, "`claude-bot restart`")
		}

		return alive && hb.PID == pid
	})
	if err != nil {
		return err
	}

	fmt.Printf("started listener (pid %d), log: %s\n", pid, logPath)

	return nil
}

// stopListener shuts the listener pid down and waits until it has released
// the lock.
func (a *app) stopListener(ctx context.Context, pid int) error {
	return stopHolder(ctx, a.path(listenerLock), pid, "listener")
}
