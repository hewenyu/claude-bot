package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/screen"
)

const (
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

func cmdServe(ctx context.Context, project string, args []string) error {
	a, _, err := openApp(ctx, project, nil, args, exactly(0), "serve")
	if err != nil {
		return err
	}

	if a.gateway != nil {
		return errServedByGateway
	}

	if a.typesafe == nil {
		return errNoJev
	}

	return a.serve(ctx, jev.New(a.typesafe.APIKey), nil)
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
// and keeps the heartbeat. Both `claude-bot serve` and the gateway run it;
// locked, if not nil, is called once it holds the lock.
func (a *app) serve(ctx context.Context, ask screen.Asker, locked func()) error {
	if err := os.MkdirAll(a.dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", a.dir, err)
	}

	// stop finds the listener by the pid in the lock it holds.
	unlock, err := holdLock(a.path(listenerLock))
	if errors.Is(err, errLocked) {
		pid, _ := a.listenerPID()

		return &busyError{Dir: filepath.Dir(a.dir), PID: pid}
	}

	if err != nil {
		return err
	}

	defer unlock()

	a.log.Info("listening", "channels", a.names())

	if locked != nil {
		locked()
	}

	// The heartbeat means "connected": it starts once every channel has
	// connected and disappears when the listener exits. The deferred calls
	// run in reverse: cancel, wait until every goroutine below has returned
	// (no channel still connected, no beat still writing), and only then
	// remove the heartbeat and let go of the lock for the next listener.
	defer func() { _ = os.Remove(a.path(heartbeatFile)) }()

	var wg sync.WaitGroup
	defer wg.Wait()

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
	wake := make(chan struct{}, 1)
	wake <- struct{}{}

	wg.Go(func() { a.screener(ctx, ask, wake) })

	if a.closable() {
		wg.Go(func() { a.watchClosed(ctx) })
	}

	return a.listen(ctx, &wg, wake)
}

// listen runs every channel until ctx ends. A channel that fails is logged
// and dropped from the heartbeat, which lists it as stopped so that inbox
// asks for a restart; the other channels keep running. listen returns only
// once every channel has stopped, or ctx has ended; the goroutines it starts
// are counted in wg. Each message delivered is a wake-up for the screener.
func (a *app) listen(ctx context.Context, wg *sync.WaitGroup, wake chan<- struct{}) error {
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

		wg.Go(func() { results <- channelResult{name, ch.Listen(ctx, a.deliverer(ctx, ch, wake), ready)} })
	}

	wg.Go(func() { a.beat(ctx, allReady, stopped) })

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

// deliverer holds a message ch delivers in the inbox, acknowledges it on the
// platform and wakes the screener with wake; nil wakes nobody.
func (a *app) deliverer(ctx context.Context, ch channel.Channel, wake chan<- struct{}) func(channel.Message) error {
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
		case wake <- struct{}{}:
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
	hb := heartbeat{PID: os.Getpid(), StartedAt: time.Now().UTC(), Version: Version, Channels: a.names()}

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
		if err := writeBeat(a.path(heartbeatFile), hb); err != nil {
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
			fmt.Printf("listener already running (pid %d); %s replaces it\n", pid, hintRestart)
		}

		return err
	}

	return a.startListener(ctx)
}

// errServedByGateway is what start and serve say in a registered project
// while the gateway is down.
var errServedByGateway = errors.New("this project is registered with the gateway, which serves every registered project: run " + hintGatewayStart + " in a terminal of your own")

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
		fmt.Printf("the gateway (pid %d) serves this project once its own listener (pid %d) stops: run %s here\n", gw, own, hintStop)

		return nil
	}

	fmt.Printf("this project is served by the gateway (pid %d), which picks up configuration changes by itself; %s shows how it is doing\n", gw, hintGatewayStatus)

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
		// Right after `gateway remove` the project is no longer registered,
		// though the gateway still lets go of it.
		name := filepath.Dir(a.dir)
		if a.gateway != nil {
			name = a.gateway.Name
		}

		return fmt.Errorf("the gateway (pid %d) serves this project: %s stops it for every project, `claude-bot gateway remove %s` takes only this one out",
			pid, hintGatewayStop, name)
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
	a, _, err := openApp(ctx, project, nil, args, exactly(0), name)

	return a, err
}

// Errors that tell Claude what to ask of the user.
var (
	errListenerDown = errors.New("listener not running: ask the user to run " + hintStart + " in a terminal of their own")
	errGatewayDown  = errors.New("gateway not running: ask the user to run " + hintGatewayStart + " in a terminal of their own")
)

// checkListener fails unless a listener (the project's own, or the gateway)
// is connected, and returns warnings for Claude when it predates this
// version or the configured channels.
func (a *app) checkListener() ([]string, error) {
	gw, err := gatewayPID()
	if err != nil {
		return nil, err
	}

	hb, alive := readHeartbeat(a.dir)
	if !alive {
		return nil, a.listenerDown(gw)
	}

	var warnings []string

	restart := hintRestart

	switch {
	case a.gateway != nil && hb.PID == gw:
		restart = hintGatewayRestart
	case a.gateway != nil:
		warnings = append(warnings, fmt.Sprintf("a listener of this project's own (pid %d) still serves it, though it is registered with the gateway; ask the user to run %s here so that the gateway takes over", hb.PID, hintStop))
	}

	if len(hb.Stopped) > 0 {
		warnings = append(warnings, stoppedWarning(hb, restart))
	} else if !slices.Equal(hb.Channels, a.names()) && hb.PID != gw {
		// The gateway picks up configuration changes by itself.
		warnings = append(warnings, fmt.Sprintf("the listener serves %s but %s are configured; ask the user to run %s",
			strings.Join(hb.Channels, ", "), strings.Join(a.names(), ", "), restart))
	}

	if hb.Version != Version {
		warnings = append(warnings, fmt.Sprintf("the listener is version %q, this is %q; ask the user to run %s", hb.Version, Version, restart))
	}

	return warnings, nil
}

// warn prints warnings for Claude.
func warn(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "claude-bot: "+w)
	}
}

// listenerDown explains why no listener beats for the project; gw is the
// gateway's pid, 0 if it is not running.
func (a *app) listenerDown(gw int) error {
	pid, err := a.listenerPID()

	switch {
	case err != nil:
		return err
	case pid != 0 && pid == gw:
		return fmt.Errorf("the gateway (pid %d) serves this project but has not connected it yet; %s and %s say why",
			gw, hintGatewayStatus, gatewayLogPath())
	case pid != 0:
		logPath := a.path(listenerLog)

		return fmt.Errorf("listener (pid %d) is running but not connected; recent log (%s):\n%s", pid, logPath, tailFile(logPath, logTailLines))
	case a.gateway == nil:
		return errListenerDown
	case gw == 0:
		return errGatewayDown
	}

	return fmt.Errorf("the gateway (pid %d) is running but does not serve this project; %s says why", gw, hintGatewayStatus)
}

// stoppedWarning says which channels of the listener failed and where to
// look.
func stoppedWarning(hb heartbeat, restart string) string {
	return fmt.Sprintf("channel %s stopped after an error (see \"channel stopped\" in the log) while the others keep running; ask the user to fix it and run %s",
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

	var hb heartbeat

	err = waitStarted(ctx, cmd, logPath, "listener", func() bool {
		var alive bool

		hb, alive = readHeartbeat(a.dir)

		return alive && hb.PID == pid
	})
	if err != nil {
		return err
	}

	if len(hb.Stopped) > 0 {
		warn([]string{stoppedWarning(hb, hintRestart)})
	}

	fmt.Printf("started listener (pid %d), log: %s\n", pid, logPath)

	return nil
}

// stopListener shuts the listener pid down and waits until it has released
// the lock.
func (a *app) stopListener(ctx context.Context, pid int) error {
	return stopHolder(ctx, a.path(listenerLock), pid, "listener")
}
