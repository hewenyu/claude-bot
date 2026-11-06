package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
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
// agents (gateway_web_runner.go).
const (
	defaultUIAddr  = "127.0.0.1:7600"
	uiTokenBytes   = 24
	maxRequestBody = 64 << 10
	webTimeout     = 15 * time.Second
	webShutdown    = 5 * time.Second

	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// web is the gateway's page and its API.
type web struct {
	store *store.Store
	bots  *bots // nil if the gateway runs no agents
	token string
	hosts []string // accepted Host headers
	log   *slog.Logger
	// checkGitHub checks a GitHub token against a repository, checkJev a
	// TypeSafe key; tests replace them.
	checkGitHub func(ctx context.Context, token string, gh github.Config) error
	checkJev    func(ctx context.Context, key string) error
}

func newWeb(s *store.Store, b *bots, token string, hosts []string, log *slog.Logger) *web {
	return &web{
		store: s, bots: b, token: token, hosts: hosts, log: log,
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

// loopbackHosts returns the Host headers that reach addr, which must be a
// loopback address: the page holds credentials and is for this machine.
func loopbackHosts(addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("page address %q: %w", addr, err)
	}

	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("page address %q is not a loopback address; the page is for this machine only", addr)
	}

	hosts := []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port}
	if own := net.JoinHostPort(host, port); !slices.Contains(hosts, own) {
		hosts = append(hosts, own) // e.g. 127.0.0.2
	}

	return hosts, nil
}

// serveWeb serves the page on addr until ctx ends. It returns once
// listening, with the page's URL and a wait that returns once the server
// has shut down, or with why it could not listen.
func (g *gateway) serveWeb(ctx context.Context, addr, token string) (url string, wait func(), err error) {
	if _, err := loopbackHosts(addr); err != nil {
		return "", nil, err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("page: %w", err)
	}

	// The Host headers are those of where it listens: a port of 0 is the
	// one it got.
	hosts, err := loopbackHosts(ln.Addr().String())
	if err != nil {
		_ = ln.Close()

		return "", nil, err
	}

	url = "http://" + ln.Addr().String() + "/"

	// The runner's agents report to the page's server: it starts with it.
	if g.newBots != nil {
		if g.bots, err = g.newBots(ctx, url); err != nil {
			g.log.Error("runner not started", "err", err)
		}
	}

	w := newWeb(g.store, g.bots, token, hosts, g.log)
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

	return url, func() { <-stopped }, nil
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
	api.HandleFunc("GET /api/projects/{id}/runner", w.api(w.projectRun))
	api.HandleFunc("PUT /api/projects/{id}/runner", w.api(w.putProjectRunner))
	api.HandleFunc("POST /api/projects/{id}/runner/resume", w.api(w.resumeProjectRunner))
	api.HandleFunc("PUT /api/runner", w.api(w.putRunner))
	api.HandleFunc("POST /api/agents/{agent}/{action}", w.api(w.agentAction))
	api.HandleFunc("GET /api/agents/{agent}/term", w.term)
	api.HandleFunc("PUT /api/github-tokens/{name}", w.api(w.putGitHubToken))
	api.HandleFunc("DELETE /api/github-tokens/{name}", w.api(w.deleteGitHubToken))
	api.HandleFunc("/api/", func(rw http.ResponseWriter, _ *http.Request) { apiError(rw, http.StatusNotFound, "no such API") })

	root := http.NewServeMux()
	root.Handle("/api/", w.authorize(api))
	root.HandleFunc("POST "+hookPath, w.agentHook)
	root.HandleFunc("POST "+finishPath, w.agentFinish)
	root.Handle("/", webui.Handler())

	return w.guard(root)
}

// guard turns away requests not addressed to this machine, and sets the
// headers every response needs.
func (w *web) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !slices.Contains(w.hosts, r.Host) {
			http.Error(rw, "forbidden host", http.StatusMisdirectedRequest)

			return
		}

		h := rw.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		next.ServeHTTP(rw, r)
	})
}

// authorize wants the page token as a bearer token. A header, unlike a
// cookie, is never sent by another site's page.
func (w *web) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")

		got := r.Header.Get(headerAuthorization)
		if got == "" {
			got = wsToken(r)
		}

		if !bearerIs(got, w.token) {
			apiError(rw, http.StatusUnauthorized, "missing or wrong token: open the page with `claude-bot gateway open`")

			return
		}

		r.Body = http.MaxBytesReader(rw, r.Body, maxRequestBody)

		next.ServeHTTP(rw, r)
	})
}

// bearerIs reports, in constant time, whether an Authorization value is
// token as a bearer token.
func bearerIs(header, token string) bool {
	return subtle.ConstantTimeCompare([]byte(header), []byte(bearerPrefix+token)) == 1
}

// api adapts a handler that returns its answer: v as JSON with a 200, no
// content if v is nil, or the error as fail answers it.
func (w *web) api(f func(*http.Request) (any, error)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		v, err := f(r)

		switch {
		case err != nil:
			w.fail(rw, err)
		case v == nil:
			rw.WriteHeader(http.StatusNoContent)
		default:
			writeAPI(rw, http.StatusOK, v)
		}
	}
}

func writeAPI(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)

	enc := json.NewEncoder(rw)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func apiError(rw http.ResponseWriter, status int, msg string) {
	writeAPI(rw, status, map[string]string{"error": msg})
}

// httpError is an error the page is told as it is, with its status.
type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return e.err.Error() }
func (e *httpError) Unwrap() error { return e.err }

func badRequest(err error) error { return &httpError{status: http.StatusBadRequest, err: err} }
func notFound(err error) error   { return &httpError{status: http.StatusNotFound, err: err} }
func conflict(err error) error   { return &httpError{status: http.StatusConflict, err: err} }

func badRequestf(format string, args ...any) error { return badRequest(fmt.Errorf(format, args...)) }

// fail answers with err: with its status if it has one, as a 404, 409 or
// 503 if it is one, else as a 500.
func (w *web) fail(rw http.ResponseWriter, err error) {
	var (
		he    *httpError
		taken *store.ConflictError
	)

	switch {
	case errors.As(err, &he):
		apiError(rw, he.status, he.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, inbox.ErrNoTask):
		apiError(rw, http.StatusNotFound, err.Error())
	case errors.As(err, &taken):
		apiError(rw, http.StatusConflict, err.Error())
	case errors.Is(err, errNoRunner):
		apiError(rw, http.StatusServiceUnavailable, err.Error())
	default:
		w.log.Error("page request", "err", err)
		apiError(rw, http.StatusInternalServerError, err.Error())
	}
}

// decodeBody reads the request's JSON body into v; a body it cannot read
// is a bad request.
func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest(err)
	}

	return nil
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
	home, err := config.Home()
	if err != nil {
		return nil, err
	}

	st, err := gatherStatus(r.Context(), home, w.store)
	if err != nil {
		return nil, err
	}

	return overviewResponse{gatewayStatus: st, Self: Version, Runner: w.runnerOverview()}, nil
}
