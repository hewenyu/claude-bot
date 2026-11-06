package agent

import (
	"encoding/json"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)

func at(sec float64) time.Time {
	return t0.Add(time.Duration(sec * float64(time.Second)))
}

func TestHooksDriveATurn(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}

	s.hook(Hook{Event: "SessionStart", SessionID: "s1"}, at(0))
	expect(t, &s, Idle, "")

	s.hook(Hook{Event: "UserPromptSubmit"}, at(1))
	s.hook(Hook{Event: "PreToolUse", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"touch x\nmore"}`)}, at(2))
	expect(t, &s, Working, "Bash: touch x")

	s.hook(Hook{Event: "PermissionRequest", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"touch x"}`)}, at(3))
	expect(t, &s, Blocked, "需要授权: Bash: touch x")

	s.input(at(4))
	s.hook(Hook{Event: "PostToolUse"}, at(5))
	expect(t, &s, Working, "")

	s.hook(Hook{Event: "Stop", LastAssistantMessage: "ok"}, at(6))
	expect(t, &s, Idle, "")

	if s.Turns != 1 || s.Ended != EndStop || s.LastMessage != "ok" || s.SessionID != "s1" {
		t.Fatalf("turns %d, last %q, session %q", s.Turns, s.LastMessage, s.SessionID)
	}
}

func TestSubagentHooksKeepTheParentsSession(t *testing.T) {
	t.Parallel()

	s := state{Status: Working, SessionID: "parent"}
	s.hook(Hook{Event: "PostToolUse", SessionID: "child", SubagentID: "a1"}, at(0))

	if s.SessionID != "parent" {
		t.Fatalf("session = %q", s.SessionID)
	}
}

func TestTitleWithoutHooks(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}

	s.title("✳ Claude Code", at(0))
	expect(t, &s, Idle, "")

	s.title("◐ Fix tests", at(1))
	expect(t, &s, Working, "")

	if s.Title != "Fix tests" {
		t.Fatalf("title = %q", s.Title)
	}
}

func TestEscEndsATurnWithoutStop(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "UserPromptSubmit"}, at(0))
	s.title("◐ Fix", at(0.5))
	s.title("✳ Fix", at(1))

	s.settle(at(2))
	expect(t, &s, Working, "")

	s.settle(at(3.1))
	expect(t, &s, Idle, "已中断")

	// The turn ended, so whoever follows the agent sees it end.
	if s.Turns != 1 || s.Ended != EndInterrupt {
		t.Fatalf("turns %d, ended %q", s.Turns, s.Ended)
	}
}

func TestStopAfterStillTitleIsANormalTurnEnd(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "UserPromptSubmit"}, at(0))
	s.title("✳ Fix", at(1))
	s.hook(Hook{Event: "Stop"}, at(1.2))
	s.settle(at(9))
	expect(t, &s, Idle, "")
}

func TestPermissionPromptStillTitleIsNotIdle(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "PreToolUse", ToolName: "Bash"}, at(0))
	s.title("✳ Fix", at(1))
	s.hook(Hook{Event: "PermissionRequest", ToolName: "Bash"}, at(1.1))
	s.settle(at(9))
	expect(t, &s, Blocked, "需要授权: Bash")
}

func TestDecliningAPermissionPrompt(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "PermissionRequest", ToolName: "Bash"}, at(0))

	// Moving the selection draws the prompt again: still blocked.
	s.input(at(1))
	s.output([]byte("\x1b[2m❯ 2. No\x1b[0m\r\n Esc to\x1b[1m cancel"))
	s.settle(at(5))
	expect(t, &s, Blocked, "需要授权: Bash")

	// Choosing "No" leaves no prompt and no hook.
	s.input(at(6))
	s.output([]byte("⎿  No (tell Claude what to do differently)"))
	s.settle(at(7))
	expect(t, &s, Blocked, "需要授权: Bash")
	s.settle(at(8.1))
	expect(t, &s, Idle, "已拒绝")

	if s.Turns != 1 || s.Ended != EndDeclined {
		t.Fatalf("turns %d, ended %q", s.Turns, s.Ended)
	}
}

func TestApprovingFromTheTerminalSpins(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "PermissionRequest", ToolName: "Bash"}, at(0))
	s.input(at(1))
	s.title("◑ Fix", at(1.2))
	expect(t, &s, Working, "")
}

func expect(t *testing.T, s *state, st Status, detail string) {
	t.Helper()

	if s.Status != st || s.Detail != detail {
		t.Fatalf("status %s %q, want %s %q", s.Status, s.Detail, st, detail)
	}
}

func TestTrustDialogBeforeTheSession(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}

	// As Claude Code draws it: cursor moves for spaces, split across reads.
	s.output([]byte("Yes,\x1b[1CI\x1b[1Ctrust\x1b[1Cthis\x1b[1Cfolder\r\nEnter\x1b[1Cto\x1b[1Cconfirm\x1b[1C·\x1b[1CEsc\x1b[1Cto"))
	expect(t, &s, Starting, "")

	s.output([]byte("\x1b[1Ccancel"))
	expect(t, &s, Blocked, "启动时需要确认: 信任这个目录")

	s.input(at(1))
	s.hook(Hook{Event: "SessionStart", SessionID: "s1"}, at(1.5))
	expect(t, &s, Idle, "")
}

func TestOutputIsIgnoredOnceHooksSpeak(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.hook(Hook{Event: "UserPromptSubmit"}, at(0))
	s.output([]byte("the user typed: Esc to cancel"))
	expect(t, &s, Working, "")
}

func TestStartupDialogWaitsForTheSession(t *testing.T) {
	t.Parallel()

	s := state{Status: Starting}
	s.output([]byte("Yes, I trust this folder\r\nEnter to confirm · Esc to cancel"))
	s.input(at(1)) // e.g. moving the selection, drawn without the footer
	s.settle(at(9))
	expect(t, &s, Blocked, "启动时需要确认: 信任这个目录")
}

func TestSplitGlyph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, rest string
		glyph    rune
	}{
		{"✳ Claude Code", "Claude Code", '✳'},
		{"◐ Fix tests", "Fix tests", '◐'},
		{"Claude Code", "Claude Code", 0},
		{"✳", "✳", 0},
		{"", "", 0},
		{"\xff x", "\xff x", 0},
	}

	for _, tt := range tests {
		glyph, rest := splitGlyph(tt.in)
		if glyph != tt.glyph || rest != tt.rest {
			t.Errorf("splitGlyph(%q) = %q, %q; want %q, %q", tt.in, glyph, rest, tt.glyph, tt.rest)
		}
	}
}
