package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
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
const (
	defaultUIAddr  = "127.0.0.1:7600"
	uiTokenBytes   = 24
	maxRequestBody = 64 << 10
	taskListLimit  = 300 // handled tasks the page lists
	titleRunes     = 120
	hintRunes      = 4
	webTimeout     = 15 * time.Second
	webShutdown    = 5 * time.Second
)

// Task states as the page shows them.
const (
	taskPending = "pending" // waiting for Claude
	taskWorking = "working" // Claude has it
	taskReplied = "replied" // handled, the bot answered
	taskClosed  = "closed"  // handled without an answer
)

// channelField is one field of a channel's form.
type channelField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Secret      bool     `json:"secret,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Select      bool     `json:"select,omitempty"` // a choice of Options, "" being the default
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty"` // the value to start a new channel with
}

// channelType describes a channel for the page.
type channelType struct {
	Name    string         `json:"name"`
	Label   string         `json:"label"`
	Fields  []channelField `json:"fields"`
	Note    string         `json:"note,omitempty"`     // shown on its form
	AddNote string         `json:"add_note,omitempty"` // shown on its form when adding it
	// Unavailable says why the project cannot add the channel.
	Unavailable string `json:"unavailable,omitempty"`
}

var proxyField = channelField{Key: "proxy", Label: "代理", Placeholder: "留空用默认；direct、env 或 http://host:port"}

var channelTypes = []channelType{
	{Name: lark.Name, Label: "飞书 / Lark", Fields: []channelField{
		{Key: "app_id", Label: "App ID", Required: true, Placeholder: "cli_…"},
		{Key: "app_secret", Label: "App Secret", Secret: true, Required: true},
		{Key: "domain", Label: "版本", Select: true, Options: []string{lark.DomainFeishu, lark.DomainLark}},
		proxyField,
	}, AddNote: "新建飞书应用更方便的办法：在项目里运行 claude-bot init，浏览器里点一下「确认」就建好了。"},
	{Name: dingtalk.Name, Label: "钉钉", Fields: []channelField{
		{Key: "client_id", Label: "Client ID（AppKey）", Required: true},
		{Key: "client_secret", Label: "Client Secret（AppSecret）", Secret: true, Required: true},
		proxyField,
	}},
	{Name: telegram.Name, Label: "Telegram", Fields: []channelField{
		{Key: "token", Label: "Bot token", Secret: true, Required: true, Placeholder: "123456:ABC…"},
		proxyField,
	}},
	{Name: discord.Name, Label: "Discord", Fields: []channelField{
		{Key: "token", Label: "Bot token", Secret: true, Required: true},
		proxyField,
	}},
	{Name: slack.Name, Label: "Slack", Fields: []channelField{
		{Key: "app_token", Label: "App-Level Token", Secret: true, Required: true, Placeholder: "xapp-…"},
		{Key: "bot_token", Label: "Bot User OAuth Token", Secret: true, Required: true, Placeholder: "xoxb-…"},
		proxyField,
	}},
	{Name: github.Name, Label: "GitHub", Fields: []channelField{
		{Key: "repo", Label: "仓库", Required: true, Placeholder: "owner/name"},
		{Key: "token_name", Label: "GitHub token", Select: true},
		proxyField,
	}, Note: "token 在「共用设置」里管理：默认 token 给所有项目用；另一个账号的项目，在那里加一个 token 并指定给它。"},
}

func channelTypeOf(name string) (channelType, bool) {
	i := slices.IndexFunc(channelTypes, func(t channelType) bool { return t.Name == name })
	if i < 0 {
		return channelType{}, false
	}

	return channelTypes[i], true
}

// web is the gateway's page and its API.
type web struct {
	g     *gateway
	token string
	hosts []string // accepted Host headers
	log   *slog.Logger
	// checkGitHub checks a GitHub token against a repository; tests
	// replace it.
	checkGitHub func(ctx context.Context, token string, gh github.Config) error
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

	b := make([]byte, uiTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("make page token: %w", err)
	}

	token = hex.EncodeToString(b)

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

	return []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port}, nil
}

// serveWeb serves the page on addr until ctx ends. It returns once
// listening, with the page's URL, or with why it could not listen.
func (g *gateway) serveWeb(ctx context.Context, addr, token string) (string, error) {
	hosts, err := loopbackHosts(addr)
	if err != nil {
		return "", err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("page: %w", err)
	}

	w := &web{g: g, token: token, hosts: hosts, log: g.log, checkGitHub: checkGitHubAccess}
	srv := &http.Server{Handler: w.routes(), ReadHeaderTimeout: webTimeout}

	go func() {
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

	return "http://" + ln.Addr().String() + "/", nil
}

func (w *web) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/overview", w.overview)
	api.HandleFunc("GET /api/projects/{id}/channel-types", w.channelTypes)
	api.HandleFunc("GET /api/projects/{id}/tasks", w.tasks)
	api.HandleFunc("GET /api/projects/{id}/tasks/{task}", w.task)
	api.HandleFunc("GET /api/projects/{id}/channels", w.channels)
	api.HandleFunc("PUT /api/projects/{id}/channels/{name}", w.putChannel)
	api.HandleFunc("POST /api/projects/{id}/channels/{name}/test", w.testChannel)
	api.HandleFunc("DELETE /api/projects/{id}/channels/{name}", w.deleteChannel)
	api.HandleFunc("DELETE /api/projects/{id}", w.deleteProject)
	api.HandleFunc("GET /api/settings", w.settings)
	api.HandleFunc("PUT /api/settings", w.putSettings)
	api.HandleFunc("PUT /api/github-tokens/{name}", w.putGitHubToken)
	api.HandleFunc("DELETE /api/github-tokens/{name}", w.deleteGitHubToken)
	api.HandleFunc("/api/", func(rw http.ResponseWriter, _ *http.Request) { apiError(rw, http.StatusNotFound, "no such API") })

	root := http.NewServeMux()
	root.Handle("/api/", w.authorize(api))
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
	want := []byte("Bearer " + w.token)

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")

		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			apiError(rw, http.StatusUnauthorized, "missing or wrong token: open the page with `claude-bot gateway open`")

			return
		}

		r.Body = http.MaxBytesReader(rw, r.Body, maxRequestBody)

		next.ServeHTTP(rw, r)
	})
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

// fail answers with err, as a 404, 409 or 400 when it is one, else a 500.
func (w *web) fail(rw http.ResponseWriter, err error) {
	var conflict *store.ConflictError

	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, inbox.ErrNoTask):
		apiError(rw, http.StatusNotFound, err.Error())
	case errors.As(err, &conflict):
		apiError(rw, http.StatusConflict, err.Error())
	case errors.Is(err, errBadRequest):
		apiError(rw, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errBadRequest.Error()+": "))
	default:
		w.log.Error("page request", "err", err)
		apiError(rw, http.StatusInternalServerError, err.Error())
	}
}

var errBadRequest = errors.New("bad request")

func (w *web) project(r *http.Request) (store.Project, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return store.Project{}, fmt.Errorf("%w: project id %q", errBadRequest, r.PathValue("id"))
	}

	return w.g.store.Project(r.Context(), id)
}

// overviewResponse is the gateway and its projects.
type overviewResponse struct {
	gatewayStatus

	Self string `json:"self"` // this binary's version
}

func (w *web) overview(rw http.ResponseWriter, r *http.Request) {
	home, err := config.Home()
	if err != nil {
		w.fail(rw, err)

		return
	}

	st, err := gatherStatus(r.Context(), home, w.g.store)
	if err != nil {
		w.fail(rw, err)

		return
	}

	writeAPI(rw, http.StatusOK, overviewResponse{gatewayStatus: st, Self: Version})
}

// taskSummary is a task as the list shows it.
type taskSummary struct {
	ID        string           `json:"id"`
	Channel   string           `json:"channel"`
	ChatID    string           `json:"chat_id"`
	ChatType  channel.ChatType `json:"chat_type"`
	State     string           `json:"state"`
	Title     string           `json:"title"`
	Senders   []string         `json:"senders"`
	Entries   int              `json:"entries"`
	Replies   int              `json:"replies"`
	Unhandled int              `json:"unhandled"` // user entries Claude has not handled
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Link      string           `json:"link,omitempty"`
}

func summarize(t inbox.Task, pending bool) taskSummary {
	s := taskSummary{
		ID: t.ID, Channel: t.Channel, ChatID: t.ChatID, ChatType: t.ChatType,
		Entries: len(t.Entries), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Link: taskLink(t), Senders: []string{},
	}

	for i, e := range t.Entries {
		if e.Kind == inbox.EntryBot {
			s.Replies++
			s.UpdatedAt = later(s.UpdatedAt, e.At)

			continue
		}

		if s.Title == "" {
			s.Title = titleOf(e)
		}

		if e.Sender != nil && !slices.Contains(s.Senders, senderName(*e.Sender)) {
			s.Senders = append(s.Senders, senderName(*e.Sender))
		}

		if i >= t.Handled {
			s.Unhandled++
		}
	}

	switch {
	case pending && t.Shown > 0:
		s.State = taskWorking
	case pending:
		s.State = taskPending
	case s.Replies > 0:
		s.State = taskReplied
	default:
		s.State = taskClosed
	}

	return s
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

func senderName(u channel.User) string {
	if u.Name != "" {
		return u.Name
	}

	return u.ID
}

// titleOf is the first line of an entry, shortened.
func titleOf(e inbox.Entry) string {
	line, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
	if line == "" && e.File != "" {
		line = filepath.Base(e.File)
	}

	if utf8.RuneCountInString(line) > titleRunes {
		line = string([]rune(line)[:titleRunes]) + "…"
	}

	return line
}

// taskLink is where a task can be seen on its platform, if it has an address.
func taskLink(t inbox.Task) string {
	if t.Channel != github.Name {
		return ""
	}

	repo, number, ok := strings.CutLast(t.ChatID, "/")
	if !ok || strings.Count(repo, "/") != 1 {
		return ""
	}

	if _, err := strconv.Atoi(number); err != nil {
		return ""
	}

	return "https://github.com/" + repo + "/issues/" + number
}

func projectInbox(p store.Project) *inbox.Inbox {
	return inbox.New(filepath.Join(p.Dir, config.DirName, inboxDir))
}

func (w *web) tasks(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	box := projectInbox(p)

	pending, err := box.PendingTasks()
	if err != nil {
		w.fail(rw, err)

		return
	}

	done, err := box.DoneTasks()
	if err != nil {
		w.fail(rw, err)

		return
	}

	out := make([]taskSummary, 0, len(pending)+len(done))
	for _, t := range pending {
		out = append(out, summarize(t, true))
	}

	for _, t := range done {
		out = append(out, summarize(t, false))
	}

	writeAPI(rw, http.StatusOK, listTasks(out, taskListLimit))
}

// listTasks puts the latest first and keeps every open task, but only the
// latest limit handled ones: the page shows the open tasks and keeps the
// handled ones in its archive.
func listTasks(all []taskSummary, limit int) []taskSummary {
	slices.SortFunc(all, func(a, b taskSummary) int { return b.UpdatedAt.Compare(a.UpdatedAt) })

	out := all[:0]
	done := 0

	for _, s := range all {
		if s.State == taskReplied || s.State == taskClosed {
			if done == limit {
				continue
			}

			done++
		}

		out = append(out, s)
	}

	return out
}

// taskDetail is a task with its whole conversation.
type taskDetail struct {
	taskSummary

	Handled int           `json:"handled"`
	Thread  string        `json:"thread_id,omitempty"`
	Log     []inbox.Entry `json:"log"`
}

func (w *web) task(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	id := r.PathValue("task")
	if !strings.HasPrefix(id, inbox.TaskPrefix) {
		w.fail(rw, fmt.Errorf("%w: %s", inbox.ErrNoTask, id))

		return
	}

	t, pending, err := projectInbox(p).Task(id)
	if err != nil {
		w.fail(rw, err)

		return
	}

	writeAPI(rw, http.StatusOK, taskDetail{taskSummary: summarize(t, pending), Handled: t.Handled, Thread: t.ThreadID, Log: t.Entries})
}

// secretView is what the page learns about a secret.
type secretView struct {
	Set  bool   `json:"set"`
	Hint string `json:"hint,omitempty"` // its last digits
}

func maskSecret(v string) secretView {
	if v == "" {
		return secretView{}
	}

	if r := []rune(v); len(r) >= 2*hintRunes {
		return secretView{Set: true, Hint: "…" + string(r[len(r)-hintRunes:])}
	}

	return secretView{Set: true}
}

// channelView is a configured channel, secrets masked.
type channelView struct {
	Name      string         `json:"name"`
	Label     string         `json:"label"`
	Fields    map[string]any `json:"fields"`
	AppKey    string         `json:"app_key"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func viewChannel(c store.Channel) channelView {
	t, _ := channelTypeOf(c.Name)
	v := channelView{Name: c.Name, Label: t.Label, AppKey: c.AppKey, UpdatedAt: c.UpdatedAt, Fields: map[string]any{}}

	raw := stringFields(c.Config)

	for _, f := range t.Fields {
		s := raw[f.Key]
		if f.Secret {
			v.Fields[f.Key] = maskSecret(s)
		} else {
			v.Fields[f.Key] = s
		}
	}

	return v
}

func (w *web) channels(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	chans, err := w.g.store.Channels(r.Context(), p.ID)
	if err != nil {
		w.fail(rw, err)

		return
	}

	out := make([]channelView, 0, len(chans))
	for _, c := range chans {
		out = append(out, viewChannel(c))
	}

	writeAPI(rw, http.StatusOK, out)
}

// channelInput is a channel form as submitted: an empty secret keeps the
// one stored. No form at all (a card's 测试连接) means the channel as stored.
type channelInput struct {
	Fields map[string]string `json:"fields"`
}

// submitted reads a channel form and merges it into the stored channel,
// returning the configuration it makes.
func (w *web) submitted(r *http.Request, p store.Project) (config.Config, error) {
	name := r.PathValue("name")

	t, ok := channelTypeOf(name)
	if !ok {
		return config.Config{}, fmt.Errorf("%w: unknown channel %q", errBadRequest, name)
	}

	var in channelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return config.Config{}, fmt.Errorf("%w: %w", errBadRequest, err)
	}

	chans, err := w.g.store.Channels(r.Context(), p.ID)
	if err != nil {
		return config.Config{}, err
	}

	var stored json.RawMessage
	if i := slices.IndexFunc(chans, func(c store.Channel) bool { return c.Name == name }); i >= 0 {
		stored = chans[i].Config
	}

	raw, err := mergeForm(t, stored, in.Fields)
	if err != nil {
		return config.Config{}, err
	}

	cfg, err := config.ChannelConfig(name, raw)
	if err != nil {
		return cfg, fmt.Errorf("%w: %w", errBadRequest, err)
	}

	if cfg.GitHub != nil {
		cfg.GitHub.Token, err = config.GitHubToken(r.Context(), w.g.store, cfg.GitHubTokenName)
		if errors.Is(err, config.ErrNoGitHubToken) {
			return cfg, fmt.Errorf("%w: %w", errBadRequest, err)
		}

		if err != nil {
			return cfg, err
		}

		if cfg.GitHub.Token == "" {
			return cfg, fmt.Errorf("%w: the gateway has no default GitHub token yet: add one in 共用设置", errBadRequest)
		}
	}

	if _, err := openChannels(cfg, quiet); err != nil {
		return cfg, fmt.Errorf("%w: %w", errBadRequest, err)
	}

	return cfg, nil
}

// stringFields is a stored channel configuration's text fields.
func stringFields(raw json.RawMessage) map[string]string {
	var all map[string]any

	_ = json.Unmarshal(raw, &all)

	out := make(map[string]string, len(all))

	for k, v := range all {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}

	return out
}

// mergeForm lays a submitted form over the stored configuration: an empty
// secret keeps the stored one, an empty plain field clears it. A nil form
// changes nothing.
func mergeForm(t channelType, stored json.RawMessage, form map[string]string) (json.RawMessage, error) {
	merged := stringFields(stored)

	if form != nil {
		for _, f := range t.Fields {
			if v := strings.TrimSpace(form[f.Key]); v != "" || !f.Secret {
				merged[f.Key] = v
			}
		}
	}

	for _, f := range t.Fields {
		if f.Required && merged[f.Key] == "" {
			return nil, fmt.Errorf("%w: %s is required", errBadRequest, f.Label)
		}
	}

	raw, err := json.Marshal(merged)

	return raw, err
}

func (w *web) putChannel(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	cfg, err := w.submitted(r, p)
	if err != nil {
		w.fail(rw, err)

		return
	}

	rows, err := cfg.Channels()
	if err != nil {
		w.fail(rw, err)

		return
	}

	if err := w.g.store.PutChannel(r.Context(), p.ID, rows[0]); err != nil {
		w.fail(rw, err)

		return
	}

	rows[0].UpdatedAt = time.Now().UTC()

	writeAPI(rw, http.StatusOK, viewChannel(rows[0]))
}

// testResult is the outcome of a credentials check.
type testResult struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"` // who the credentials are
	Error    string `json:"error,omitempty"`
}

// testChannel checks the submitted credentials against the platform, the way
// init does, without connecting: a second connection to an app would take
// messages away from the one serving it.
func (w *web) testChannel(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	cfg, err := w.submitted(r, p)
	if errors.Is(err, errBadRequest) {
		writeAPI(rw, http.StatusOK, testResult{Error: strings.TrimPrefix(err.Error(), errBadRequest.Error()+": ")})

		return
	}

	if err != nil {
		w.fail(rw, err)

		return
	}

	chans, err := openChannels(cfg, quiet)
	if err != nil || len(chans) != 1 {
		w.fail(rw, fmt.Errorf("open channel: %w", err))

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webTimeout)
	defer cancel()

	who, err := whoIs(ctx, chans[0])
	if err != nil {
		writeAPI(rw, http.StatusOK, testResult{Error: err.Error()})

		return
	}

	writeAPI(rw, http.StatusOK, testResult{OK: true, Identity: who})
}

// whoIs asks the platform who a channel's credentials belong to.
func whoIs(ctx context.Context, ch channel.Channel) (string, error) {
	switch c := ch.(type) {
	case *lark.Channel:
		bot, err := c.Bot(ctx)

		return "飞书应用「" + bot.AppName + "」", err
	case *discord.Channel:
		bot, err := c.Bot(ctx)

		return "Discord bot " + bot.Name, err
	case *telegram.Channel:
		bot, err := c.Bot(ctx)

		return "Telegram @" + bot.Username, err
	case *slack.Channel:
		bot, err := c.Bot(ctx)
		if err == nil {
			err = c.CheckAppToken(ctx)
		}

		return "Slack bot " + bot.Name + "（" + bot.Team + "）", err
	case *dingtalk.Channel:
		app, err := c.App(ctx)

		return "钉钉应用 " + app.ClientID, err
	case *github.Channel:
		acct, err := c.Account(ctx)
		who := acct.Login + " @ " + acct.Repo

		if err == nil && !acct.CanPush {
			who += "（没有写权限：能收消息、发评论，但不能 close / merge）"
		}

		return who, err
	}

	return "", fmt.Errorf("cannot check channel %s", ch.Name())
}

func (w *web) deleteChannel(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	if err := w.g.store.RemoveChannel(r.Context(), p.ID, r.PathValue("name")); err != nil {
		w.fail(rw, err)

		return
	}

	rw.WriteHeader(http.StatusNoContent)
}

func (w *web) deleteProject(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	path, err := unregister(r.Context(), w.g.store, p)
	if err != nil {
		w.fail(rw, err)

		return
	}

	writeAPI(rw, http.StatusOK, map[string]string{"config": path})
}

// settingsView is the shared settings, masked.
type settingsView struct {
	TypeSafe secretView `json:"typesafe_key"`
	// GitHubTokens are the default token (named "") and the named ones,
	// with the projects each serves.
	GitHubTokens   []githubTokenView `json:"github_tokens"`
	GitHubProjects []githubProject   `json:"github_projects"` // the projects with a GitHub channel
}

func (w *web) settingsNow(ctx context.Context) (settingsView, error) {
	var v settingsView

	key, err := w.g.store.Setting(ctx, store.SettingTypeSafeKey)
	if err != nil {
		return v, err
	}

	if v.GitHubTokens, v.GitHubProjects, err = w.githubTokens(ctx); err != nil {
		return v, err
	}

	v.TypeSafe = maskSecret(key)

	return v, nil
}

func (w *web) settings(rw http.ResponseWriter, r *http.Request) {
	v, err := w.settingsNow(r.Context())
	if err != nil {
		w.fail(rw, err)

		return
	}

	writeAPI(rw, http.StatusOK, v)
}

// settingsInput replaces the settings given; empty ones are kept.
type settingsInput struct {
	TypeSafeKey string `json:"typesafe_key"`
	GitHubToken string `json:"github_token"`
}

func (w *web) putSettings(rw http.ResponseWriter, r *http.Request) {
	var in settingsInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		w.fail(rw, fmt.Errorf("%w: %w", errBadRequest, err))

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webTimeout)
	defer cancel()

	if key := strings.TrimSpace(in.TypeSafeKey); key != "" {
		if err := jev.New(key).Check(ctx); err != nil {
			w.fail(rw, fmt.Errorf("%w: TypeSafe API key not accepted: %w", errBadRequest, err))

			return
		}

		if err := w.g.store.SetSetting(ctx, store.SettingTypeSafeKey, key); err != nil {
			w.fail(rw, err)

			return
		}
	}

	if token := strings.TrimSpace(in.GitHubToken); token != "" {
		if err := w.checkGitHubToken(ctx, token, ""); err != nil {
			w.fail(rw, fmt.Errorf("%w: GitHub token not accepted: %w", errBadRequest, err))

			return
		}

		if err := w.g.store.SetSetting(ctx, store.SettingGitHubToken, token); err != nil {
			w.fail(rw, err)

			return
		}
	}

	w.settings(rw, r)
}
