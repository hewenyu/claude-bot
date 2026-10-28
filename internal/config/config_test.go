package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/lark"
)

func TestSaveLoad(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")

	dir := filepath.Join(t.TempDir(), DirName)
	want := Config{Lark: &lark.Config{AppID: "cli_1", AppSecret: "s", Domain: "lark"}}

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
}

func TestLoad_envOverrides(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_env")
	t.Setenv("LARK_APP_SECRET", "env_secret")
	t.Setenv("LARK_DOMAIN", "")

	got, err := Load(t.TempDir()) // no config.json
	if err != nil {
		t.Fatal(err)
	}

	if got.Lark == nil || got.Lark.AppID != "cli_env" || got.Lark.BaseURL() != lark.FeishuURL {
		t.Errorf("Load() = %+v", got.Lark)
	}
}

func TestLoad_missing(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")

	got, err := Load(t.TempDir())
	if err != nil || got.Lark != nil {
		t.Errorf("Load() on empty dir = %+v, %v", got, err)
	}
}

func TestDir_explicitProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv(ProjectEnv, root)

	got, err := Dir(t.Context())
	if err != nil || got != filepath.Join(root, DirName) {
		t.Errorf("Dir() = %q, %v; want %q", got, err, filepath.Join(root, DirName))
	}
}

func TestDir_explicitSubdirResolvesToRepoRoot(t *testing.T) {
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

	t.Setenv(ProjectEnv, sub)

	got, err := Dir(t.Context())
	if err != nil || got != filepath.Join(repo, DirName) {
		t.Errorf("Dir() = %q, %v; want repo root %q", got, err, filepath.Join(repo, DirName))
	}
}

func TestDir_explicitMissing(t *testing.T) {
	t.Setenv(ProjectEnv, filepath.Join(t.TempDir(), "nope"))

	if _, err := Dir(t.Context()); err == nil {
		t.Error("Dir() with a missing -C directory should fail")
	}
}
