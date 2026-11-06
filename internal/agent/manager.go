package agent

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound reports an unknown agent.
var ErrNotFound = errors.New("no such agent")

// hookEvents are the Claude Code hooks an agent reports to the gateway.
var hookEvents = []string{
	EventSessionStart, EventUserPromptSubmit, EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
	EventPermissionRequest, EventNotification, EventStop, EventSubagentStop, EventSessionEnd,
}

const (
	// defaultProgram is the agent command when none is configured.
	defaultProgram = "claude"
	// defaultRingSize is how much output a terminal keeps for replay.
	defaultRingSize = 4 << 20
	// hookTimeout is how many seconds Claude gives a hook command.
	hookTimeout = 5
)

// sessionVars are what a Claude Code session sets for the programs it runs.
// An agent started by a gateway that was itself started from Claude Code
// must not inherit them: it would take itself for a child session and not
// save its conversation, so it could never be resumed.
var sessionVars = map[string]bool{
	"CLAUDECODE":                    true,
	"CLAUDE_CODE_ENTRYPOINT":        true,
	"CLAUDE_CODE_SESSION_ID":        true,
	"CLAUDE_CODE_CHILD_SESSION":     true,
	"CLAUDE_CODE_SESSION_ATTENDED":  true,
	"CLAUDE_CODE_EXECPATH":          true,
	"CLAUDE_CODE_SSE_PORT":          true,
	"CLAUDE_CODE_PARENT_SESSION_ID": true,
}

// messagingVarPrefix starts the variables a Claude Code session sets for
// its messaging, which a nested session must not inherit either. It
// mirrors Claude Code's own names.
const messagingVarPrefix = "CLAUDE_CODE_MESSAGING_"

// Environment the manager gives every agent.
const (
	// EnvPrefix starts the variables the manager and its owner give an
	// agent: an owner started from an agent must not hand its parent's on.
	EnvPrefix  = "CLAUDE_BOT_AGENT_"
	EnvAgentID = EnvPrefix + "ID"
)

// Config says how to start agents.
type Config struct {
	// Program is the agent command, normally "claude".
	Program string
	// Args go before everything the manager adds, e.g. a permission mode.
	Args []string
	// HookCommand is the shell command Claude runs for every hook; it reads
	// the event on stdin and reports it to the gateway.
	HookCommand string
	// Allow are permission rules every agent gets.
	Allow []string
	// Env is added to every agent's environment, e.g. how to report its
	// hooks. Its PATH entry, if any, goes before the inherited PATH.
	Env map[string]string
	// RingSize is how much output each terminal keeps for replay.
	RingSize int
}

// DefaultPermissionMode is the permission mode agents start in unless
// their arguments choose one: they run on a machine kept for them, and
// nobody is there to approve each tool call.
const DefaultPermissionMode = "bypassPermissions"

// WithPermissionMode returns args with --permission-mode set to the
// default, unless args already choose how permissions work.
func WithPermissionMode(args []string) []string {
	for _, a := range args {
		if a == "--permission-mode" || strings.HasPrefix(a, "--permission-mode=") || a == "--dangerously-skip-permissions" {
			return args
		}
	}

	return append(slices.Clone(args), "--permission-mode", DefaultPermissionMode)
}

// SpawnOptions describe a new agent.
type SpawnOptions struct {
	Name string
	Cwd  string
	// Project is the gateway project the agent works for.
	Project int64
	// Prompt is the first message, submitted as the program starts.
	Prompt string
}

// Manager owns the agents.
type Manager struct {
	cfg      Config
	onChange func()
	notes    notifier

	mu     sync.Mutex
	agents map[string]*Agent

	// changes are the agents that changed since the owner was last told.
	changesMu sync.Mutex
	changes   map[string]struct{}
}

// NewManager returns a manager that calls onChange after any change of an
// agent it still has: in the background, once for a burst of changes.
func NewManager(cfg Config, onChange func()) *Manager {
	if cfg.Program == "" {
		cfg.Program = defaultProgram
	}

	if cfg.RingSize <= 0 {
		cfg.RingSize = defaultRingSize
	}

	m := &Manager{cfg: cfg, onChange: onChange, agents: map[string]*Agent{}, changes: map[string]struct{}{}}
	m.notes.f = m.tell

	return m
}

// Restore brings back agents saved by an earlier run of the owner, to show.
// Their programs ended with it, so live ones come back lost.
func (m *Manager) Restore(infos []Info) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, info := range infos {
		if info.Status.Live() {
			info.Status, info.Detail, info.PID = Lost, "", 0
		}

		// What it left the owner ends with ReapAll, before.
		info.Procs = nil

		a := newAgent(info, m.cfg.RingSize, m.changed)
		a.note("gateway restarted; earlier output is gone")
		m.agents[info.ID] = a
	}
}

// Spawn starts a new agent with a fresh session, unless ctx ended. The
// agent outlives ctx: Kill ends it.
func (m *Manager) Spawn(ctx context.Context, o SpawnOptions) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}

	cwd, err := filepath.Abs(o.Cwd)
	if err != nil {
		return Info{}, err
	}

	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return Info{}, fmt.Errorf("no such folder: %s", cwd)
	}

	now := time.Now()
	info := Info{
		ID:        newID(now),
		Name:      o.Name,
		Cwd:       cwd,
		Project:   o.Project,
		SessionID: newUUID(),
		Status:    Starting,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if info.Name == "" {
		info.Name = filepath.Base(cwd)
	}

	a := newAgent(info, m.cfg.RingSize, m.changed)

	cmd, err := m.command(info, o.Prompt)
	if err != nil {
		return Info{}, err
	}

	// Known before it starts: its first changes, even an exit right away,
	// look it up by id.
	m.mu.Lock()
	m.agents[info.ID] = a
	m.mu.Unlock()

	if err := a.start(cmd); err != nil {
		m.mu.Lock()
		delete(m.agents, info.ID)
		m.mu.Unlock()

		return Info{}, err
	}

	return a.Info(), nil
}

// command is how to run an agent: the program, its hooks reporting to the
// gateway, and the session it keeps.
func (m *Manager) command(info Info, prompt string) (*exec.Cmd, error) {
	path, err := exec.LookPath(m.cfg.Program)
	if err != nil {
		return nil, fmt.Errorf("agent program %q: %w", m.cfg.Program, err)
	}

	settings, err := m.settings()
	if err != nil {
		return nil, err
	}

	args := slices.Clone(m.cfg.Args)
	args = append(args, "--settings", settings, "--session-id", info.SessionID)

	if prompt != "" {
		if strings.HasPrefix(prompt, "-") {
			prompt = " " + prompt // not an option
		}

		args = append(args, prompt)
	}

	// The agent outlives any request; Kill ends it, with signals to its
	// process group.
	cmd := exec.Command(path, args...) //nolint:noctx,gosec // see above; the program is the configured agent
	cmd.Dir = info.Cwd
	cmd.Env = m.env(info)

	return cmd, nil
}

// settings is the --settings JSON that adds the gateway's hooks and
// permission rules. Claude merges it with the user's own settings; nothing
// on disk changes.
func (m *Manager) settings() (string, error) {
	if m.cfg.HookCommand == "" && len(m.cfg.Allow) == 0 {
		return "{}", nil
	}

	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}

	type matcher struct {
		Hooks []hook `json:"hooks"`
	}

	hooks := map[string][]matcher{}
	for _, e := range hookEvents {
		hooks[e] = []matcher{{Hooks: []hook{{Type: "command", Command: m.cfg.HookCommand, Timeout: hookTimeout}}}}
	}

	settings := map[string]any{"hooks": hooks}
	if len(m.cfg.Allow) > 0 {
		settings["permissions"] = map[string]any{"allow": m.cfg.Allow}
	}

	b, err := json.Marshal(settings)

	return string(b), err
}

func (m *Manager) env(info Info) []string {
	extra := map[string]string{
		"TERM":      "xterm-256color",
		"COLORTERM": "truecolor",
		EnvAgentID:  info.ID,
	}

	maps.Copy(extra, m.cfg.Env)

	var env []string

	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")

		switch {
		case sessionVars[k] || strings.HasPrefix(k, messagingVarPrefix) || strings.HasPrefix(k, EnvPrefix):
			// Not a nested session; the gateway's own settings come below.
			continue
		case k == "PATH" && extra["PATH"] != "":
			extra["PATH"] += string(os.PathListSeparator) + v

			continue
		}

		if _, ok := extra[k]; !ok {
			env = append(env, kv)
		}
	}

	for k, v := range extra {
		env = append(env, k+"="+v)
	}

	return env
}

// changed notes that agent id changed, for tell. Agents call it with
// their lock held: it does not block.
func (m *Manager) changed(id string) {
	if m.onChange == nil {
		return
	}

	m.changesMu.Lock()
	m.changes[id] = struct{}{}
	m.changesMu.Unlock()

	m.notes.notify()
}

// tell calls onChange if an agent that changed since it last did is still
// one the manager has.
func (m *Manager) tell() {
	m.changesMu.Lock()
	ids := m.changes
	m.changes = map[string]struct{}{}
	m.changesMu.Unlock()

	for id := range ids {
		if _, err := m.Get(id); err == nil {
			m.onChange()

			return
		}
	}
}

// Get returns an agent by id.
func (m *Manager) Get(id string) (*Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.agents[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	return a, nil
}

// List returns every agent, the oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	agents := make([]*Agent, 0, len(m.agents))

	for _, a := range m.agents {
		agents = append(agents, a)
	}
	m.mu.Unlock()

	infos := make([]Info, 0, len(agents))
	for _, a := range agents {
		infos = append(infos, a.Info())
	}

	slices.SortFunc(infos, func(x, y Info) int {
		return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), strings.Compare(x.ID, y.ID))
	})

	return infos
}

// Hook applies a hook event reported for an agent.
func (m *Manager) Hook(id string, h Hook) error {
	a, err := m.Get(id)
	if err != nil {
		return err
	}

	a.Hook(h)

	return nil
}

// Remove forgets an agent that is no longer running.
func (m *Manager) Remove(id string) error {
	a, err := m.Get(id)
	if err != nil {
		return err
	}

	if a.Info().Status.Live() {
		return ErrLive
	}

	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()

	return nil
}

// KillAll ends every running agent and waits for them.
func (m *Manager) KillAll() {
	var wg sync.WaitGroup

	for _, info := range m.List() {
		if !info.Status.Live() {
			continue
		}

		a, err := m.Get(info.ID)
		if err != nil {
			continue
		}

		wg.Go(func() { _ = a.Kill() })
	}

	wg.Wait()
}

var idSeq atomic.Int64

// newID is unique within a gateway: the time keeps ids apart across
// restarts, the sequence within one millisecond.
func newID(now time.Time) string {
	return "a" + strconv.FormatInt(now.UnixMilli(), 36) + strconv.FormatInt(idSeq.Add(1), 36)
}

func newUUID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AgentInfo returns what an agent is doing.
func (m *Manager) AgentInfo(id string) (Info, error) {
	a, err := m.Get(id)
	if err != nil {
		return Info{}, err
	}

	return a.Info(), nil
}

// WaitAgent waits until an agent's program ends, or ctx does.
func (m *Manager) WaitAgent(ctx context.Context, id string) {
	a, err := m.Get(id)
	if err != nil {
		return
	}

	done := a.Done()
	if done == nil {
		return
	}

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// KillAgent ends an agent's program.
func (m *Manager) KillAgent(id string) error {
	a, err := m.Get(id)
	if err != nil {
		return err
	}

	return a.Kill()
}
