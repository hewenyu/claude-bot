package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
	"github.com/hewenyu/claude-bot/internal/webui"
)

// The gateway serves a local web page: which project handled what, and the
// configuration of each project's channels. It listens on loopback only,
// accepts only loopback Host headers (no DNS rebinding) and wants the token
// from the database on every API call (no cross-site requests). Secrets go
// in, never out: the page sees only whether one is set and its last digits.
//
// The handlers are split by what they serve: tasks (gateway_web_tasks.go),
// channels (gateway_web_channels.go), the shared settings and GitHub tokens
// (gateway_web_settings.go, gateway_web_github.go), the runner and its
// agents (gateway_web_runner.go); the HTTP plumbing is in gateway_web_http.go.
const (
	defaultUIAddr  = "127.0.0.1:7600"
	uiTokenBytes   = 24
	maxRequestBody = 64 << 10
	webTimeout     = 15 * time.Second
	webShutdown    = 5 * time.Second
)

// web is the gateway's page and its API.
type web struct {
	store *store.Store
	bots  *bots       // nil if the gateway runs no agents
	home  gatewayHome // where the gateway's heartbeat is
	token string
	hosts []string // accepted Host headers
	log   *slog.Logger
	// checkGitHub checks a GitHub token against a repository, checkJev a
	// TypeSafe key; tests replace them.
	checkGitHub func(ctx context.Context, token string, gh github.Config) error
	checkJev    func(ctx context.Context, key string) error
}

func newWeb(s *store.Store, b *bots, home gatewayHome, token string, hosts []string, log *slog.Logger) *web {
	return &web{
		store: s, bots: b, home: home, token: token, hosts: hosts, log: log,
		checkGitHub: checkGitHubAccess,
		checkJev:    func(ctx context.Context, key string) error { return jev.New(key).Check(ctx) },
	}
}

// uiSettings returns the page's address and token, creating the token.
func uiSettings(ctx context.Context, s *store.Store) (addr, token string, err error) {
	if addr, err = s.Setting(ctx, store.SettingUIAddr); err != nil {
		return "", "", err
	}

	if addr == "" {
		addr = defaultUIAddr
	}

	if token, err = s.Setting(ctx, store.SettingUIToken); err != nil || token != "" {
		return addr, token, err
	}

	token = newHex(uiTokenBytes)

	return addr, token, s.SetSetting(ctx, store.SettingUIToken, token)
}

// listenPage listens on addr, which must be a loopback address, and
// returns the Host headers that reach where it listens: with a port of 0,
// the one it got.
func listenPage(ctx context.Context, addr string) (net.Listener, []string, error) {
	if _, err := loopbackHosts(addr); err != nil {
		return nil, nil, err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("page: %w", err)
	}

	hosts, err := loopbackHosts(ln.Addr().String())
	if err != nil {
		_ = ln.Close()

		return nil, nil, err
	}

	return ln, hosts, nil
}

// pageURL is the page's address on ln.
func pageURL(ln net.Listener) string {
	return "http://" + ln.Addr().String() + "/"
}

// servePage serves w on ln until ctx ends, and returns a wait that returns
// once the server has shut down.
func (g *gateway) servePage(ctx context.Context, ln net.Listener, w *web) (wait func()) {
	srv := &http.Server{
		Handler:           w.routes(),
		ReadHeaderTimeout: webTimeout,
		// Requests end with the gateway rather than run on into a closed
		// database.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), webShutdown)
		defer cancel()

		_ = srv.Shutdown(sctx) //nolint:contextcheck // shutting down after ctx ended
	}()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.log.Error("page stopped", "err", err)
		}
	}()

	return func() { <-stopped }
}

func (w *web) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/overview", w.api(w.overview))
	api.HandleFunc("GET /api/projects/{id}/channel-types", w.api(w.channelTypes))
	api.HandleFunc("GET /api/projects/{id}/tasks", w.api(w.tasks))
	api.HandleFunc("GET /api/projects/{id}/tasks/{task}", w.api(w.task))
	api.HandleFunc("GET /api/projects/{id}/channels", w.api(w.channels))
	api.HandleFunc("PUT /api/projects/{id}/channels/{name}", w.api(w.putChannel))
	api.HandleFunc("POST /api/projects/{id}/channels/{name}/test", w.api(w.testChannel))
	api.HandleFunc("DELETE /api/projects/{id}/channels/{name}", w.api(w.deleteChannel))
	api.HandleFunc("DELETE /api/projects/{id}", w.api(w.deleteProject))
	api.HandleFunc("GET /api/settings", w.api(w.settings))
	api.HandleFunc("PUT /api/settings", w.api(w.putSettings))
	api.HandleFunc("GET /api/projects/{id}/runner", w.api(w.withBots(w.projectRun)))
	api.HandleFunc("PUT /api/projects/{id}/runner", w.api(w.withBots(w.putProjectRunner)))
	api.HandleFunc("POST /api/projects/{id}/runner/resume", w.api(w.withBots(w.resumeProjectRunner)))
	api.HandleFunc("PUT /api/runner", w.api(w.withBots(w.putRunner)))
	api.HandleFunc("POST /api/agents/{agent}/{action}", w.api(w.withBots(w.agentAction)))
	api.HandleFunc("GET /api/agents/{agent}/term", w.term)
	api.HandleFunc("PUT /api/github-tokens/{name}", w.api(w.putGitHubToken))
	api.HandleFunc("DELETE /api/github-tokens/{name}", w.api(w.deleteGitHubToken))
	api.HandleFunc("/api/", func(rw http.ResponseWriter, _ *http.Request) { apiError(rw, http.StatusNotFound, "no such API") })

	root := http.NewServeMux()
	root.Handle("/api/", w.authorize(api))
	root.Handle("POST "+hookPath, w.authorizeAgent(w.api(w.withBots(w.agentHook))))
	root.Handle("POST "+finishPath, w.authorizeAgent(w.api(w.withBots(w.agentFinish))))
	root.Handle("/", webui.Handler())

	return w.guard(root)
}

func (w *web) project(r *http.Request) (store.Project, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return store.Project{}, badRequestf("project id %q", r.PathValue("id"))
	}

	return w.store.Project(r.Context(), id)
}

// overviewResponse is the gateway and its projects.
type overviewResponse struct {
	gatewayStatus

	Self   string          `json:"self"`             // this binary's version
	Runner *runnerOverview `json:"runner,omitempty"` // nil if the gateway runs no agents
}

func (w *web) overview(r *http.Request) (any, error) {
	hb, alive := w.home.heartbeat()

	st, err := gatherStatus(r.Context(), string(w.home), w.store, hb, alive)
	if err != nil {
		return nil, err
	}

	return overviewResponse{gatewayStatus: st, Self: Version, Runner: w.runnerOverview()}, nil
}
