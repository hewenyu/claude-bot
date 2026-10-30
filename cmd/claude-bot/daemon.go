package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Background processes (a project's listener, the gateway) hold a lock file
// while they run, with their pid in it, and log to a file. These helpers
// start, find and stop them.

// lockHolder returns the pid of the process holding lock, 0 if none does.
// legacy, if the lock holds no pid, names it some other way.
func lockHolder(lock string, legacy func() int) (int, error) {
	if _, err := os.Stat(lock); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}

	unlock, err := lockFile(lock)
	if err == nil {
		unlock()

		return 0, nil
	}

	raw, _ := os.ReadFile(lock) //nolint:gosec // fixed file in a bot dir
	if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
		return pid, nil
	}

	if pid := legacy(); pid != 0 {
		return pid, nil
	}

	return 0, fmt.Errorf("a process holds %s but its pid is unknown", lock)
}

// stopHolder shuts down pid, which holds lock, and waits until it has
// released the lock.
func stopHolder(ctx context.Context, lock string, pid int, what string) error {
	if err := terminate(pid); err != nil {
		return err
	}

	deadline := time.NewTimer(listenerShutdown)
	defer deadline.Stop()

	tick := time.NewTicker(startupPoll)
	defer tick.Stop()

	for {
		if unlock, err := lockFile(lock); err == nil {
			unlock()

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%s (pid %d) did not stop within %s", what, pid, listenerShutdown)
		case <-tick.C:
		}
	}
}

// spawn starts this binary with args in dir, detached from this process,
// with output appended to logPath.
func spawn(dir, logPath string, args ...string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate own binary: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, privatePerm) //nolint:gosec // fixed file in a bot dir
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close() //nolint:errcheck // the child holds its own descriptor

	cmd := exec.Command(exe, args...) //nolint:gosec,noctx // re-executes this binary; must outlive ctx
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", strings.Join(args, " "), err)
	}

	return cmd, nil
}

// waitStarted waits until up reports the spawned cmd ready, failing if it
// exits first or takes longer than listenerStartup; notReady starts the
// error then.
func waitStarted(ctx context.Context, cmd *exec.Cmd, logPath, notReady string, up func() bool) error {
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(listenerStartup)
	defer deadline.Stop()

	tick := time.NewTicker(startupPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("exited (%w):\n%s", err, tailFile(logPath, logTailLines))
		case <-deadline.C:
			return fmt.Errorf("%s within %s; recent log (%s):\n%s", notReady, listenerStartup, logPath, tailFile(logPath, logTailLines))
		case <-tick.C:
			if up() {
				return nil
			}
		}
	}
}
