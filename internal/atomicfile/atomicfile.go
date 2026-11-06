// Package atomicfile writes files so that readers see either the old or the
// new content, never a partial write.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File modes claude-bot uses: its state stays private to the owner; what
// it writes into a repository for the team (.gitignore, the /bot command)
// is world-readable like the rest of the repository.
const (
	PrivateFile fs.FileMode = 0o600 // credentials and state
	PrivateDir  fs.FileMode = 0o700 // the gateway home
	ProjectDir  fs.FileMode = 0o750 // directories claude-bot makes in a repository
	SharedFile  fs.FileMode = 0o644 // files meant for the team
)

// Write replaces path with data. The file ends up with perm even if it
// already existed with wider permissions.
func Write(path string, data []byte, perm fs.FileMode) error {
	return write(path, data, perm, true)
}

// WriteJSON replaces path with v as indented JSON and a final newline.
func WriteJSON(path string, v any, perm fs.FileMode) error {
	return writeJSON(path, v, perm, true)
}

// WriteJSONNoSync is WriteJSON without flushing the data to disk first:
// for files rewritten all the time, such as heartbeats, whose content a
// crash may lose. Readers still never see a partial write.
func WriteJSONNoSync(path string, v any, perm fs.FileMode) error {
	return writeJSON(path, v, perm, false)
}

// ReadJSON decodes the JSON file at path into v. A missing file is an error
// that wraps fs.ErrNotExist, for callers to whom that is no error.
func ReadJSON(path string, v any) error {
	raw, err := os.ReadFile(path) //nolint:gosec // callers pass their own files
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	return nil
}

func writeJSON(path string, v any, perm fs.FileMode, sync bool) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}

	return write(path, append(raw, '\n'), perm, sync)
}

func write(path string, data []byte, perm fs.FileMode, sync bool) error {
	name := filepath.Base(path)

	tmp, err := writeTemp(filepath.Dir(path), "."+name+".*", data, sync)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) //nolint:errcheck // already renamed on success

	if err := os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}

	return nil
}

// WriteTemp writes data to a new file in dir, named after pattern as in
// os.CreateTemp and readable only by the owner, and returns its path. The
// data is on disk before it returns, so a rename after a crash never
// leaves an empty file.
func WriteTemp(dir, pattern string, data []byte) (string, error) {
	return writeTemp(dir, pattern, data, true)
}

func writeTemp(dir, pattern string, data []byte, sync bool) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	_, err = f.Write(data)
	if err == nil && sync {
		err = f.Sync()
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("write %s: %w", f.Name(), err)
	}

	return f.Name(), nil
}
