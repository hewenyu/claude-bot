//go:build unix

package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// listenerHelperEnv makes the test binary act as a listener: it holds the
// lock named by the variable until SIGTERM.
const listenerHelperEnv = "CLAUDE_BOT_TEST_LISTENER"

func TestMain(m *testing.M) {
	if lock := os.Getenv(listenerHelperEnv); lock != "" {
		os.Exit(fakeListener(lock))
	}

	os.Exit(m.Run())
}

func fakeListener(lock string) int {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)

	unlock, err := lockFile(lock)
	if err != nil {
		return 1
	}
	defer unlock()

	os.Stdout.WriteString("ready\n") //nolint:errcheck // the test waits for it
	<-term

	return 0
}

func TestKeepListener(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := assemble(dir, slog.New(slog.DiscardHandler), &fakeChannel{})

	cmd := exec.Command(os.Args[0]) //nolint:noctx // the test binary itself, killed in Cleanup

	cmd.Env = append(os.Environ(), listenerHelperEnv+"="+a.path(listenerLock))

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

	beat := func(channels ...string) {
		t.Helper()

		hb := heartbeat{PID: cmd.Process.Pid, BeatAt: time.Now().UTC(), Channels: channels}
		if err := writeJSON(a.path(heartbeatFile), hb); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()

	beat("fake")

	if running, err := a.keepListener(ctx); !running || err != nil {
		t.Fatalf("same channels: running=%v err=%v, want kept", running, err)
	}

	beat("lark") // started before "fake" was configured

	if running, err := a.keepListener(ctx); running || err != nil {
		t.Fatalf("changed channels: running=%v err=%v, want stopped", running, err)
	}

	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("old listener: %v, want a clean exit on SIGTERM", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old listener still running")
	}
}
