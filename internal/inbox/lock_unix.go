//go:build unix

package inbox

import (
	"fmt"
	"os"
	"syscall"
)

// lockPath takes an exclusive flock on path, waiting for it. The kernel
// releases it if the process dies.
func lockPath(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, messagePerm) //nolint:gosec // fixed file in the inbox
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
