package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The page's channels: a form per channel type, saved into the gateway's
// database and checked against the platform without connecting.

// hintRunes is how many of a secret's last runes the page sees.
const hintRunes = 4

// Form keys the page fills in by itself; each is the JSON key of the field
// in the channel's configuration.
const (
	fieldRepo      = "repo"
	fieldTokenName = "token_name"
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

// channelForm is what the page asks for a kind of channel, besides what
// config.Kinds says of it.
type channelForm struct {
	fields  []channelField
	note    string
	addNote string
}

// channelForms are the forms of every kind of channel; each field's key is
// the JSON key of the field in the channel's configuration.
var channelForms = map[string]channelForm{
	lark.Name: {fields: []channelField{
		{Key: "app_id", Label: "App ID", Required: true, Placeholder: "cli_…"},
		{Key: "app_secret", Label: "App Secret", Secret: true, Required: true},
		{Key: "domain", Label: "版本", Select: true, Options: []string{lark.DomainFeishu, lark.DomainLark}},
		proxyField,
	}, addNote: "新建飞书应用更方便的办法：在项目里运行 claude-bot init，浏览器里点一下「确认」就建好了。"},
	dingtalk.Name: {fields: []channelField{
		{Key: "client_id", Label: "Client ID（AppKey）", Required: true},
		{Key: "client_secret", Label: "Client Secret（AppSecret）", Secret: true, Required: true},
		proxyField,
	}},
	telegram.Name: {fields: []channelField{
		{Key: "token", Label: "Bot token", Secret: true, Required: true, Placeholder: "123456:ABC…"},
		proxyField,
	}},
	discord.Name: {fields: []channelField{
		{Key: "token", Label: "Bot token", Secret: true, Required: true},
		proxyField,
	}},
	slack.Name: {fields: []channelField{
		{Key: "app_token", Label: "App-Level Token", Secret: true, Required: true, Placeholder: "xapp-…"},
		{Key: "bot_token", Label: "Bot User OAuth Token", Secret: true, Required: true, Placeholder: "xoxb-…"},
		proxyField,
	}},
	github.Name: {fields: []channelField{
		{Key: fieldRepo, Label: "仓库", Required: true, Placeholder: "owner/name"},
		{Key: fieldTokenName, Label: "GitHub token", Select: true},
		proxyField,
	}, note: "token 在「共用设置」里管理：默认 token 给所有项目用；另一个账号的项目，在那里加一个 token 并指定给它。"},
}

// channelTypeOf describes the kind of channel called name for the page.
func channelTypeOf(name string) (channelType, bool) {
	k, ok := config.KindOf(name)
	if !ok {
		return channelType{}, false
	}

	f := channelForms[name]

	return channelType{Name: k.Name, Label: k.Label, Fields: slices.Clone(f.fields), Note: f.note, AddNote: f.addNote}, true
}

// channelTypes are the channels a project can add, with the choices it has:
// the GitHub tokens, the repository of its GitHub remote. A repository not
// on GitHub has nothing there to watch.
func (w *web) channelTypes(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	named, err := config.GitHubTokens(r.Context(), w.store)
	if err != nil {
		return nil, err
	}

	names := sortedKeys(named)
	remote := githubRemote(r.Context(), p.Dir)
	kinds := config.Kinds()
	out := make([]channelType, len(kinds))

	for i, k := range kinds {
		t, _ := channelTypeOf(k.Name)

		if t.Name == github.Name {
			for j, f := range t.Fields {
				switch f.Key {
				case fieldRepo:
					t.Fields[j].Default = remote
				case fieldTokenName:
					t.Fields[j].Options = names
				}
			}

			if remote == "" {
				t.Unavailable = "仓库没有 GitHub 上的 remote"
			}
		}

		out[i] = t
	}

	return out, nil
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

	raw := storedFields(c.Config)

	for _, f := range t.Fields {
		s, _ := raw[f.Key].(string)
		if f.Secret {
			v.Fields[f.Key] = maskSecret(s)
		} else {
			v.Fields[f.Key] = s
		}
	}

	return v
}

func (w *web) channels(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	chans, err := w.store.Channels(r.Context(), p.ID)
	if err != nil {
		return nil, err
	}

	out := make([]channelView, 0, len(chans))
	for _, c := range chans {
		out = append(out, viewChannel(c))
	}

	return out, nil
}

// channelInput is a channel form as submitted: an empty secret keeps the
// one stored. No form at all (a card's 测试连接) means the channel as stored.
type channelInput struct {
	Fields map[string]string `json:"fields"`
}

// submitted reads a channel form and merges it into the stored channel,
// returning the configuration it makes and the channel it opens.
func (w *web) submitted(r *http.Request, p store.Project) (config.Config, channel.Channel, error) {
	name := r.PathValue("name")

	t, ok := channelTypeOf(name)
	if !ok {
		return config.Config{}, nil, badFormf("unknown channel %q", name)
	}

	var in channelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return config.Config{}, nil, badForm(err)
	}

	chans, err := w.store.Channels(r.Context(), p.ID)
	if err != nil {
		return config.Config{}, nil, err
	}

	var stored json.RawMessage
	if i := slices.IndexFunc(chans, func(c store.Channel) bool { return c.Name == name }); i >= 0 {
		stored = chans[i].Config
	}

	raw, err := mergeForm(t, stored, in.Fields)
	if err != nil {
		return config.Config{}, nil, err
	}

	cfg, err := config.ChannelConfig(name, raw)
	if err != nil {
		return cfg, nil, badForm(err)
	}

	if cfg.GitHub != nil {
		if err := w.withGitHubToken(r.Context(), &cfg); err != nil {
			return cfg, nil, err
		}
	}

	opened, err := cfg.Open(quiet)
	if err != nil {
		return cfg, nil, badForm(err)
	}

	if len(opened) != 1 {
		return cfg, nil, fmt.Errorf("channel %s opened as %d channels", name, len(opened))
	}

	return cfg, opened[0], nil
}

// withGitHubToken fills in the token a GitHub channel's configuration names.
func (w *web) withGitHubToken(ctx context.Context, cfg *config.Config) error {
	token, err := config.GitHubToken(ctx, w.store, cfg.GitHubTokenName)
	if errors.Is(err, config.ErrNoGitHubToken) {
		return badForm(err)
	}

	if err != nil {
		return err
	}

	if token == "" {
		return badFormf("the gateway has no default GitHub token yet: add one in 共用设置")
	}

	cfg.GitHub.Token = token

	return nil
}

// storedFields is a stored channel configuration, by JSON key.
func storedFields(raw json.RawMessage) map[string]any {
	all := map[string]any{}

	_ = json.Unmarshal(raw, &all)

	return all
}

// mergeForm lays a submitted form over the stored configuration: an empty
// secret keeps the stored one, an empty plain field clears it, and keys the
// form does not have are kept as stored. A nil form changes nothing.
func mergeForm(t channelType, stored json.RawMessage, form map[string]string) (json.RawMessage, error) {
	merged := storedFields(stored)

	if form != nil {
		for _, f := range t.Fields {
			if v := strings.TrimSpace(form[f.Key]); v != "" || !f.Secret {
				merged[f.Key] = v
			}
		}
	}

	for _, f := range t.Fields {
		if s, _ := merged[f.Key].(string); f.Required && s == "" {
			return nil, badFormf("%s is required", f.Label)
		}
	}

	return json.Marshal(merged)
}

func (w *web) putChannel(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	cfg, _, err := w.submitted(r, p)
	if err != nil {
		return nil, err
	}

	rows, err := cfg.Channels()
	if err != nil {
		return nil, err
	}

	if err := w.store.PutChannel(r.Context(), p.ID, rows[0]); err != nil {
		return nil, err
	}

	rows[0].UpdatedAt = time.Now().UTC()

	return viewChannel(rows[0]), nil
}

// testResult is the outcome of a credentials check.
type testResult struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"` // who the credentials are
	Error    string `json:"error,omitempty"`
}

// testChannel checks the submitted credentials against the platform, the way
// init does, without connecting: a second connection to an app would take
// messages away from the one serving it. A form it cannot use is a failed
// test, not a failed request.
func (w *web) testChannel(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	_, ch, err := w.submitted(r, p)

	if form, ok := errors.AsType[*formError](err); ok {
		return testResult{Error: form.Error()}, nil
	}

	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(r.Context(), webTimeout)
	defer cancel()

	who, err := whoIs(ctx, ch)
	if err != nil {
		return testResult{Error: err.Error()}, nil
	}

	return testResult{OK: true, Identity: who}, nil
}

// whoIs asks the platform who a channel's credentials belong to.
func whoIs(ctx context.Context, ch channel.Channel) (string, error) {
	if id, ok := ch.(channel.Identifier); ok {
		return id.Identity(ctx)
	}

	return "", fmt.Errorf("cannot check channel %s", ch.Name())
}

func (w *web) deleteChannel(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	return nil, w.store.RemoveChannel(r.Context(), p.ID, r.PathValue("name"))
}

func (w *web) deleteProject(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	path, err := config.Unregister(r.Context(), w.store, p)
	if err != nil {
		return nil, err
	}

	return map[string]string{"config": path}, nil
}
