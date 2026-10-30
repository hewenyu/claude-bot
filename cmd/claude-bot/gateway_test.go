//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/screen"
	"github.com/hewenyu/claude-bot/internal/store"
)

// pipeChannel is a connected fake channel; the test sends messages through
// it as the platform would. Each Listen is one connection.
type pipeChannel struct {
	fakeChannel

	mu       sync.Mutex
	deliver  func(channel.Message) error
	listens  int
	listened chan struct{} // closed on the first Listen
}

func newPipe() *pipeChannel { return &pipeChannel{listened: make(chan struct{})} }

func (p *pipeChannel) Name() string { return "lark" }

func (p *pipeChannel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	p.mu.Lock()
	p.deliver = deliver
	p.listens++

	if p.listens == 1 {
		close(p.listened)
	}
	p.mu.Unlock()

	ready()
	<-ctx.Done()

	return nil
}

func (p *pipeChannel) connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.listens
}

func (p *pipeChannel) send(t *testing.T, m channel.Message) {
	t.Helper()

	p.mu.Lock()
	deliver := p.deliver
	p.mu.Unlock()

	if err := deliver(m); err != nil {
		t.Fatal(err)
	}
}

// gatewayFixture is a gateway over a temporary database whose projects
// connect to fake channels, by repository root.
type gatewayFixture struct {
	t     *testing.T
	store *store.Store
	g     *gateway
	pipes map[string]*pipeChannel
	mu    sync.Mutex
}

func newGatewayFixture(t *testing.T) *gatewayFixture {
	t.Helper()

	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = s.Close() })

	if err := s.SetSetting(t.Context(), store.SettingTypeSafeKey, "ts"); err != nil {
		t.Fatal(err)
	}

	f := &gatewayFixture{t: t, store: s, pipes: map[string]*pipeChannel{}}
	f.g = newGateway(s, slog.New(slog.DiscardHandler))
	f.g.tick = 20 * time.Millisecond
	f.g.ask = func(string) screen.Asker { return stuffing{} }
	f.g.build = func(dir string, cfg config.Config, log *slog.Logger) (*app, error) {
		if cfg.Lark == nil {
			return nil, errNotConfigured
		}

		f.mu.Lock()
		defer f.mu.Unlock()

		pipe := newPipe()
		f.pipes[filepath.Dir(dir)] = pipe

		a := assemble(dir, log, pipe)
		a.typesafe = cfg.TypeSafe

		return a, nil
	}

	return f
}

// register adds a project with a Lark app.
func (f *gatewayFixture) register(name, appID string) store.Project {
	f.t.Helper()

	root := filepath.Join(f.t.TempDir(), name)
	if err := os.MkdirAll(root, 0o750); err != nil {
		f.t.Fatal(err)
	}

	p, err := f.store.SaveProject(f.t.Context(), root, []store.Channel{{
		Name: "lark", AppKey: "lark:" + appID, Config: json.RawMessage(`{"app_id":"` + appID + `","app_secret":"s"}`),
	}})
	if err != nil {
		f.t.Fatal(err)
	}

	return p
}

// run runs the gateway until the test ends.
func (f *gatewayFixture) run() {
	ctx, cancel := context.WithCancel(f.t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = f.g.loop(ctx, filepath.Join(f.t.TempDir(), gatewayBeat))
	}()

	f.t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (f *gatewayFixture) pipe(p store.Project) *pipeChannel {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.pipes[p.Dir]
}

// waitState waits until the gateway reports the project in state.
func (f *gatewayFixture) waitState(p store.Project, state string) runOutcome {
	f.t.Helper()

	var last runOutcome

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range f.g.outcomes() {
			if o.ID == p.ID {
				last = o
			}
		}

		if last.State == state {
			return last
		}

		time.Sleep(10 * time.Millisecond)
	}

	f.t.Fatalf("project %s is %q (%s), want %q", p.Name, last.State, last.Error, state)

	return last
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	// Screening waits for a burst of messages to settle (screenQuiet).
	deadline := time.Now().Add(screenMax + 5*time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func botDir(p store.Project) string { return filepath.Join(p.Dir, config.DirName) }

func TestGateway_eachProjectGetsItsMessages(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)
	api := f.register("api", "cli_api")
	web := f.register("web", "cli_web")
	f.run()

	f.waitState(api, stateRunning)
	f.waitState(web, stateRunning)

	// Each project's listener beats in the project, as `claude-bot serve` would.
	for _, p := range []store.Project{api, web} {
		if hb, alive := readHeartbeat(botDir(p)); !alive || hb.PID != os.Getpid() || !slices.Equal(hb.Channels, []string{"lark"}) {
			t.Errorf("heartbeat of %s = %+v, %v", p.Name, hb, alive)
		}
	}

	f.pipe(api).send(t, channel.Message{
		ID: "lark:om_1", Channel: "lark", ChatID: "oc_1", ChatType: channel.ChatDirect,
		Sender: channel.User{ID: "ou_1"}, Text: "部署失败了", CreatedAt: time.Now(),
	})

	apiApp := assemble(botDir(api), slog.New(slog.DiscardHandler))
	webApp := assemble(botDir(web), slog.New(slog.DiscardHandler))

	waitFor(t, "the message screened into a task of api", func() bool {
		tasks, _ := apiApp.inbox.PendingTasks()

		return len(tasks) == 1 && strings.Contains(tasks[0].Entries[0].Text, "部署失败")
	})

	if tasks, _ := webApp.inbox.PendingTasks(); len(tasks) != 0 {
		t.Errorf("web got %d tasks of api's", len(tasks))
	}

	if f.pipe(api).ackCount() != 1 {
		t.Errorf("acks = %d, want 1", f.pipe(api).ackCount())
	}
}

func TestGateway_followsTheDatabase(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)
	api := f.register("api", "cli_api")
	f.run()

	f.waitState(api, stateRunning)
	first := f.pipe(api)

	// A changed configuration reconnects the project with it.
	if err := f.store.PutChannel(t.Context(), api.ID, store.Channel{
		Name: "lark", AppKey: "lark:cli_api",
		Config: json.RawMessage(`{"app_id":"cli_api","app_secret":"rotated"}`),
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "a new connection", func() bool { return f.pipe(api) != first })
	f.waitState(api, stateRunning)

	// An unrelated write does not.
	second := f.pipe(api)
	if err := f.store.SetSetting(t.Context(), store.SettingGitHubToken, "ghp"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * f.g.tick)

	if f.pipe(api) != second || second.connections() != 1 {
		t.Error("a change that does not concern the project reconnected it")
	}

	// A new project is picked up; an unregistered one is let go.
	web := f.register("web", "cli_web")
	f.waitState(web, stateRunning)

	if err := f.store.RemoveProject(t.Context(), api.ID); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "api to be let go", func() bool {
		_, alive := readHeartbeat(botDir(api))

		unlock, err := lockFile(filepath.Join(botDir(api), listenerLock))
		if err == nil {
			unlock()
		}

		return !alive && err == nil
	})
}

// A project whose own listener still runs (from before the gateway) is not
// connected a second time: two connections would split its messages. The
// gateway takes over once that listener stops.
func TestGateway_waitsForTheProjectsOwnListener(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)
	api := f.register("api", "cli_api")

	if err := os.MkdirAll(botDir(api), 0o750); err != nil {
		t.Fatal(err)
	}

	unlock, err := lockFile(filepath.Join(botDir(api), listenerLock))
	if err != nil {
		t.Fatal(err)
	}

	f.run()

	o := f.waitState(api, stateBusy)
	if !strings.Contains(o.Error, "stop") {
		t.Errorf("busy error %q does not say how to hand over", o.Error)
	}

	time.Sleep(10 * f.g.tick)

	if p := f.pipe(api); p != nil && p.connections() > 0 {
		t.Fatal("the gateway connected a project whose own listener runs")
	}

	unlock()

	f.waitState(api, stateRunning)
}

func TestGateway_states(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)

	idle, err := f.store.AddProject(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gone := f.register("gone", "cli_gone")
	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}

	f.run()

	f.waitState(idle, stateIdle)

	if o := f.waitState(gone, stateFailed); !strings.Contains(o.Error, "gateway remove") {
		t.Errorf("gone project error = %q", o.Error)
	}

	// Without a TypeSafe key nothing connects: messages would never pass Jev.
	if err := f.store.SetSetting(t.Context(), store.SettingTypeSafeKey, ""); err != nil {
		t.Fatal(err)
	}

	api := f.register("api", "cli_api")
	if o := f.waitState(api, stateFailed); !strings.Contains(o.Error, "TypeSafe") {
		t.Errorf("no-key error = %q", o.Error)
	}

	if err := f.store.SetSetting(t.Context(), store.SettingTypeSafeKey, "ts"); err != nil {
		t.Fatal(err)
	}

	f.waitState(api, stateRunning)
}

func TestGateway_stopLetsGoOfEveryProject(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)
	api := f.register("api", "cli_api")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	beat := filepath.Join(t.TempDir(), gatewayBeat)

	go func() { done <- f.g.loop(ctx, beat) }()

	f.waitState(api, stateRunning)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("loop() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not stop")
	}

	if _, alive := readHeartbeat(botDir(api)); alive {
		t.Error("project heartbeat left behind")
	}
}

func TestCheckListener_registered(t *testing.T) {
	t.Parallel()

	a := assemble(t.TempDir(), slog.New(slog.DiscardHandler), &fakeChannel{})
	a.gateway = &store.Project{ID: 1, Name: "api"}

	// The test home has no gateway running.
	if err := a.checkListener(); !errors.Is(err, errGatewayDown) {
		t.Errorf("checkListener() = %v, want errGatewayDown", err)
	}
}

func TestUnregister_writesConfigBack(t *testing.T) {
	t.Parallel()

	f := newGatewayFixture(t)
	api := f.register("api", "cli_api")

	path, err := unregister(t.Context(), f.store, api)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.ReadFile(botDir(api))
	if err != nil || cfg.Lark == nil || cfg.Lark.AppID != "cli_api" || cfg.TypeSafe == nil || cfg.TypeSafe.APIKey != "ts" {
		t.Errorf("config.json after unregister = %+v, %v (%s)", cfg, err, path)
	}

	if _, err := f.store.Project(t.Context(), api.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("project still registered: %v", err)
	}
}

// registerTemp registers a new repository with the gateway in a temporary
// home, which the test then uses.
func registerTemp(t *testing.T) (home, root string) {
	t.Helper()

	home = t.TempDir()
	t.Setenv(config.HomeEnv, home)

	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.DirName), 0o750); err != nil {
		t.Fatal(err)
	}

	_, err := config.Save(t.Context(), filepath.Join(root, config.DirName), config.Config{
		GitHub:   &github.Config{Token: "t", Repo: "o/r"},
		TypeSafe: &jev.Config{APIKey: "ts"},
	})
	if err != nil {
		t.Fatal(err)
	}

	return home, root
}

func TestStop_leavesTheGatewayAlone(t *testing.T) { //nolint:paralleltest // sets the environment
	home, root := registerTemp(t)

	// The gateway holds its own lock and the project's.
	locks := filepath.Join(home, gatewayLock) + string(os.PathListSeparator) + filepath.Join(root, config.DirName, listenerLock)
	gw, exited := startFakeListener(t, locks)

	err := cmdStop(t.Context(), root, nil)
	if err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Errorf("stop in a project the gateway serves = %v, want a refusal naming the gateway", err)
	}

	if err := cmdStart(t.Context(), root, nil); err != nil {
		t.Errorf("start in a project the gateway serves = %v, want nil", err)
	}

	select {
	case err := <-exited:
		t.Fatalf("stop in a project killed the gateway: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if pid, _ := gatewayPID(); pid != gw {
		t.Errorf("gatewayPID() = %d, want %d", pid, gw)
	}
}

func TestStop_handsOverFromTheProjectsOwnListener(t *testing.T) { //nolint:paralleltest // sets the environment
	_, root := registerTemp(t)

	_, exited := startFakeListener(t, filepath.Join(root, config.DirName, listenerLock))

	if err := cmdStop(t.Context(), root, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("the project's own listener: %v, want a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the project's own listener still runs")
	}

	// With no gateway running, start says how to run it.
	if err := cmdStart(t.Context(), root, nil); !errors.Is(err, errServedByGateway) {
		t.Errorf("start = %v, want errServedByGateway", err)
	}
}
