//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/store"
)

const (
	testPageToken = "page-token"
	testPageHost  = "127.0.0.1:7600"
)

// page is the web API of a gateway fixture.
type page struct {
	t *testing.T
	f *gatewayFixture
	h http.Handler
}

func newPage(t *testing.T) *page {
	t.Helper()

	f := newGatewayFixture(t)

	hosts, err := loopbackHosts(testPageHost)
	if err != nil {
		t.Fatal(err)
	}

	w := &web{g: f.g, token: testPageToken, hosts: hosts, log: slog.New(slog.DiscardHandler), checkGitHub: fakeGitHubCheck}

	return &page{t: t, f: f, h: w.routes()}
}

// fakeGitHubCheck accepts tokens starting with "good" for any repository.
func fakeGitHubCheck(_ context.Context, token string, gh github.Config) error {
	if !strings.HasPrefix(token, "good") {
		return errors.New("401 Bad credentials")
	}

	if gh.Repo == "" {
		return errors.New("no repository")
	}

	return nil
}

// call makes an authorized request and returns the status and body.
func (p *page) call(method, path string, body any) (int, []byte) {
	p.t.Helper()

	var in bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&in).Encode(body); err != nil {
			p.t.Fatal(err)
		}
	}

	r := httptest.NewRequestWithContext(p.t.Context(), method, path, &in)
	r.Host = testPageHost
	r.Header.Set("Authorization", "Bearer "+testPageToken)

	rw := httptest.NewRecorder()
	p.h.ServeHTTP(rw, r)

	return rw.Code, rw.Body.Bytes()
}

func (p *page) decode(raw []byte, v any) {
	p.t.Helper()

	if err := json.Unmarshal(raw, v); err != nil {
		p.t.Fatalf("decode %s: %v", raw, err)
	}
}

func TestWeb_onlyThisMachineWithTheToken(t *testing.T) {
	t.Parallel()

	p := newPage(t)

	cases := []struct {
		name, host, auth, path string
		want                   int
	}{
		{"another host", "evil.example:7600", "Bearer " + testPageToken, "/api/overview", http.StatusMisdirectedRequest},
		{"rebound name", "attacker.test", "", "/", http.StatusMisdirectedRequest},
		{"no token", testPageHost, "", "/api/overview", http.StatusUnauthorized},
		{"wrong token", "localhost:7600", "Bearer nope", "/api/settings", http.StatusUnauthorized},
		{"token without bearer", testPageHost, testPageToken, "/api/overview", http.StatusUnauthorized},
		{"page needs no token", "localhost:7600", "", "/", http.StatusOK},
		{"page routes load the page", "[::1]:7600", "", "/p/1/channels", http.StatusOK},
		{"missing asset", testPageHost, "", "/assets/nope.js", http.StatusNotFound},
		{"api with token", testPageHost, "Bearer " + testPageToken, "/api/overview", http.StatusOK},
		{"unknown api", testPageHost, "Bearer " + testPageToken, "/api/nope", http.StatusNotFound},
	}

	for _, c := range cases {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, c.path, nil)
		r.Host = c.host

		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}

		rw := httptest.NewRecorder()
		p.h.ServeHTTP(rw, r)

		if rw.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rw.Code, c.want, rw.Body)
		}

		if c.want != http.StatusMisdirectedRequest && rw.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no Content-Security-Policy", c.name)
		}

		if strings.HasPrefix(c.path, "/api/") && c.want != http.StatusMisdirectedRequest && rw.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: API answer may be cached", c.name)
		}
	}
}

func TestLoopbackHosts(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:7600", "localhost:0", "[::1]:7600"} {
		if _, err := loopbackHosts(addr); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}

	for _, addr := range []string{"0.0.0.0:7600", ":7600", "192.168.1.2:7600", "example.com:80", "127.0.0.1"} {
		if _, err := loopbackHosts(addr); err == nil {
			t.Errorf("%s: accepted", addr)
		}
	}
}

// secretFixture stores a project and settings full of recognizable secrets.
func secretFixture(t *testing.T, p *page) (store.Project, map[string]string) {
	t.Helper()

	secrets := map[string]string{
		"lark":     "lark-app-secret-0001",
		"slack":    "xoxb-slack-bot-token-0002",
		"slackApp": "xapp-1-A0SLACK-0003",
		"typesafe": "ts-typesafe-key-0004",
		"github":   "ghp_github-token-0005",
	}

	proj, err := p.f.store.SaveProject(t.Context(), t.TempDir(), []store.Channel{
		{Name: "lark", AppKey: "lark:cli_a", Config: json.RawMessage(`{"app_id":"cli_a","app_secret":"` + secrets["lark"] + `"}`)},
		{Name: "slack", AppKey: "slack:A0SLACK", Config: json.RawMessage(`{"app_token":"` + secrets["slackApp"] + `","bot_token":"` + secrets["slack"] + `"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}

	for key, v := range map[string]string{store.SettingTypeSafeKey: secrets["typesafe"], store.SettingGitHubToken: secrets["github"]} {
		if err := p.f.store.SetSetting(t.Context(), key, v); err != nil {
			t.Fatal(err)
		}
	}

	return proj, secrets
}

func TestWeb_neverShowsSecrets(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	proj, secrets := secretFixture(t, p)
	id := projectPath(proj)

	for _, path := range []string{"/api/overview", "/api/settings", id + "/channel-types", id + "/channels", id + "/tasks"} {
		status, body := p.call(http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, status, body)
		}

		for name, s := range secrets {
			if bytes.Contains(body, []byte(s)) {
				t.Errorf("%s shows the %s secret", path, name)
			}
		}
	}
}

func TestWeb_showsWhichSecretsAreSet(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	proj, _ := secretFixture(t, p)

	_, body := p.call(http.MethodGet, "/api/settings", nil)

	var got settingsView
	p.decode(body, &got)

	if !got.TypeSafe.Set || got.TypeSafe.Hint != "…0004" || len(got.GitHubTokens) != 1 || got.GitHubTokens[0].Token.Hint != "…0005" {
		t.Errorf("settings = %+v", got)
	}

	_, body = p.call(http.MethodGet, projectPath(proj)+"/channels", nil)

	var chans []struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	p.decode(body, &chans)

	if len(chans) != 2 || string(chans[0].Fields["app_id"]) != `"cli_a"` || string(chans[0].Fields["app_secret"]) != `{"set":true,"hint":"…0001"}` {
		t.Errorf("channels = %s", body)
	}
}

func TestMaskSecret(t *testing.T) {
	t.Parallel()

	cases := map[string]secretView{
		"":                {},
		"short":           {Set: true}, // too short to show any of it
		"long-secret-abc": {Set: true, Hint: "…-abc"},
		"密钥密钥密钥密钥":        {Set: true, Hint: "…密钥密钥"},
	}

	for in, want := range cases {
		if got := maskSecret(in); got != want {
			t.Errorf("maskSecret(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func projectPath(p store.Project) string { return "/api/projects/" + strconv.FormatInt(p.ID, 10) }

func storedChannel(t *testing.T, s *store.Store, project int64, name string) map[string]string {
	t.Helper()

	chans, err := s.Channels(t.Context(), project)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range chans {
		if c.Name == name {
			var out map[string]string
			if err := json.Unmarshal(c.Config, &out); err != nil {
				t.Fatal(err)
			}

			return out
		}
	}

	return nil
}

func putForm(p *page, proj store.Project, name string, fields map[string]string) (int, []byte) {
	return p.call(http.MethodPut, projectPath(proj)+"/channels/"+name, channelInput{Fields: fields})
}

func TestWeb_channelFormKeepsSecrets(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	a := p.f.register("a", "cli_a")

	// An empty secret keeps the stored one.
	if status, body := putForm(p, a, "lark", map[string]string{"app_id": "cli_a", "app_secret": "", "domain": "lark"}); status != http.StatusOK {
		t.Fatalf("keep secret: %d %s", status, body)
	}

	if got := storedChannel(t, p.f.store, a.ID, "lark"); got["app_secret"] != "s" || got["domain"] != "lark" {
		t.Errorf("after keeping the secret: %v", got)
	}

	// A new one replaces it; a cleared plain field is cleared.
	if status, body := putForm(p, a, "lark", map[string]string{"app_id": "cli_a", "app_secret": " new-secret "}); status != http.StatusOK {
		t.Fatalf("replace secret: %d %s", status, body)
	}

	if got := storedChannel(t, p.f.store, a.ID, "lark"); got["app_secret"] != "new-secret" || got["domain"] != "" {
		t.Errorf("after replacing the secret: %v", got)
	}

	if status, _ := p.call(http.MethodDelete, projectPath(a)+"/channels/lark", nil); status != http.StatusNoContent {
		t.Errorf("delete: %d", status)
	}

	if got := storedChannel(t, p.f.store, a.ID, "lark"); got != nil {
		t.Errorf("deleted channel still stored: %v", got)
	}
}

func TestWeb_channelFormRefused(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	a := p.f.register("a", "cli_a")
	b := p.f.register("b", "cli_b")

	cases := []struct {
		name    string
		proj    store.Project
		channel string
		fields  map[string]string
		want    int
	}{
		{"another project's app", b, "lark", map[string]string{"app_id": "cli_a", "app_secret": "x"}, http.StatusConflict},
		{"required secret missing", a, "telegram", map[string]string{"token": ""}, http.StatusBadRequest},
		{"required field missing", b, "lark", map[string]string{"app_id": ""}, http.StatusBadRequest},
		{"unknown channel", a, "irc", map[string]string{}, http.StatusBadRequest},
		{"unknown project", store.Project{ID: 999}, "lark", map[string]string{"app_id": "x", "app_secret": "y"}, http.StatusNotFound},
	}

	for _, c := range cases {
		if status, body := putForm(p, c.proj, c.channel, c.fields); status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, status, body, c.want)
		}
	}

	if got := storedChannel(t, p.f.store, b.ID, "lark"); got["app_id"] != "cli_b" {
		t.Errorf("a refused form changed project b: %v", got)
	}

	// A form that does not make a channel is a failed test, not an error.
	status, body := p.call(http.MethodPost, projectPath(a)+"/channels/telegram/test", channelInput{Fields: map[string]string{}})

	var res testResult
	p.decode(body, &res)

	if status != http.StatusOK || res.OK || !strings.Contains(res.Error, "required") {
		t.Errorf("test without a token: %d %+v", status, res)
	}
}

// seedTasks stores one task in each state.
func seedTasks(t *testing.T, box *inbox.Inbox) {
	t.Helper()

	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := &channel.User{ID: "ou_1", Name: "Alice"}
	bob := &channel.User{ID: "ou_2"}

	save := func(tk inbox.Task, pending bool) {
		if err := box.Locked(func() error { return box.SaveTask(tk, pending) }); err != nil {
			t.Fatal(err)
		}
	}

	user := func(msg, text string, who *channel.User, minutes int) inbox.Entry {
		return inbox.Entry{Kind: inbox.EntryUser, Message: msg, Sender: who, At: at.Add(time.Duration(minutes) * time.Minute), Text: text}
	}

	replied := inbox.Task{ID: "task:replied", Channel: "lark", ChatID: "oc_1", ChatType: channel.ChatGroup, CreatedAt: at}
	replied.Append("m1", []inbox.Entry{user("m1", "部署失败了\n日志在附件里", alice, 0), user("m1", "", alice, 0)})
	replied.Entries[1].File = "/tmp/x/deploy.log"
	replied.Entries = append(replied.Entries, inbox.Entry{Kind: inbox.EntryBot, At: at.Add(30 * time.Minute), Text: "修好了"})
	replied.Handled = len(replied.Entries)
	save(replied, false)

	working := inbox.Task{ID: "task:working", Channel: "github", ChatID: "Owner/Repo/12", CreatedAt: at}
	working.Append("m2", []inbox.Entry{user("m2", "issue title", bob, 10)})
	working.Append("m3", []inbox.Entry{user("m3", "more", alice, 20)})
	working.Handled, working.Shown = 1, 2
	save(working, true)

	pending := inbox.Task{ID: "task:pending", Channel: "lark", ChatID: "oc_2", CreatedAt: at}
	pending.Append("m4", []inbox.Entry{user("m4", strings.Repeat("长", titleRunes+5), alice, 15)})
	save(pending, true)

	closed := inbox.Task{ID: "task:closed", Channel: "lark", ChatID: "oc_3", CreatedAt: at}
	closed.Append("m5", []inbox.Entry{user("m5", "谢谢", bob, 5)})
	closed.Handled = 1
	save(closed, false)
}

func TestWeb_taskList(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	proj := p.f.register("a", "cli_a")
	seedTasks(t, projectInbox(proj))

	status, body := p.call(http.MethodGet, projectPath(proj)+"/tasks", nil)
	if status != http.StatusOK {
		t.Fatalf("tasks: %d %s", status, body)
	}

	var list []taskSummary
	p.decode(body, &list)

	order := make([]string, 0, len(list))
	byID := map[string]taskSummary{}

	for _, s := range list {
		order = append(order, s.ID+"="+s.State)
		byID[s.ID] = s
	}

	// Latest first; a bot reply counts as activity.
	if want := "task:replied=replied task:working=working task:pending=pending task:closed=closed"; strings.Join(order, " ") != want {
		t.Errorf("tasks = %s, want %s", strings.Join(order, " "), want)
	}

	checks := map[string]bool{
		"replied": byID["task:replied"].Title == "部署失败了" && byID["task:replied"].Replies == 1 &&
			byID["task:replied"].Unhandled == 0 && strings.Join(byID["task:replied"].Senders, ",") == "Alice",
		"working": byID["task:working"].Unhandled == 1 && byID["task:working"].Link == "https://github.com/Owner/Repo/issues/12" &&
			strings.Join(byID["task:working"].Senders, ",") == "ou_2,Alice",
		"pending": strings.HasSuffix(byID["task:pending"].Title, "…") && len([]rune(byID["task:pending"].Title)) == titleRunes+1,
	}

	for name, ok := range checks {
		if !ok {
			t.Errorf("%s = %+v", name, byID["task:"+name])
		}
	}
}

func TestWeb_task(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	proj := p.f.register("a", "cli_a")
	seedTasks(t, projectInbox(proj))

	status, body := p.call(http.MethodGet, projectPath(proj)+"/tasks/task:replied", nil)

	var detail taskDetail
	p.decode(body, &detail)

	if status != http.StatusOK || len(detail.Log) != 3 || detail.Handled != 3 || detail.Log[1].File != "/tmp/x/deploy.log" {
		t.Errorf("detail: %d %s", status, body)
	}

	for _, id := range []string{"task:nope", "..%2f..%2fetc", "m1"} {
		if status, body := p.call(http.MethodGet, projectPath(proj)+"/tasks/"+id, nil); status != http.StatusNotFound {
			t.Errorf("task %s: %d %s", id, status, body)
		}
	}

	if status, _ := p.call(http.MethodGet, "/api/projects/x/tasks", nil); status != http.StatusBadRequest {
		t.Errorf("bad project id: %d", status)
	}

	empty := p.f.register("empty", "cli_e")
	if status, body := p.call(http.MethodGet, projectPath(empty)+"/tasks", nil); status != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("no tasks: %d %s", status, body)
	}
}

func TestTaskLink(t *testing.T) {
	t.Parallel()

	cases := []struct {
		channel, chat, want string
	}{
		{"github", "o/r/7", "https://github.com/o/r/issues/7"},
		{"github", "o/r", ""},
		{"github", "o/r/x", ""},
		{"github", "a/b/c/7", ""},
		{"lark", "o/r/7", ""},
	}

	for _, c := range cases {
		if got := taskLink(inbox.Task{Channel: c.channel, ChatID: c.chat}); got != c.want {
			t.Errorf("taskLink(%s %s) = %q, want %q", c.channel, c.chat, got, c.want)
		}
	}
}

// githubPage is a page over two projects with GitHub channels on the
// default token.
func githubPage(t *testing.T) (p *page, a, b store.Project) {
	t.Helper()

	p = newPage(t)

	if err := p.f.store.SetSetting(t.Context(), store.SettingGitHubToken, "good-default-token"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"a", "b"} {
		proj, err := p.f.store.SaveProject(t.Context(), t.TempDir(), []store.Channel{
			{Name: "github", AppKey: "github:o/" + name, Config: json.RawMessage(`{"repo":"o/` + name + `"}`)},
		})
		if err != nil {
			t.Fatal(err)
		}

		if name == "a" {
			a = proj
		} else {
			b = proj
		}
	}

	return p, a, b
}

// tokenOf is the GitHub token a project's listener would use.
func tokenOf(t *testing.T, s *store.Store, p store.Project) string {
	t.Helper()

	cfg, err := config.FromStore(t.Context(), s, p)
	if err != nil {
		t.Fatalf("FromStore(%s): %v", p.Name, err)
	}

	return cfg.GitHub.Token
}

func TestWeb_githubTokenServesItsProjects(t *testing.T) {
	t.Parallel()

	p, a, b := githubPage(t)

	status, body := p.call(http.MethodPut, "/api/github-tokens/work", githubTokenInput{Token: "good-work-token", Projects: []int64{b.ID}})
	if status != http.StatusOK {
		t.Fatalf("add token: %d %s", status, body)
	}

	if bytes.Contains(body, []byte("good-work-token")) || bytes.Contains(body, []byte("good-default-token")) {
		t.Errorf("settings show a token: %s", body)
	}

	if got, want := tokenOf(t, p.f.store, a)+" "+tokenOf(t, p.f.store, b), "good-default-token good-work-token"; got != want {
		t.Errorf("tokens of a, b = %s, want %s", got, want)
	}

	var got settingsView
	p.decode(body, &got)

	if len(got.GitHubTokens) != 2 || strings.Join(got.GitHubTokens[0].Projects, ",") != a.Name ||
		got.GitHubTokens[1].Name != "work" || strings.Join(got.GitHubTokens[1].Projects, ",") != b.Name {
		t.Errorf("tokens = %+v", got.GitHubTokens)
	}
}

func TestWeb_githubChannelForm(t *testing.T) {
	t.Parallel()

	p, a, b := githubPage(t)

	if status, body := p.call(http.MethodPut, "/api/github-tokens/work", githubTokenInput{Token: "good-work-token", Projects: []int64{b.ID}}); status != http.StatusOK {
		t.Fatalf("add token: %d %s", status, body)
	}

	// The channel form offers the token by name, and refuses unknown ones.
	_, body := p.call(http.MethodGet, projectPath(a)+"/channel-types", nil)

	var types []channelType
	p.decode(body, &types)

	gh := types[slices.IndexFunc(types, func(t channelType) bool { return t.Name == "github" })]
	if f := gh.Fields[slices.IndexFunc(gh.Fields, func(f channelField) bool { return f.Key == "token_name" })]; !slices.Equal(f.Options, []string{"work"}) {
		t.Errorf("token choices = %v", f.Options)
	}

	if gh.Unavailable == "" {
		t.Error("a repository without a GitHub remote can add a GitHub channel")
	}

	if status, body := putForm(p, a, "github", map[string]string{"repo": "o/a", "token_name": "nope"}); status != http.StatusBadRequest {
		t.Errorf("unknown token name: %d %s", status, body)
	}

	if status, body := putForm(p, a, "github", map[string]string{"repo": "o/a", "token_name": "work"}); status != http.StatusOK || tokenOf(t, p.f.store, a) != "good-work-token" {
		t.Errorf("choose token in the form: %d %s", status, body)
	}
}

func TestWeb_githubTokenRefusedOrInUse(t *testing.T) {
	t.Parallel()

	p, _, b := githubPage(t)

	cases := []struct {
		name string
		in   githubTokenInput
		want int
	}{
		{"bad token", githubTokenInput{Token: "bad", Projects: []int64{b.ID}}, http.StatusBadRequest},
		{"new token without a value", githubTokenInput{Projects: []int64{b.ID}}, http.StatusBadRequest},
		{"project without GitHub", githubTokenInput{Token: "good", Projects: []int64{999}}, http.StatusBadRequest},
	}

	for _, c := range cases {
		if status, body := p.call(http.MethodPut, "/api/github-tokens/work", c.in); status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, status, body, c.want)
		}
	}

	if status, _ := p.call(http.MethodPut, "/api/github-tokens/a%20b", githubTokenInput{Token: "good"}); status != http.StatusBadRequest {
		t.Errorf("bad name: %d", status)
	}

	if tokens, _ := config.GitHubTokens(t.Context(), p.f.store); len(tokens) != 0 {
		t.Errorf("refused tokens were stored: %v", tokens)
	}

	if status, body := p.call(http.MethodPut, "/api/github-tokens/work", githubTokenInput{Token: "good-work", Projects: []int64{b.ID}}); status != http.StatusOK {
		t.Fatalf("add: %d %s", status, body)
	}

	if status, _ := p.call(http.MethodDelete, "/api/github-tokens/work", nil); status != http.StatusConflict {
		t.Errorf("delete a token in use: %d, want 409", status)
	}

	// Serving no project, b goes back to the default token; then it can go.
	if status, body := p.call(http.MethodPut, "/api/github-tokens/work", githubTokenInput{}); status != http.StatusOK || tokenOf(t, p.f.store, b) != "good-default-token" {
		t.Errorf("unassign: %d %s", status, body)
	}

	if status, _ := p.call(http.MethodDelete, "/api/github-tokens/work", nil); status != http.StatusOK {
		t.Errorf("delete an unused token: %d", status)
	}

	if tokens, _ := config.GitHubTokens(t.Context(), p.f.store); len(tokens) != 0 {
		t.Errorf("deleted token still stored: %v", tokens)
	}
}
