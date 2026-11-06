package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
)

// every sets up every kind of channel.
var every = Config{
	Lark:     &lark.Config{AppID: "cli_a1", AppSecret: "s"},
	Discord:  &discord.Config{Token: "MTIzNDU2.Gx.secret"},
	Telegram: &telegram.Config{Token: "123456:AAE-secret"},
	Slack:    &slack.Config{AppToken: "xapp-1-A0123-456-secret", BotToken: "xoxb-1"},
	GitHub:   &github.Config{Token: "ghp_secret", Repo: "Hewenyu/Claude-Bot"},
	DingTalk: &dingtalk.Config{ClientID: "dingabc", ClientSecret: "s"},
}

func TestKinds_channelsAndAppKeys(t *testing.T) {
	t.Parallel()

	chans, err := every.Channels()
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, c := range chans {
		got[c.Name] = c.AppKey
	}

	want := map[string]string{
		lark.Name: "lark:cli_a1", discord.Name: "discord:MTIzNDU2", telegram.Name: "telegram:123456",
		slack.Name: "slack:A0123", github.Name: "github:hewenyu/claude-bot", dingtalk.Name: "dingtalk:dingabc",
	}
	if len(got) != len(want) {
		t.Fatalf("Channels() = %v, want %v", got, want)
	}

	for name, key := range want {
		if got[name] != key {
			t.Errorf("app key of %s = %q, want %q", name, got[name], key)
		}
	}
}

// Stored and read back, every channel is as it was, but for the GitHub
// token, which the gateway keeps apart.
func TestKinds_decode(t *testing.T) {
	t.Parallel()

	chans, err := every.Channels()
	if err != nil {
		t.Fatal(err)
	}

	var back Config

	for _, c := range chans {
		if strings.Contains(string(c.Config), "ghp_secret") {
			t.Errorf("channel %s stores the GitHub token: %s", c.Name, c.Config)
		}

		if err := back.decodeChannel(c.Name, c.Config); err != nil {
			t.Fatal(err)
		}
	}

	for _, k := range Kinds() {
		if !back.Has(k.Name) {
			t.Errorf("%s lost on the way", k.Name)
		}
	}

	if *back.Lark != *every.Lark || back.GitHub.Repo != every.GitHub.Repo || back.GitHub.Token != "" {
		t.Errorf("read back %+v, %+v", back.Lark, back.GitHub)
	}

	if err := back.decodeChannel("irc", nil); err == nil {
		t.Error("an unknown channel decoded")
	}
}

func TestKinds_open(t *testing.T) {
	t.Parallel()

	chs, err := every.Open(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, len(chs))
	for i, ch := range chs {
		names[i] = ch.Name()
	}

	if want := strings.Split(KindNames(","), ","); !slices.Equal(names, want) {
		t.Errorf("Open() = %v, want %v", names, want)
	}

	if chs, err := (Config{Telegram: &telegram.Config{}}).Open(slog.New(slog.DiscardHandler)); err == nil || len(chs) != 0 {
		t.Errorf("Open(bad telegram) = %v, %v; want an error", chs, err)
	}
}

func TestErrNotConfigured_namesEveryVariable(t *testing.T) {
	t.Parallel()

	for _, k := range Kinds() {
		for _, v := range append(slices.Clone(k.Env), k.OptionalEnv...) {
			if !strings.Contains(ErrNotConfigured.Error(), v) {
				t.Errorf("ErrNotConfigured does not name %s: %v", v, ErrNotConfigured)
			}
		}
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()

	if k, ok := KindOf(dingtalk.Name); !ok || k.Label == "" || len(k.Env) != 2 {
		t.Errorf("KindOf(dingtalk) = %+v, %v", k, ok)
	}

	if _, ok := KindOf("irc"); ok {
		t.Error("KindOf(irc) found a kind")
	}

	if (Config{}).HasAny() || !(Config{GitHub: &github.Config{}}).HasAny() {
		t.Error("HasAny is wrong")
	}
}

func TestUnregister(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	api := newProjectDir(t, "api")
	p := must(Save(ctx, api, Config{Lark: &lark.Config{AppID: "cli_api", AppSecret: "s"}}))(t)

	s := must(OpenStore(ctx))(t)
	defer s.Close() //nolint:errcheck // test

	path, err := Unregister(ctx, s, p)
	expect(t, err == nil && path == FilePath(api), "Unregister = %q, %v", path, err)

	cfg := must(ReadFile(api))(t)
	expect(t, cfg.Lark != nil && cfg.Lark.AppID == "cli_api", "config.json after Unregister = %+v", cfg)

	// A directory that is gone has nothing to write back to.
	gone := newProjectDir(t, "gone")
	p = must(Save(ctx, gone, Config{Lark: &lark.Config{AppID: "cli_gone", AppSecret: "s"}}))(t)

	if err := os.RemoveAll(filepath.Dir(gone)); err != nil {
		t.Fatal(err)
	}

	path, err = Unregister(ctx, s, p)
	expect(t, err == nil && path == "", "Unregister(gone) = %q, %v", path, err)

	// One that cannot be looked at is not gone: the project stays.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	p = must(s.SaveProject(ctx, filepath.Join(file, "repo"), nil))(t)

	_, err = Unregister(ctx, s, p)
	expect(t, err != nil, "Unregister under a file succeeded")

	_, err = s.Project(ctx, p.ID)
	expect(t, err == nil, "project removed after a failed Unregister: %v", err)
}

func TestUseGitHubToken(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	api := newProjectDir(t, "api")
	p := must(Save(ctx, api, Config{GitHub: &github.Config{Token: "ghp_default", Repo: "o/api"}}))(t)

	s := must(OpenStore(ctx))(t)
	defer s.Close() //nolint:errcheck // test

	if err := SetGitHubToken(ctx, s, "work", "ghp_work"); err != nil {
		t.Fatal(err)
	}

	if err := UseGitHubToken(ctx, s, p.ID, "work"); err != nil {
		t.Fatal(err)
	}

	cfg := must(FromStore(ctx, s, p))(t)
	expect(t, cfg.GitHubTokenName == "work" && cfg.GitHub.Token == "ghp_work", "after UseGitHubToken: %+v, %q", cfg.GitHub, cfg.GitHubTokenName)

	if err := DeleteGitHubToken(ctx, s, "work"); err != nil {
		t.Fatal(err)
	}

	_, err := GitHubToken(ctx, s, "work")
	expect(t, errors.Is(err, ErrNoGitHubToken), "deleted token: %v", err)

	expect(t, SetGitHubToken(ctx, s, "", "") != nil, "SetGitHubToken saved an empty token")
}
