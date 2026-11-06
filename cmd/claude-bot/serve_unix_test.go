//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCheckListener(t *testing.T) {
	t.Parallel()

	a := assemble(t.TempDir(), slog.New(slog.DiscardHandler), &fakeChannel{})

	if _, err := a.checkListener(); !errors.Is(err, errListenerDown) {
		t.Fatalf("checkListener() with no listener = %v, want errListenerDown", err)
	}

	listener, _ := startFakeListener(t, a.path(listenerLock))

	// Holding the lock without a heartbeat: still connecting.
	if _, err := a.checkListener(); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("checkListener() while connecting = %v, want not connected", err)
	}

	// An outdated listener still serves; inbox only warns.
	hb := heartbeat{PID: listener, BeatAt: time.Now().UTC(), Version: "v0.0.1", Channels: []string{"lark"}}
	if err := writeBeat(a.path(heartbeatFile), hb); err != nil {
		t.Fatal(err)
	}

	warnings, err := a.checkListener()
	if err != nil {
		t.Errorf("checkListener() with an outdated listener = %v, want nil", err)
	}

	if len(warnings) != 2 || !strings.Contains(warnings[0], "lark but fake") || !strings.Contains(warnings[1], `"v0.0.1"`) {
		t.Errorf("checkListener() warnings = %q, want the channels and the version", warnings)
	}
}

func TestStopListener(t *testing.T) {
	t.Parallel()

	a := assemble(t.TempDir(), slog.New(slog.DiscardHandler), &fakeChannel{})

	if pid, err := a.listenerPID(); pid != 0 || err != nil {
		t.Fatalf("listenerPID() with no listener = %d, %v; want 0", pid, err)
	}

	listener, exited := startFakeListener(t, a.path(listenerLock))

	pid, err := a.listenerPID()
	if pid != listener || err != nil {
		t.Fatalf("listenerPID() = %d, %v; want %d", pid, err, listener)
	}

	if err := a.stopListener(context.Background(), pid); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("listener: %v, want a clean exit on SIGTERM", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listener still running")
	}

	if pid, err := a.listenerPID(); pid != 0 || err != nil {
		t.Errorf("listenerPID() after stop = %d, %v; want 0", pid, err)
	}
}

// startFakeListener runs the test binary as a listener holding lock (or
// several, as a path list) and returns its pid, and a channel that yields
// its exit.
func startFakeListener(t *testing.T, lock string) (int, <-chan error) {
	t.Helper()

	cmd := exec.Command(os.Args[0]) //nolint:noctx // the test binary itself, killed in Cleanup

	cmd.Env = append(os.Environ(), listenerHelperEnv+"="+lock)

	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cmd.Process.Kill() })

	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		t.Fatalf("helper: %q", line)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	return cmd.Process.Pid, exited
}
