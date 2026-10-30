package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/jev"
)

func TestWriteFileLoad(t *testing.T) {
	isolate(t)
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	t.Setenv("DINGTALK_CLIENT_ID", "")

	dir := filepath.Join(t.TempDir(), DirName)
	want := Config{
		Lark:     &lark.Config{AppID: "cli_1", AppSecret: "s", Domain: "lark"},
		Discord:  &discord.Config{Token: "t", Proxy: "direct"},
		Telegram: &telegram.Config{Token: "1:t"},
		Slack:    &slack.Config{AppToken: "xapp-1", BotToken: "xoxb-1", Proxy: "env"},
		TypeSafe: &jev.Config{APIKey: "ts-1"},
	}

	if err := WriteFile(dir, want); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.json mode = %o, want 600 (it holds the app secret)", perm)
	}

	got, err := Load(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if got.Lark == nil || *got.Lark != *want.Lark {
		t.Errorf("Load() = %+v, want %+v", got.Lark, want.Lark)
	}

	if got.Discord == nil || *got.Discord != *want.Discord {
		t.Errorf("Load() = %+v, want %+v", got.Discord, want.Discord)
	}

	if got.Telegram == nil || *got.Telegram != *want.Telegram {
		t.Errorf("Load() = %+v, want %+v", got.Telegram, want.Telegram)
	}

	if got.Slack == nil || *got.Slack != *want.Slack {
		t.Errorf("Load() = %+v, want %+v", got.Slack, want.Slack)
	}
}

func TestLoad_envOverrides(t *testing.T) {
	isolate(t)
	t.Setenv("LARK_APP_ID", "cli_env")
	t.Setenv("LARK_APP_SECRET", "env_secret")
	t.Setenv("LARK_DOMAIN", "")
	t.Setenv("DISCORD_BOT_TOKEN", "env_token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "1:env")
	t.Setenv("SLACK_APP_TOKEN", "xapp-env")
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-env")

	got, err := Load(t.Context(), t.TempDir()) // no config.json
	if err != nil {
		t.Fatal(err)
	}

	if got.Lark == nil || got.Lark.AppID != "cli_env" || got.Lark.BaseURL() != lark.FeishuURL {
		t.Errorf("Load() = %+v", got.Lark)
	}

	if got.Discord == nil || got.Discord.Token != "env_token" {
		t.Errorf("Load() = %+v", got.Discord)
	}

	if got.Telegram == nil || got.Telegram.Token != "1:env" {
		t.Errorf("Load() = %+v", got.Telegram)
	}

	if got.Slack == nil || got.Slack.AppToken != "xapp-env" || got.Slack.BotToken != "xoxb-env" {
		t.Errorf("Load() = %+v", got.Slack)
	}
}

func TestLoad_dingtalkEnv(t *testing.T) {
	isolate(t)
	t.Setenv("DINGTALK_CLIENT_ID", "ding-env")
	t.Setenv("DINGTALK_CLIENT_SECRET", "")

	if got, err := Load(t.Context(), t.TempDir()); err != nil || got.DingTalk != nil {
		t.Errorf("Load() with only the client id = %+v, %v; want no DingTalk", got.DingTalk, err)
	}

	t.Setenv("DINGTALK_CLIENT_SECRET", "ding-secret")

	got, err := Load(t.Context(), t.TempDir())
	if err != nil || got.DingTalk == nil || got.DingTalk.ClientID != "ding-env" || got.DingTalk.ClientSecret != "ding-secret" {
		t.Errorf("Load() = %+v, %v", got.DingTalk, err)
	}
}

func TestLoad_missing(t *testing.T) {
	isolate(t)
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	t.Setenv("DINGTALK_CLIENT_ID", "")

	got, err := Load(t.Context(), t.TempDir())
	if err != nil || got.Lark != nil || got.Discord != nil || got.Telegram != nil || got.Slack != nil {
		t.Errorf("Load() on empty dir = %+v, %v", got, err)
	}
}

func TestDir_explicitProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	got, err := Dir(t.Context(), root)
	if err != nil || got != filepath.Join(root, DirName) {
		t.Errorf("Dir() = %q, %v; want %q", got, err, filepath.Join(root, DirName))
	}
}

func TestDir_explicitSubdirResolvesToRepoRoot(t *testing.T) {
	t.Parallel()

	repo, err := filepath.EvalSymlinks(t.TempDir()) // macOS temp dirs are symlinked
	if err != nil {
		t.Fatal(err)
	}

	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := Dir(t.Context(), sub)
	if err != nil || got != filepath.Join(repo, DirName) {
		t.Errorf("Dir() = %q, %v; want repo root %q", got, err, filepath.Join(repo, DirName))
	}
}

func TestDir_explicitMissing(t *testing.T) {
	t.Parallel()

	if _, err := Dir(t.Context(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Dir() with a missing -C directory should fail")
	}
}

func TestLoad_typesafeKeyOnlyFromConfig(t *testing.T) {
	isolate(t)
	t.Setenv("TYPESAFE_API_KEY", "ts-env")

	got, err := Load(t.Context(), t.TempDir())
	if err != nil || got.TypeSafe != nil {
		t.Errorf("Load = %+v, %v; want no TypeSafe key from the environment", got.TypeSafe, err)
	}
}

func TestLoad_envKeepsSettings(t *testing.T) {
	isolate(t)
	t.Setenv("LARK_APP_ID", "cli_env")
	t.Setenv("LARK_APP_SECRET", "env_secret")
	t.Setenv("LARK_DOMAIN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "1:env")

	dir := t.TempDir()
	saved := Config{
		Lark:     &lark.Config{AppID: "cli_1", AppSecret: "s", Domain: "lark", Proxy: "env"},
		Telegram: &telegram.Config{Token: "1:file", Proxy: "http://127.0.0.1:7890"},
	}

	if err := WriteFile(dir, saved); err != nil {
		t.Fatal(err)
	}

	got, err := Load(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := (lark.Config{AppID: "cli_env", AppSecret: "env_secret", Domain: "lark", Proxy: "env"}); got.Lark == nil || *got.Lark != want {
		t.Errorf("Load().Lark = %+v, want %+v", got.Lark, want)
	}

	if want := (telegram.Config{Token: "1:env", Proxy: "http://127.0.0.1:7890"}); got.Telegram == nil || *got.Telegram != want {
		t.Errorf("Load().Telegram = %+v, want %+v", got.Telegram, want)
	}

	// init rewrites config.json from Read: the environment must not leak into it.
	file, err := Read(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if *file.Lark != *saved.Lark || *file.Telegram != *saved.Telegram {
		t.Errorf("Read() = %+v, %+v; want config.json as saved", file.Lark, file.Telegram)
	}
}

func TestHas(t *testing.T) {
	t.Parallel()

	cfg := Config{Slack: &slack.Config{}, DingTalk: &dingtalk.Config{}}

	for name, want := range map[string]bool{
		lark.Name: false, discord.Name: false, telegram.Name: false,
		slack.Name: true, github.Name: false, dingtalk.Name: true, "irc": false,
	} {
		if got := cfg.Has(name); got != want {
			t.Errorf("Has(%q) = %v, want %v", name, got, want)
		}
	}
}

// isolate points the gateway home at an empty directory, so that a test
// never reads the user's.
func isolate(t *testing.T) string {
	t.Helper()

	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(HomeEnv, home)

	return home
}
