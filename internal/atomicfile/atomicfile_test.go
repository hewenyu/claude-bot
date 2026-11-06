package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_replacesContentAndTightensMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("content = %q, %v; want new", got, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("dir holds %d entries, want only the target (no temp files left)", len(entries))
	}
}

func TestWriteJSON_indentsAndEndsWithNewline(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v.json")

	if err := WriteJSON(path, map[string]int{"a": 1}, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("content = %q, %v", got, err)
	}
}

func TestReadJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v.json")

	var v map[string]int
	if err := ReadJSON(path, &v); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadJSON(missing) = %v, want fs.ErrNotExist", err)
	}

	if err := WriteJSONNoSync(path, map[string]int{"a": 1}, PrivateFile); err != nil {
		t.Fatal(err)
	}

	if err := ReadJSON(path, &v); err != nil || v["a"] != 1 {
		t.Errorf("ReadJSON = %v, %v", v, err)
	}

	if err := os.WriteFile(path, []byte("{"), PrivateFile); err != nil {
		t.Fatal(err)
	}

	if err := ReadJSON(path, &v); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadJSON(broken) = %v, want a parse error", err)
	}
}
