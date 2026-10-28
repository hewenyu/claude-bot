//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// lockFile takes an exclusive, non-blocking flock. The kernel releases it if
// the process dies, so a crashed listener never leaves a stale lock.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed file in the bot dir
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// detach starts cmd in its own session so it survives the calling shell.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
