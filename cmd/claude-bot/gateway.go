package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/screen"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The gateway is one background process serving every registered project:
// it runs each project's listener (channels, Jev screening, heartbeat) as
// `claude-bot serve` would, from the configuration in the gateway database,
// and picks up changes to it by itself. Its files live in the gateway home
// (~/.claude-bot); each project's inbox stays in the project.
const (
	gatewayLock = "gateway.lock"
	gatewayBeat = "gateway.json"
	gatewayLog  = "gateway.log"
	// reconcileEvery is how often the gateway looks for configuration
	// changes and retries projects another listener holds.
	reconcileEvery = 2 * time.Second
	// settleWait bounds how long `gateway start` waits for the projects to
	// connect before it reports.
	settleWait = 20 * time.Second
)

// Project states, as the gateway reports them.
const (
	stateStarting = "starting" // its channels are connecting
	stateRunning  = "running"  // every channel connected (some may have stopped since)
	stateBusy     = "busy"     // a listener of its own holds the project; retried until it stops
	stateFailed   = "failed"   // see the error; retried when its configuration changes
	stateIdle     = "idle"     // no channels configured
	stateStopped  = "stopped"  // the gateway is not running
)

const gatewayUsage = `Usage: claude-bot gateway <command>
  start               run the gateway in the background: one process serving every registered project
  stop                shut it down
  restart             replace it, e.g. after upgrading claude-bot
  status              the gateway and every registered project, as JSON
  import [<repo>...]  move projects' .claude-bot/config.json into the gateway (default: this repository)
  open [--print]      show the gateway's page in the browser (--print: only print its address)
  remove <project>    unregister a project, writing its configuration back to its config.json
  serve               run the gateway in the foreground`

func cmdGateway(ctx context.Context, project string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w\n%s", errUsage, gatewayUsage)
	}

	sub, rest := args[0], args[1:]

	switch sub {
	case "serve":
		return gatewayServe(ctx, rest)
	case "start":
		return gatewayStart(ctx, rest)
	case "stop":
		return gatewayStop(ctx, rest)
	case "restart":
		if err := gatewayStop(ctx, rest); err != nil {
			return err
		}

		return gatewayStart(ctx, rest)
	case "status":
		return gatewayStatusCmd(ctx, rest)
	case "import":
		return gatewayImport(ctx, project, rest)
	case "remove":
		return gatewayRemove(ctx, rest)
	case "open":
		return gatewayOpen(ctx, rest)
	case "-h", "--help", "help":
		fmt.Println(gatewayUsage)

		return nil
	}

	return fmt.Errorf("%w: unknown gateway command %q\n%s", errUsage, sub, gatewayUsage)
}

// gatewayFile returns a file in the gateway home.
func gatewayFile(name string) (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, name), nil
}

// gatewayLogPath is where the gateway logs, for messages.
func gatewayLogPath() string {
	path, err := gatewayFile(gatewayLog)
	if err != nil {
		return "~/" + config.DirName + "/" + gatewayLog
	}

	return path
}

// gatewayPID returns the pid of the running gateway, 0 if there is none.
func gatewayPID() (int, error) {
	lock, err := gatewayFile(gatewayLock)
	if err != nil {
		return 0, err
	}

	return lockHolder(lock, func() int { return 0 })
}

// gatewayHeartbeat is written to the gateway home while the gateway runs.
type gatewayHeartbeat struct {
	PID       int          `json:"pid"`
	StartedAt time.Time    `json:"started_at"`
	BeatAt    time.Time    `json:"beat_at"`
	Version   string       `json:"version"`
	UI        string       `json:"ui,omitempty"` // the page, without its token
	Projects  []runOutcome `json:"projects"`
}

// runOutcome is how the gateway is doing with one project.
type runOutcome struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

func readGatewayHeartbeat() (gatewayHeartbeat, bool) {
	var hb gatewayHeartbeat

	path, err := gatewayFile(gatewayBeat)
	if err != nil {
		return hb, false
	}

	raw, err := os.ReadFile(path) //nolint:gosec // fixed file in the gateway home
	if err != nil || json.Unmarshal(raw, &hb) != nil {
		return hb, false
	}

	return hb, time.Since(hb.BeatAt) < heartbeatStale
}

func gatewayServe(ctx context.Context, args []string) error {
	if _, err := parseArgs("gateway serve", args); err != nil {
		return err
	}

	home, err := config.Home()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", home, err)
	}

	lock := filepath.Join(home, gatewayLock)

	unlock, err := lockFile(lock)
	if err != nil {
		pid, _ := gatewayPID()

		return fmt.Errorf("gateway already running (pid %d): %w", pid, err)
	}
	defer unlock()

	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), privatePerm); err != nil {
		return fmt.Errorf("write gateway pid: %w", err)
	}

	defer func() { _ = os.Remove(filepath.Join(home, gatewayBeat)) }()

	s, err := store.Open(ctx, home)
	if err != nil {
		return err
	}
	defer s.Close() //nolint:errcheck // the gateway only reads

	g := newGateway(s, newLogger())
	g.log.Info("gateway started", "pid", os.Getpid(), "version", Version, "home", home)

	// The page is a view: without it the projects are served all the same.
	if addr, token, err := uiSettings(ctx, s); err != nil {
		g.log.Error("page not served", "err", err)
	} else if g.ui, err = g.serveWeb(ctx, addr, token); err != nil {
		g.log.Error("page not served", "err", err)
	} else {
		g.log.Info("page", "url", g.ui)
	}

	return g.loop(ctx, filepath.Join(home, gatewayBeat))
}

// gateway serves the registered projects.
type gateway struct {
	store *store.Store
	log   *slog.Logger
	// build makes a project's app and ask its Jev client; tests replace
	// them with fakes.
	build func(dir string, cfg config.Config, log *slog.Logger) (*app, error)
	ask   func(key string) screen.Asker

	started time.Time
	tick    time.Duration // how often it reconciles
	ui      string        // the page's URL, "" if it is not served

	mu   sync.Mutex
	runs map[int64]*projectRun
}

// projectRun is one project being served, with the configuration it was
// started with.
type projectRun struct {
	project     store.Project
	fingerprint string
	cancel      context.CancelFunc
	done        chan struct{}
	state, err  string // under gateway.mu
}

func newGateway(s *store.Store, log *slog.Logger) *gateway {
	return &gateway{
		store:   s,
		log:     log,
		build:   appFor,
		ask:     func(key string) screen.Asker { return jev.New(key) },
		started: time.Now().UTC(),
		tick:    reconcileEvery,
		runs:    make(map[int64]*projectRun),
	}
}

// loop serves the projects until ctx ends, reconciling with the database
// whenever it changes and writing the heartbeat to beatPath.
func (g *gateway) loop(ctx context.Context, beatPath string) error {
	defer g.stopAll()

	last := int64(-1)

	t := time.NewTicker(g.tick)
	defer t.Stop()

	for {
		rev, err := g.store.Revision(ctx)

		switch {
		case err != nil && ctx.Err() == nil:
			g.log.Error("read configuration", "err", err)
		case err == nil && (rev != last || g.busy()):
			if err := g.reconcile(ctx); err != nil {
				g.log.Error("apply configuration", "err", err)
			} else {
				last = rev
			}
		}

		hb := gatewayHeartbeat{PID: os.Getpid(), StartedAt: g.started, BeatAt: time.Now().UTC(), Version: Version, UI: g.ui, Projects: g.outcomes()}
		if err := writeJSON(beatPath, hb); err != nil {
			g.log.Warn("write heartbeat", "err", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// busy reports whether some project waits for another listener to let go.
func (g *gateway) busy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, r := range g.runs {
		if r.state == stateBusy {
			return true
		}
	}

	return false
}

// reconcile starts the projects that are new, restarts those whose
// configuration changed or that another listener held, and stops those no
// longer registered.
func (g *gateway) reconcile(ctx context.Context) error {
	projects, err := g.store.Projects(ctx)
	if err != nil {
		return err
	}

	registered := make(map[int64]bool, len(projects))

	for _, p := range projects {
		registered[p.ID] = true

		cfg, cfgErr := config.FromStore(ctx, g.store, p)
		fp := fingerprint(p, cfg, cfgErr)

		g.mu.Lock()
		r := g.runs[p.ID]
		keep := r != nil && r.fingerprint == fp && r.state != stateBusy
		g.mu.Unlock()

		if keep {
			continue
		}

		var prev *projectRun

		if r != nil {
			g.stop(r)

			if r.state == stateBusy && r.fingerprint == fp {
				prev = r
			}
		}

		g.start(ctx, p, cfg, cfgErr, fp, prev)
	}

	g.mu.Lock()
	gone := make([]*projectRun, 0)

	for id, r := range g.runs {
		if !registered[id] {
			gone = append(gone, r)

			delete(g.runs, id)
		}
	}
	g.mu.Unlock()

	for _, r := range gone {
		g.stop(r)
		g.log.Info("project unregistered", "project", r.project.Name)
	}

	return nil
}

// fingerprint identifies what a project is served with: a change restarts it.
func fingerprint(p store.Project, cfg config.Config, err error) string {
	cfg.Gateway = nil

	raw, _ := json.Marshal(struct {
		Project store.Project
		Config  config.Config
		Err     string
	}{p, cfg, fmt.Sprint(err)})
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:])
}

// start serves a project in the background; prev is how it was doing, if
// this retries it.
func (g *gateway) start(ctx context.Context, p store.Project, cfg config.Config, cfgErr error, fp string, prev *projectRun) {
	rctx, cancel := context.WithCancel(ctx)
	r := &projectRun{project: p, fingerprint: fp, cancel: cancel, done: make(chan struct{}), state: stateStarting}

	if prev != nil {
		r.state, r.err = prev.state, prev.err // retrying a busy project: no flicker, no repeated log line
	}

	g.mu.Lock()
	g.runs[p.ID] = r
	g.mu.Unlock()

	go func() {
		defer close(r.done)

		state, why := g.run(rctx, p, cfg, cfgErr, func() {
			g.mu.Lock()
			r.state, r.err = stateStarting, ""
			g.mu.Unlock()
		})
		if rctx.Err() != nil {
			return // stopped on purpose
		}

		g.mu.Lock()
		if state != r.state {
			g.log.Warn("project not served", "project", p.Name, "state", state, "why", why)
		}

		r.state, r.err = state, why
		g.mu.Unlock()
	}()
}

// stop ends a project's run and waits for it to let go of the project.
func (g *gateway) stop(r *projectRun) {
	r.cancel()
	<-r.done
}

func (g *gateway) stopAll() {
	g.mu.Lock()
	runs := make([]*projectRun, 0, len(g.runs))

	for _, r := range g.runs {
		runs = append(runs, r)
	}
	g.mu.Unlock()

	for _, r := range runs {
		g.stop(r)
	}
}

// run serves one project until ctx ends or it cannot go on, and says how it
// ended.
// locked is called once the project is the gateway's to serve.
func (g *gateway) run(ctx context.Context, p store.Project, cfg config.Config, cfgErr error, locked func()) (state, why string) {
	if cfgErr != nil {
		return stateFailed, cfgErr.Error()
	}

	if info, err := os.Stat(p.Dir); err != nil || !info.IsDir() {
		return stateFailed, fmt.Sprintf("project directory %s is gone: register it again with `claude-bot init` where it is now, or `claude-bot gateway remove %s`", p.Dir, p.Name)
	}

	if cfg.TypeSafe == nil {
		return stateFailed, "no TypeSafe API key: every message goes through Jev; run `claude-bot init --typesafe-key <key>`"
	}

	a, err := g.build(filepath.Join(p.Dir, config.DirName), cfg, g.log.With("project", p.Name))
	if errors.Is(err, errNotConfigured) {
		return stateIdle, "no channels: run `claude-bot init` in the project"
	}

	if err != nil {
		return stateFailed, err.Error()
	}

	a.locked = locked
	err = a.serve(ctx, g.ask(cfg.TypeSafe.APIKey))

	var busy *busyError

	switch {
	case ctx.Err() != nil:
		return "", ""
	case errors.As(err, &busy):
		return stateBusy, fmt.Sprintf("its own listener (pid %d) still runs: `claude-bot -C %s stop` and the gateway takes over", busy.PID, p.Dir)
	case err == nil:
		return stateFailed, "stopped"
	}

	return stateFailed, err.Error()
}

// outcomes reports every project being served, "running" once its
// listener beats.
func (g *gateway) outcomes() []runOutcome {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]runOutcome, 0, len(g.runs))

	for id, r := range g.runs {
		o := runOutcome{ID: id, State: r.state, Error: r.err}

		if o.State == stateStarting {
			if hb, alive := readHeartbeat(filepath.Join(r.project.Dir, config.DirName)); alive && hb.PID == os.Getpid() {
				o.State = stateRunning
			}
		}

		out = append(out, o)
	}

	return out
}
