// Package atomicfile writes files so that readers see either the old or the
// new content, never a partial write.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces path with data. The file ends up with perm even if it
// already existed with wider permissions.
func Write(path string, data []byte, perm fs.FileMode) error {
	name := filepath.Base(path)

	tmp, err := WriteTemp(filepath.Dir(path), "."+name+".*", data)
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
// os.CreateTemp and readable only by the owner, and returns its path.
func WriteTemp(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = os.Remove(f.Name())

		return "", fmt.Errorf("write %s: %w", f.Name(), err)
	}

	return f.Name(), nil
}
