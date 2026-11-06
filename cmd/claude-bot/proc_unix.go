//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// lockFile takes an exclusive, non-blocking flock; errLocked says another
// process holds it. The kernel releases it if the process dies, so a crashed
// listener never leaves a stale lock.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, privatePerm) //nolint:gosec // fixed file in the bot dir
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("lock %s: %w", path, errLocked)
		}

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

// terminate asks the process pid to shut down, as Ctrl-C would.
func terminate(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}

	return nil
}
