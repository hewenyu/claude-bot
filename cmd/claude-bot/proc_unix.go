//go:build unix

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/hewenyu/claude-bot/internal/filelock"
)

// lockFile takes an exclusive, non-blocking lock; errLocked says another
// process holds it. The kernel releases it if the process dies, so a crashed
// listener never leaves a stale lock.
func lockFile(path string) (func(), error) {
	unlock, err := filelock.TryLock(path, privatePerm)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, fmt.Errorf("lock %s: %w", path, errLocked)
	}

	return unlock, err
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
