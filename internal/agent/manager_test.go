package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAgent acts like an interactive agent: it sets an idle title, echoes
// each line it reads while showing a busy title, and records its arguments.
const fakeAgent = `#!/bin/sh
printf '%s\n' "$@" > "$(dirname "$0")/args"
env > "$(dirname "$0")/env"
printf '\033[c\033]0;✳ Fake\007ready\r\n'
while IFS= read -r line; do
  printf '\033]0;◐ Fake\007got: %s\r\n' "$line"
  sleep 0.2
  printf '\033]0;✳ Fake\007'
done
`

func TestAgentInATerminal(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "fake-agent")

	if err := os.WriteFile(prog, []byte(fakeAgent), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent")
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")

	changes := make(chan Info, 100)
	m := NewManager(Config{Program: prog, HookCommand: "claude-bot hook", Allow: []string{"Bash(claude-bot reply:*)"}, Env: map[string]string{"PATH": "/opt/claude-bot", "CLAUDE_BOT_AGENT_URL": "x"}},
		func(i Info) { changes <- i })

	info, err := m.Spawn(SpawnOptions{Cwd: dir, Prompt: "-first task"})
	if err != nil {
		t.Fatal(err)
	}

	a, _ := m.Get(info.ID)
	waitStatus(t, a, Idle)

	expectFile(t, filepath.Join(dir, "args"), "--settings", `"PermissionRequest"`, `"command":"claude-bot hook"`,
		`"allow":["Bash(claude-bot reply:*)"]`, "--session-id", info.SessionID, " -first task")

	env := expectFile(t, filepath.Join(dir, "env"), "CLAUDE_BOT_AGENT_ID="+info.ID, "CLAUDE_BOT_AGENT_URL=x", "PATH=/opt/claude-bot:",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "TERM=xterm-256color")

	if strings.Contains(env, "CLAUDE_CODE_SESSION_ID") {
		t.Error("env has the parent session")
	}

	replay, out, detach := a.Attach()
	defer detach()

	if !bytes.Contains(replay, []byte("ready")) || bytes.Contains(replay, []byte("\x1b[c")) {
		t.Errorf("replay = %q", replay)
	}

	if err := a.Send("hello", false); err != nil {
		t.Fatal(err)
	}

	waitOutput(t, out, "got: \x1b[200~hello\x1b[201~")

	killAndRemove(t, m, a)

	if len(changes) == 0 {
		t.Fatal("no change reported")
	}
}

// expectFile checks that the file holds every one of wants, and returns it.
func expectFile(t *testing.T, path string, wants ...string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range wants {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q:\n%s", filepath.Base(path), want, b)
		}
	}

	return string(b)
}

// killAndRemove ends the agent, checks it ended, and forgets it.
func killAndRemove(t *testing.T, m *Manager, a *Agent) {
	t.Helper()

	if err := a.Kill(); err != nil {
		t.Fatal(err)
	}

	if got := a.Info(); got.Status != Exited || got.ExitCode == nil {
		t.Fatalf("after kill: %+v", got)
	}

	if err := a.Send("again", true); err == nil {
		t.Fatal("send to an ended agent worked")
	}

	if err := m.Remove(a.Info().ID); err != nil {
		t.Fatal(err)
	}
}

func TestRestoredAgentsAreLost(t *testing.T) {
	t.Parallel()

	m := NewManager(Config{}, nil)
	m.Restore([]Info{{ID: "a1", Status: Working, PID: 7}, {ID: "a2", Status: Exited}})

	got := m.List()
	if got[0].Status != Lost || got[0].PID != 0 || got[1].Status != Exited {
		t.Fatalf("restored %+v", got)
	}
}

func waitStatus(t *testing.T, a *Agent, want Status) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.Info().Status == want {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("status %s, want %s", a.Info().Status, want)
}

func waitOutput(t *testing.T, out <-chan []byte, want string) {
	t.Helper()

	var seen []byte

	timeout := time.After(5 * time.Second)

	for {
		select {
		case p, ok := <-out:
			if !ok {
				t.Fatalf("output ended before %q; saw %q", want, seen)
			}

			seen = append(seen, p...)
			if bytes.Contains(seen, []byte(want)) {
				return
			}
		case <-timeout:
			t.Fatalf("no %q in %q", want, seen)
		}
	}
}

func TestDialogBeforeTheSessionShows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prog := filepath.Join(dir, "dialog-agent")
	script := "#!/bin/sh\nprintf 'Yes,\\033[9GI\\033[11Gtrust\\033[17Gthis\\033[22Gfolder\\r\\nEnter\\033[8Gto\\033[11Gconfirm \\033[21GEsc\\033[25Gto\\033[28Gcancel\\r\\n'\nread x\n"

	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewManager(Config{Program: prog}, nil)

	info, err := m.Spawn(SpawnOptions{Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}

	a, _ := m.Get(info.ID)
	waitStatus(t, a, Blocked)

	if got := a.Info().Detail; got != "启动时需要确认: 信任这个目录" {
		t.Fatalf("detail = %q", got)
	}

	_ = a.Kill()
}

func TestPasteCannotEndEarly(t *testing.T) {
	t.Parallel()

	got := pasteSafe("hi\x1b[201~\r/exit\x1b[200~")
	if strings.Contains(got, "\x1b") {
		t.Fatalf("pasteSafe kept an escape: %q", got)
	}

	if got != "hi[201~\r/exit[200~" {
		t.Fatalf("pasteSafe = %q", got)
	}
}

func TestIDsAreUniqueWithinAMillisecond(t *testing.T) {
	t.Parallel()

	now := time.Now()
	seen := map[string]bool{}

	for range 1000 {
		id := newID(now)
		if seen[id] {
			t.Fatalf("id %s repeated", id)
		}

		seen[id] = true
	}
}

func TestSpawnInAMissingFolderFails(t *testing.T) {
	t.Parallel()

	m := NewManager(Config{Program: "true"}, nil)

	if _, err := m.Spawn(SpawnOptions{Cwd: filepath.Join(t.TempDir(), "gone")}); err == nil {
		t.Fatal("spawned in a missing folder")
	}

	if n := len(m.List()); n != 0 {
		t.Fatalf("%d agents kept after a failed spawn", n)
	}
}

func TestSpawnOfAMissingProgramKeepsNoAgent(t *testing.T) {
	t.Parallel()

	m := NewManager(Config{Program: "claude-bot-no-such-program"}, nil)

	if _, err := m.Spawn(SpawnOptions{Cwd: t.TempDir()}); err == nil {
		t.Fatal("spawned a missing program")
	}

	if n := len(m.List()); n != 0 {
		t.Fatalf("%d agents kept after a failed spawn", n)
	}
}

func TestWithPermissionMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"none", nil, "--permission-mode bypassPermissions"},
		{"other args", []string{"--model", "opus"}, "--model opus --permission-mode bypassPermissions"},
		{"mode chosen", []string{"--permission-mode", "acceptEdits"}, "--permission-mode acceptEdits"},
		{"mode chosen with =", []string{"--permission-mode=plan"}, "--permission-mode=plan"},
		{"skip permissions", []string{"--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := strings.Join(WithPermissionMode(tt.in), " "); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResumableNeedsTheSavedConversation(t *testing.T) {
	t.Parallel()

	saved := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(saved, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		info Info
		want bool
	}{
		{"saved", Info{SessionID: "s1", Transcript: saved}, true},
		{"no transcript yet", Info{SessionID: "s1"}, false},
		{"transcript never written", Info{SessionID: "s1", Transcript: saved + ".gone"}, false},
		{"no session", Info{Transcript: saved}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.info.Resumable(); got != tt.want {
				t.Fatalf("Resumable = %v, want %v", got, tt.want)
			}
		})
	}
}
