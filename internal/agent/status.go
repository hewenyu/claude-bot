// Package agent runs coding agents in terminals and follows what they are
// doing: working, blocked on a person, or idle.
package agent

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hewenyu/claude-bot/internal/term"
)

// Status is what an agent is doing.
type Status string

// Statuses. Lost is an agent whose process ended with the gateway, as an
// earlier run of it saved the agent.
const (
	Starting Status = "starting"
	Working  Status = "working"
	Blocked  Status = "blocked"
	Idle     Status = "idle"
	Exited   Status = "exited"
	Lost     Status = "lost"
)

// Live reports whether a process is behind the status.
func (s Status) Live() bool {
	return s == Starting || s == Working || s == Blocked || s == Idle
}

// Claude Code hook events.
const (
	EventSessionStart       = "SessionStart"
	EventUserPromptSubmit   = "UserPromptSubmit"
	EventPreToolUse         = "PreToolUse"
	EventPostToolUse        = "PostToolUse"
	EventPostToolUseFailure = "PostToolUseFailure"
	EventPermissionRequest  = "PermissionRequest"
	EventNotification       = "Notification"
	EventStop               = "Stop"
	EventSubagentStop       = "SubagentStop"
	EventSessionEnd         = "SessionEnd"
)

// notifyPermission is the notification type of a permission prompt.
const notifyPermission = "permission_prompt"

// Details shown with a status.
const (
	detailPermission = "需要授权: "
	detailStartup    = "启动时需要确认"
	detailTrust      = detailStartup + ": 信任这个目录"
	detailInterrupt  = "已中断"
	detailDeclined   = "已拒绝"
)

// End is how a turn ended.
type End string

// How a turn ended, kept in Info.Ended. Only a Stop hook means the agent
// finished its turn itself; the others are a person stopping it.
const (
	EndStop      End = "stop"
	EndInterrupt End = "interrupt"
	EndDeclined  End = "declined"
)

// Phrases of Claude Code's screen, as squash leaves them: they mirror what
// it draws, and change when it does.
const (
	// screenCancel and screenAsk are on its dialogs: "Esc to cancel", "Do
	// you want to …".
	screenCancel = "esctocancel"
	screenAsk    = "doyouwantto"
	// screenTrust is on the dialog that asks, before a session in a new
	// folder, whether to trust it.
	screenTrust = "trustthisfolder"
)

// tailMax is how much squashed output is kept to match a phrase split
// between reads: more than the longest phrase.
const tailMax = 256

// glyphIdle is the glyph Claude Code puts before its title when idle.
const glyphIdle = '✳'

// toolLineMax bounds the tool call shown as a detail, in runes.
const toolLineMax = 120

// settleDelay is how long a fallback waits for a hook before it trusts what
// the screen says.
const settleDelay = 2 * time.Second

// Hook is one Claude Code hook event, as its hook command received it on
// stdin. Only the fields the status needs are read.
type Hook struct {
	Event                string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	TranscriptPath       string          `json:"transcript_path"`
	SubagentID           string          `json:"agent_id"`
	ToolName             string          `json:"tool_name"`
	ToolInput            json.RawMessage `json:"tool_input"`
	Message              string          `json:"message"`
	NotificationType     string          `json:"notification_type"`
	LastAssistantMessage string          `json:"last_assistant_message"`
}

// state is the part of an agent that hooks, titles and input change. Hooks
// are the truth; the title and the screen only cover what hooks never say:
// a turn interrupted with Esc, or a permission prompt answered "no".
type state struct {
	Activity

	hooked   bool
	lastHook time.Time
	spinning bool
	// stillAt is when the title stopped spinning while the agent worked.
	stillAt time.Time
	// inputAt is when a person typed into a blocked agent, and promptSeen
	// whether the prompt was drawn again since.
	inputAt    time.Time
	promptSeen bool
	// tail is the end of the output looked at so far, so a phrase split
	// between reads still matches.
	tail string
}

// hook applies a hook event. It reports whether anything shown changed.
func (s *state) hook(h Hook, now time.Time) bool {
	before := s.shown()

	s.hooked = true
	s.lastHook = now

	s.session(h)
	s.countSubagents(h)

	switch h.Event {
	case EventSessionStart:
		if s.Status == Starting || s.Status == Blocked {
			// A dialog before the session, if any, was answered.
			s.Status, s.Detail = Idle, ""
		}
	case EventUserPromptSubmit:
		s.Status, s.Detail = Working, ""
	case EventPreToolUse:
		s.Status, s.Detail = Working, toolSummary(h)
	case EventPostToolUse, EventPostToolUseFailure, EventSubagentStop:
		s.toolDone(h)
	case EventPermissionRequest:
		s.Status, s.Detail = Blocked, detailPermission+toolSummary(h)
		s.inputAt = time.Time{}
	case EventNotification:
		if h.NotificationType == notifyPermission && s.Status != Blocked {
			s.Status, s.Detail = Blocked, h.Message
			s.inputAt = time.Time{}
		}
	case EventStop:
		s.Status, s.Detail = Idle, ""
		s.Turns++
		s.Ended = EndStop
		s.LastMessage = h.LastAssistantMessage
	}

	return s.shown() != before
}

// countSubagents follows the subagents the agent starts and that end.
func (s *state) countSubagents(h Hook) {
	switch {
	case h.Event == EventPreToolUse && startsSubagent(h):
		s.Subagents++
	case h.Event == EventPostToolUseFailure && startsSubagent(h),
		// It never ran: no SubagentStop comes.
		h.Event == EventSubagentStop:
		s.Subagents = max(0, s.Subagents-1)
	}
}

// subagentTools are the tools that start a subagent: Agent, once Task.
var subagentTools = map[string]bool{"Agent": true, "Task": true}

// startsSubagent reports a call of the agent's own that starts a subagent,
// in the foreground or the background: either way a SubagentStop ends it.
func startsSubagent(h Hook) bool {
	return h.SubagentID == "" && subagentTools[h.ToolName]
}

// session notes the session a hook comes from. A subagent's hooks carry
// its own session; the agent keeps its own.
func (s *state) session(h Hook) {
	if h.SubagentID != "" {
		return
	}

	if h.SessionID != "" {
		s.SessionID = h.SessionID
	}

	if h.TranscriptPath != "" {
		s.Transcript = h.TranscriptPath
	}
}

// toolDone follows a tool call that ended: the agent works on.
func (s *state) toolDone(h Hook) {
	if s.Status == Blocked || s.Status == Idle || s.Status == Starting {
		s.Status = Working
	}

	if s.Status == Working && h.SubagentID == "" {
		s.Detail = ""
	}
}

// title applies a window title the program set. Only a live agent's
// status follows it: one that ended stays ended.
func (s *state) title(t string, now time.Time) bool {
	before := s.shown()

	glyph, rest := splitGlyph(t)
	s.Title = rest

	switch {
	case !s.Status.Live():
		// E.g. something it left draws on.
	case isSpinner(glyph):
		s.spinning = true
		s.stillAt = time.Time{}

		if !s.hooked || s.Status == Blocked && s.inputAt.After(s.lastHook) {
			s.Status, s.Detail = Working, ""
		}
	case glyph == glyphIdle:
		s.spinning = false

		if !s.hooked && (s.Status == Starting || s.Status == Working) {
			s.Status = Idle
		}

		if s.Status == Working {
			s.stillAt = now
		}
	}

	return s.shown() != before
}

// input records that a person typed into the terminal.
func (s *state) input(now time.Time) {
	if s.Status == Blocked {
		s.inputAt = now
		s.promptSeen = false
		s.tail = ""
	}
}

// output looks at what the program printed for dialogs no hook reports:
// one before the session starts, such as trusting a folder, and a
// permission prompt drawn again after a person typed into it.
func (s *state) output(p []byte) {
	startup := s.Status == Starting && !s.hooked
	again := s.Status == Blocked && !s.inputAt.IsZero() && !s.promptSeen

	if !startup && !again {
		s.tail = ""

		return
	}

	text := s.tail + squash(p)
	s.tail = text[max(0, len(text)-tailMax):]

	if !strings.Contains(text, screenCancel) && !strings.Contains(text, screenAsk) {
		return
	}

	if again {
		s.promptSeen = true

		return
	}

	s.Status, s.Detail = Blocked, detailStartup
	if strings.Contains(text, screenTrust) {
		s.Detail = detailTrust
	}
}

// squash is text without escape sequences, spaces or case, so that a
// phrase matches however the program drew it: Claude Code moves the cursor
// instead of printing spaces.
func squash(p []byte) string {
	text := term.StripANSI(string(p))

	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}

		return unicode.ToLower(r)
	}, text)
}

// settle applies the fallbacks once no hook has explained what the screen
// showed for settleDelay.
func (s *state) settle(now time.Time) bool {
	before := s.shown()

	switch {
	case s.Status == Working && !s.spinning && !s.stillAt.IsZero() &&
		s.stillAt.After(s.lastHook) && now.Sub(s.stillAt) >= settleDelay:
		// Esc ends a turn without a Stop hook. It is a turn all the same:
		// whoever follows the agent must see it end.
		s.Status, s.Detail = Idle, detailInterrupt
		s.Turns++
		s.Ended = EndInterrupt
		s.stillAt = time.Time{}
	case s.Status == Blocked && s.hooked && !s.inputAt.IsZero() && !s.promptSeen &&
		s.inputAt.After(s.lastHook) && now.Sub(s.inputAt) >= settleDelay:
		// Declining a permission prompt ends the turn without a hook. A
		// dialog before the session ends with SessionStart or the exit.
		s.Status, s.Detail = Idle, detailDeclined
		s.Turns++
		s.Ended = EndDeclined
		s.inputAt = time.Time{}
	}

	return s.shown() != before
}

type shown struct {
	status           Status
	detail, title    string
	ended            End
	turns, subagents int
	session          string
}

func (s *state) shown() shown {
	return shown{s.Status, s.Detail, s.Title, s.Ended, s.Turns, s.Subagents, s.SessionID}
}

// splitGlyph splits the status glyph Claude Code puts before its title.
func splitGlyph(t string) (rune, string) {
	r, n := utf8.DecodeRuneInString(t)
	if r == utf8.RuneError {
		return 0, t
	}

	if rest, ok := strings.CutPrefix(t[n:], " "); ok {
		return r, rest
	}

	return 0, t
}

func isSpinner(r rune) bool {
	return r >= 0x2800 && r <= 0x28FF || r >= 0x25D0 && r <= 0x25D3
}

// toolSummary is one line saying what a tool call does.
func toolSummary(h Hook) string {
	var in map[string]any

	_ = json.Unmarshal(h.ToolInput, &in)

	for _, k := range []string{"command", "file_path", "pattern", "url", "description", "prompt"} {
		if v, ok := in[k].(string); ok && v != "" {
			return h.ToolName + ": " + term.Clip(firstLine(v), toolLineMax)
		}
	}

	return h.ToolName
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}
