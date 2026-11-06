package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()

	home := filepath.Join(t.TempDir(), "home")

	s, err := Open(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s, home
}

func TestOpen_private(t *testing.T) {
	t.Parallel()

	s, home := openTemp(t)

	// A write creates the WAL, which holds secrets too.
	if err := s.SetSetting(t.Context(), SettingTypeSafeKey, "k"); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{home: atomicfile.PrivateDir, dbPath(home): atomicfile.PrivateFile, dbPath(home) + "-wal": atomicfile.PrivateFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode of %s = %o, want %o", filepath.Base(path), got, want)
		}
	}
}

func TestOpenExisting(t *testing.T) {
	t.Parallel()

	home := t.TempDir()

	if _, ok, err := OpenExisting(t.Context(), home); ok || err != nil {
		t.Fatalf("OpenExisting() without a database = %v, %v; want not ok, nil", ok, err)
	}

	if _, err := os.Stat(dbPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OpenExisting created %s", dbPath(home))
	}

	s, err := Open(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	s, ok, err := OpenExisting(t.Context(), home)
	if !ok || err != nil {
		t.Fatalf("OpenExisting() = %v, %v; want ok", ok, err)
	}

	_ = s.Close()
}

func TestSettings(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := t.Context()

	if v, err := s.Setting(ctx, SettingGitHubToken); v != "" || err != nil {
		t.Fatalf("unset setting = %q, %v", v, err)
	}

	rev0, _ := s.Revision(ctx)

	if err := s.SetSetting(ctx, SettingGitHubToken, "t1"); err != nil {
		t.Fatal(err)
	}

	rev1, _ := s.Revision(ctx)
	if rev1 == rev0 {
		t.Error("revision did not change on write")
	}

	// Writing the same value is not a change.
	if err := s.SetSetting(ctx, SettingGitHubToken, "t1"); err != nil {
		t.Fatal(err)
	}

	if rev, _ := s.Revision(ctx); rev != rev1 {
		t.Errorf("revision changed on a no-op write: %d -> %d", rev1, rev)
	}

	if v, _ := s.Setting(ctx, SettingGitHubToken); v != "t1" {
		t.Errorf("setting = %q, want t1", v)
	}

	if err := s.SetSetting(ctx, SettingGitHubToken, ""); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.Setting(ctx, SettingGitHubToken); v != "" {
		t.Errorf("removed setting = %q", v)
	}
}

func TestSettingsWithPrefix(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := t.Context()

	for k, v := range map[string]string{
		SettingGitHubToken:                     "default",
		SettingGitHubTokenPrefix + "work":      "w",
		SettingGitHubTokenPrefix + "oss":       "o",
		"github_tokenx":                        "not one",
		SettingGitHubTokenPrefix + "100%_like": "p",
	} {
		if err := s.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.SettingsWithPrefix(ctx, SettingGitHubTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 || got["work"] != "w" || got["oss"] != "o" || got["100%_like"] != "p" {
		t.Errorf("SettingsWithPrefix = %v", got)
	}
}

func TestSaveProject_names(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := t.Context()

	a, err := s.SaveProject(ctx, "/src/api", nil)
	if err != nil {
		t.Fatal(err)
	}

	again, err := s.SaveProject(ctx, "/src/api", nil)
	if err != nil || again.ID != a.ID {
		t.Fatalf("registering again = %+v, %v; want project %d", again, err, a.ID)
	}

	b, err := s.SaveProject(ctx, "/other/api", nil)
	if err != nil {
		t.Fatal(err)
	}

	if a.Name != "api" || b.Name != "api-2" {
		t.Errorf("names = %q, %q; want api, api-2", a.Name, b.Name)
	}

	if p, err := s.ProjectByDir(ctx, "/other/api"); err != nil || p.ID != b.ID {
		t.Errorf("ProjectByDir = %+v, %v", p, err)
	}

	if _, err := s.ProjectByDir(ctx, "/nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ProjectByDir(unknown) = %v, want ErrNotFound", err)
	}

	ps, err := s.Projects(ctx)
	if err != nil || len(ps) != 2 {
		t.Fatalf("Projects() = %v, %v", ps, err)
	}
}

func ch(name, key, cfg string) Channel {
	return Channel{Name: name, AppKey: key, Config: json.RawMessage(cfg)}
}

func TestChannels_oneAppOneProject(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := t.Context()

	a, _ := s.SaveProject(ctx, "/src/a", nil)
	b, _ := s.SaveProject(ctx, "/src/b", nil)

	if _, err := s.SaveProject(ctx, a.Dir, []Channel{ch("lark", "lark:cli_1", `{"app_id":"cli_1"}`), ch("github", "github:o/a", `{"repo":"o/a"}`)}); err != nil {
		t.Fatal(err)
	}

	err := s.PutChannel(ctx, b.ID, ch("lark", "lark:cli_1", `{"app_id":"cli_1"}`))

	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Project.ID != a.ID {
		t.Fatalf("second project on the same app = %v, want a ConflictError naming project a", err)
	}

	// Updating the owner's own channel is not a conflict.
	if err := s.PutChannel(ctx, a.ID, ch("lark", "lark:cli_1", `{"app_id":"cli_1","domain":"lark"}`)); err != nil {
		t.Fatalf("updating own channel: %v", err)
	}

	// A failed SaveProject changes nothing.
	if _, err := s.SaveProject(ctx, b.Dir, []Channel{ch("telegram", "telegram:1", `{}`), ch("lark", "lark:cli_1", `{}`)}); err == nil {
		t.Fatal("SaveProject with a taken app succeeded")
	}

	if got, _ := s.Channels(ctx, b.ID); len(got) != 0 {
		t.Errorf("project b has %d channels after a failed SaveProject, want 0", len(got))
	}

	// SaveProject drops channels not listed.
	if _, err := s.SaveProject(ctx, a.Dir, []Channel{ch("github", "github:o/a", `{"repo":"o/a"}`)}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Channels(ctx, a.ID)
	if len(got) != 1 || got[0].Name != "github" {
		t.Errorf("channels of a = %+v, want only github", got)
	}

	// Once a drops the app, b may take it; removing a project frees its apps.
	if err := s.PutChannel(ctx, b.ID, ch("lark", "lark:cli_1", `{}`)); err != nil {
		t.Errorf("taking a freed app: %v", err)
	}

	if err := s.RemoveProject(ctx, b.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.PutChannel(ctx, a.ID, ch("lark", "lark:cli_1", `{}`)); err != nil {
		t.Errorf("app of a removed project still taken: %v", err)
	}
}

// A project's runner setting goes with it: SQLite gives a later project
// the id of the last one removed.
func TestRemoveProject_forgetsItsRunner(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := t.Context()

	p, err := s.SaveProject(ctx, "/src/a", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetRunnerOn(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}

	if on, err := s.RunnersOn(ctx); err != nil || !on[p.ID] || len(on) != 1 {
		t.Fatalf("RunnersOn = %v, %v; want only %d", on, err, p.ID)
	}

	if err := s.RemoveProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	if on, _ := s.RunnersOn(ctx); on[p.ID] {
		t.Errorf("runner of a removed project still on")
	}
}

func TestMigrate_newerSchema(t *testing.T) {
	t.Parallel()

	s, home := openTemp(t)
	if _, err := s.db.ExecContext(t.Context(), `PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	if _, err := Open(t.Context(), home); err == nil {
		t.Error("opening a database from a newer version succeeded")
	}
}
