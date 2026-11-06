//go:build unix

// Package filelock takes exclusive locks on files that the kernel releases
// when the process dies, so a crash never leaves a stale lock. claude-bot
// runs on macOS and Linux only.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrLocked reports a lock another process holds, from TryLock.
var ErrLocked = errors.New("held by another process")

// Lock takes an exclusive lock on path, creating it with perm, and waits
// for it. Call the returned func to release it.
func Lock(path string, perm os.FileMode) (func(), error) {
	return lock(path, perm, syscall.LOCK_EX)
}

// TryLock is Lock without waiting: it fails with ErrLocked when another
// process holds the lock.
func TryLock(path string, perm os.FileMode) (func(), error) {
	return lock(path, perm, syscall.LOCK_EX|syscall.LOCK_NB)
}

func lock(path string, perm os.FileMode, how int) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, perm) //nolint:gosec // the caller's own lock file
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("lock %s: %w", path, ErrLocked)
		}

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
