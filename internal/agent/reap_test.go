package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// strayAgent leaves what Claude Code's shells leave: a shell in a session
// of its own (as Claude Code runs them) puts a command in the background
// and ends, and launchd or init adopts the command; and a process of a
// session of its own holds the terminal. It exits soon, or with "stay"
// runs until it is killed.
const strayAgent = `#!/bin/sh
dir="$(dirname "$0")"
perl -e 'use POSIX; POSIX::setsid(); exec "/bin/sh", "-c", "sleep 300 </dev/null >/dev/null 2>&1 & echo \$! > $ARGV[0]/orphan; sleep 1"' "$dir" </dev/null >/dev/null 2>&1 &
perl -e 'use POSIX; POSIX::setsid(); sleep 300' &
echo $! > "$dir/holder"
sleep 1.5
printf 'ready\r\n'
for last; do :; done
if [ "$last" = stay ]; then
  while :; do sleep 1; done
fi
`

func TestWhatAnAgentLeavesEndsWithIt(t *testing.T) {
	needReap(t)
	t.Parallel()

	for _, stay := range []bool{false, true} {
		t.Run("stay="+strconv.FormatBool(stay), func(t *testing.T) {
			t.Parallel()
			strayRound(t, stay)
		})
	}
}

// strayRound runs strayAgent, which exits by itself or, with stay, is
// killed, and checks that nothing it left outlives it.
func strayRound(t *testing.T, stay bool) {
	t.Helper()

	dir := t.TempDir()
	prog := filepath.Join(dir, "stray-agent")

	if err := os.WriteFile(prog, []byte(strayAgent), 0o755); err != nil {
		t.Fatal(err)
	}

	prompt := ""
	if stay {
		prompt = "stay"
	}

	m := NewManager(Config{Program: prog}, nil)

	info, err := m.Spawn(SpawnOptions{Cwd: dir, Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}

	a, _ := m.Get(info.ID)
	pids := []int{readPID(t, filepath.Join(dir, "orphan")), readPID(t, filepath.Join(dir, "holder"))}

	// The family is looked at every familyPoll; an agent lives longer
	// than that.
	waitFamily(t, a, pids...)

	if stay {
		if err := a.Kill(); err != nil {
			t.Fatal(err)
		}
	}

	expectEnded(t, m, a, pids)
}

// expectEnded checks that the agent ended, with all of pids, and forgets
// it.
func expectEnded(t *testing.T, m *Manager, a *Agent, pids []int) {
	t.Helper()

	select {
	case <-a.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not end: what it left holds its terminal")
	}

	info := a.Info()
	if got := info; got.Status != Exited || got.ExitCode == nil || len(got.Procs) != 0 {
		t.Fatalf("after exit: %+v", got)
	}

	for _, pid := range pids {
		waitGone(t, pid)
	}

	if err := m.Remove(info.ID); err != nil {
		t.Fatal(err)
	}

	if len(m.List()) != 0 {
		t.Fatal("removed agent still listed")
	}
}

// TestHelperSleeper is no test: started with helperSleep set, the test
// binary sleeps, a program whose environment even macOS shows.
func TestHelperSleeper(t *testing.T) {
	t.Parallel()

	if os.Getenv(helperSleep) == "" {
		t.Skip("a helper")
	}

	time.Sleep(5 * time.Minute)
}

const helperSleep = "AGENT_TEST_SLEEP"

func TestReapAllEndsWhatAnEarlierRunLeft(t *testing.T) {
	needReap(t)
	t.Parallel()

	start := func(env ...string) *exec.Cmd {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperSleeper$")

		cmd.Env = append(os.Environ(), append(env, helperSleep+"=1")...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		go func() { _ = cmd.Wait() }()

		t.Cleanup(func() { _ = cmd.Process.Kill() })

		return cmd
	}

	// One carries the id of an agent of the earlier run; one was in an
	// agent's family when it was saved.
	marked := start(EnvAgentID + "=a-left")
	saved := start()
	stranger := start(EnvAgentID + "=a-other")

	var savedProc Proc

	for deadline := time.Now().Add(5 * time.Second); ; {
		// Until it execs, a child has the test's environment.
		found := 0

		for _, p := range listProcs() {
			switch {
			case p.PID == marked.Process.Pid && agentOf(procEnviron(p.PID)) == "a-left":
				found++
			case p.PID == saved.Process.Pid:
				savedProc = Proc{PID: p.PID, Start: p.Start}
				found++
			}
		}

		if found == 2 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("the processes never showed up")
		}

		time.Sleep(20 * time.Millisecond)
	}

	// A pid used again since is not taken for the saved one.
	if n := ReapAll([]Info{{ID: "a-gone", Procs: []Proc{{PID: savedProc.PID, Start: savedProc.Start + 1}}}}); n != 0 {
		t.Fatalf("reaped %d for a pid used again", n)
	}

	if n := ReapAll([]Info{{ID: "a-left"}, {ID: "a-gone", Procs: []Proc{savedProc}}}); n != 2 {
		t.Fatalf("reaped %d, want 2", n)
	}

	waitGone(t, marked.Process.Pid)
	waitGone(t, saved.Process.Pid)

	if err := syscall.Kill(stranger.Process.Pid, 0); err != nil {
		t.Fatalf("another agent's process was ended: %v", err)
	}
}

func TestFamily(t *testing.T) {
	t.Parallel()

	procs := []proc{
		{PID: 50, PPID: 1, PGID: 50, SID: 50, Start: 1},       // the owner's world
		{PID: 100, PPID: 50, PGID: 100, SID: 100, Start: 10},  // the program
		{PID: 101, PPID: 100, PGID: 101, SID: 101, Start: 11}, // a shell, its own session
		{PID: 102, PPID: 101, PGID: 101, SID: 101, Start: 12}, // what it put in the background
		{PID: 200, PPID: 1, PGID: 200, SID: 200, Start: 13},   // a stranger
		{PID: 300, PPID: 1, PGID: 300, SID: 300, Start: 14},   // marked, adopted
	}
	env := map[int]string{300: EnvAgentID + "=a1", 200: EnvAgentID + "=a2"}
	reads := 0

	f := newFamily("a1", 100)
	f.list = func() []proc { return procs }
	f.environ = func(pid int) []byte { reads++; return []byte(env[pid]) }

	if !f.scan() || !slices.Equal(pids(f.procs()), []int{100, 101, 102, 300}) {
		t.Fatalf("family %v", f.procs())
	}

	// The shell ended; launchd adopted what it left, still in its session.
	procs = []proc{
		procs[0], procs[1],
		{PID: 102, PPID: 1, PGID: 101, SID: 101, Start: 12},
		procs[4], procs[5],
		{PID: 103, PPID: 1, PGID: 101, SID: 101, Start: 15}, // joined the session later
	}
	readsBefore := reads

	f.scan()

	if got := pids(f.procs()); !slices.Equal(got, []int{100, 102, 103, 300}) {
		t.Fatalf("family %v", got)
	}

	if reads != readsBefore {
		t.Fatalf("read environments again: %d", reads-readsBefore)
	}

	// Its session's number used again by a stranger who leads it.
	procs = []proc{procs[0], procs[1], {PID: 101, PPID: 50, PGID: 101, SID: 101, Start: 20}, {PID: 104, PPID: 101, PGID: 101, SID: 101, Start: 21}}

	f.scan()

	if got := pids(f.procs()); !slices.Equal(got, []int{100}) {
		t.Fatalf("family %v", got)
	}
}

// waitFamily waits until the agent's family holds pids.
func waitFamily(t *testing.T, a *Agent, want ...int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		got := pids(a.Info().Procs)
		missing := false

		for _, pid := range want {
			if !slices.Contains(got, pid) {
				missing = true
			}
		}

		if !missing {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("family %v lacks some of %v", got, want)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func pids(procs []Proc) []int {
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		out = append(out, p.PID)
	}

	return out
}

func TestAgentOf(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"\x00\x00\x00\x01/bin/sh\x00sh\x00HOME=/h\x00" + EnvAgentID + "=a1\x00": "a1",
		EnvAgentID + "=a2":                      "a2",
		"HOME=/h\x00X" + EnvAgentID + "=a3\x00": "",
		"HOME=/h\x00":                           "",
	}

	for env, want := range tests {
		if got := agentOf([]byte(env)); got != want {
			t.Errorf("agentOf(%q) = %q, want %q", env, got, want)
		}
	}
}

// needReap skips where reap cannot find processes.
func needReap(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reap finds no processes on " + runtime.GOOS)
	}

	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("no perl to start a process in a session of its own")
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("no pid in %s", path)

	return 0
}

// waitGone waits for a process to end. An ended process some parent did
// not wait for yet is gone all the same.
func waitGone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) || zombie(pid) {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("process %d still runs", pid)
}

func zombie(pid int) bool {
	out, err := exec.CommandContext(context.Background(), "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()

	return err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
