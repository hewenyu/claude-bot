package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
)

func TestSaveLoad(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")

	dir := filepath.Join(t.TempDir(), DirName)
	want := Config{
		Lark:    &lark.Config{AppID: "cli_1", AppSecret: "s", Domain: "lark"},
		Discord: &discord.Config{Token: "t", Proxy: "direct"},
	}

	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.json mode = %o, want 600 (it holds the app secret)", perm)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got.Lark == nil || *got.Lark != *want.Lark {
		t.Errorf("Load() = %+v, want %+v", got.Lark, want.Lark)
	}

	if got.Discord == nil || *got.Discord != *want.Discord {
		t.Errorf("Load() = %+v, want %+v", got.Discord, want.Discord)
	}
}

func TestLoad_envOverrides(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_env")
	t.Setenv("LARK_APP_SECRET", "env_secret")
	t.Setenv("LARK_DOMAIN", "")
	t.Setenv("DISCORD_BOT_TOKEN", "env_token")

	got, err := Load(t.TempDir()) // no config.json
	if err != nil {
		t.Fatal(err)
	}

	if got.Lark == nil || got.Lark.AppID != "cli_env" || got.Lark.BaseURL() != lark.FeishuURL {
		t.Errorf("Load() = %+v", got.Lark)
	}

	if got.Discord == nil || got.Discord.Token != "env_token" {
		t.Errorf("Load() = %+v", got.Discord)
	}
}

func TestLoad_missing(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")

	got, err := Load(t.TempDir())
	if err != nil || got.Lark != nil || got.Discord != nil {
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
