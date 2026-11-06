package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/runner"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The gateway runs Claude Code with /bot in the projects whose runner is
// on, so nobody has to keep `/loop /bot` open in each: the runner decides
// when an agent starts, /bot what it does. The agents run in terminals the
// gateway owns; the page shows them. They report their hooks to the
// gateway with `claude-bot hook`.
const (
	runnerFile = "runner.json"
	// runnerSaveDelay gathers a burst of changes into one save.
	runnerSaveDelay = 500 * time.Millisecond
	// hookTimeout is how long a hook waits for the gateway: never long, so
	// a gateway that went away does not hold Claude up.
	hookTimeout = 2 * time.Second
	// maxHookInput bounds the hook event read from stdin.
	maxHookInput = 1 << 20
	// hookTokenBytes is the size of the agents' token.
	hookTokenBytes = 24
	// headerAgent names the agent a hook comes from.
	headerAgent = "X-Claude-Bot-Agent"
	// hookPath is where agents report their hooks.
	hookPath = "/agent/hook"
	// runnerOn is the value that turns a project's runner on.
	runnerOn = "on"
)

// What the gateway gives each agent, besides agent.EnvAgentID.
const (
	envAgentURL   = agent.EnvPrefix + "URL"
	envAgentToken = agent.EnvPrefix + "TOKEN"
)

// How the gateway starts the agents, from its own environment.
const (
	envRunnerAgent = "CLAUDE_BOT_RUNNER_AGENT" // the agent command, "claude" by default
	envRunnerArgs  = "CLAUDE_BOT_RUNNER_ARGS"  // its extra arguments, space separated
)

// botAllow are the commands /bot runs that an agent may run unasked, when
// its permission mode asks for others.
var botAllow = []string{"Bash(claude-bot reply:*)", "Bash(claude-bot inbox:*)", "Bash(claude-bot done:*)", "Bash(claude-bot github:*)"}

// runnerState is what the gateway keeps of the runner across restarts.
type runnerState struct {
	Runs   []runner.Run `json:"runs"`
	Agents []agent.Info `json:"agents"`
}

// bots is the runner and the agents it runs.
type bots struct {
	agents *agent.Manager
	runner *runner.Runner
	token  string // the agents' token for their hooks
	path   string // where the state is saved
	log    *slog.Logger
	done   chan struct{}

	saveMu   sync.Mutex
	saveSoon *time.Timer
	stopping atomic.Bool
}

// agentCommand is the agent command and its arguments, from the gateway's
// environment.
func agentCommand() (string, []string) {
	return cmp.Or(os.Getenv(envRunnerAgent), "claude"), agent.WithPermissionMode(strings.Fields(os.Getenv(envRunnerArgs)))
}

// startBots starts the runner, its agents running program with args and
// reporting to the gateway at url, and runs it until ctx ends. Wait for it
// with wait.
func startBots(ctx context.Context, home, url string, log *slog.Logger, program string, args []string) (*bots, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}

	// Agents run the hook through this path; resolve a symlink so it keeps
	// working if the link moves.
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	b := &bots{token: newHex(hookTokenBytes), path: filepath.Join(home, runnerFile), log: log, done: make(chan struct{})}

	var saved runnerState

	raw, err := os.ReadFile(b.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, fmt.Errorf("%s: %w", b.path, err)
		}
	}

	b.agents = agent.NewManager(agent.Config{
		Program:     program,
		Args:        args,
		HookCommand: shellQuote(self) + " hook",
		Allow:       botAllow,
		Env: map[string]string{
			envAgentURL:   strings.TrimSuffix(url, "/") + hookPath,
			envAgentToken: b.token,
			// /bot runs claude-bot: this one, whatever the PATH says.
			"PATH": filepath.Dir(self),
		},
	}, func(agent.Info) {
		b.changed()

		if b.runner != nil {
			b.runner.Kick()
		}
	})
	b.agents.Restore(saved.Agents)

	b.runner = runner.New(runner.Options{
		Agents:   b.agents,
		Pending:  pendingIn,
		Check:    checkBotCommand,
		OnChange: b.changed,
	}, runner.Saved{Runs: saved.Runs})

	go func() {
		defer close(b.done)

		b.runner.Run(ctx)
		b.shutdown()
	}()

	log.Info("runner started", "agent", program, "args", strings.Join(args, " "))

	return b, nil
}

// shutdown saves the runner and ends its agents. They stay saved as they
// were, to come back lost and resume.
func (b *bots) shutdown() {
	b.stopping.Store(true)
	b.save()
	b.agents.KillAll()
}

// wait waits for the runner to stop after its context ended.
func (b *bots) wait() {
	if b != nil {
		<-b.done
	}
}

// changed saves soon after any change, not on every one.
func (b *bots) changed() {
	if b.stopping.Load() {
		// Agents ending with the gateway stay saved as running.
		return
	}

	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	if b.saveSoon == nil {
		b.saveSoon = time.AfterFunc(runnerSaveDelay, func() {
			b.saveMu.Lock()
			b.saveSoon = nil
			b.saveMu.Unlock()

			if !b.stopping.Load() {
				b.save()
			}
		})
	}
}

func (b *bots) save() {
	if b.runner == nil {
		return // restoring the agents, before the runner is made
	}

	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	st := runnerState{Runs: b.runner.Saved().Runs, Agents: b.agents.List()}
	if err := writeJSON(b.path, st); err != nil {
		b.log.Error("save runner", "err", err)
	}
}

// sync tells the runner which projects there are, which it runs and how
// many agents may run at once.
func (b *bots) sync(ctx context.Context, s *store.Store) error {
	projects, err := s.Projects(ctx)
	if err != nil {
		return err
	}

	on, err := s.SettingsWithPrefix(ctx, store.SettingRunnerPrefix)
	if err != nil {
		return err
	}

	list := make([]runner.Project, 0, len(projects))
	for _, p := range projects {
		list = append(list, runner.Project{ID: p.ID, Name: p.Name, Dir: p.Dir, On: on[strconv.FormatInt(p.ID, 10)] == runnerOn})
	}

	b.runner.SetProjects(list)

	n, err := runnerMaxParallel(ctx, s)
	if err != nil {
		return err
	}

	return b.runner.SetMaxParallel(n)
}

// runnerKey is the setting that turns a project's runner on.
func runnerKey(project int64) string {
	return store.SettingRunnerPrefix + strconv.FormatInt(project, 10)
}

// runnerMaxParallel is how many agents may run at once.
func runnerMaxParallel(ctx context.Context, s *store.Store) (int, error) {
	v, err := s.Setting(ctx, store.SettingRunnerMaxParallel)
	if err != nil || v == "" {
		return runner.DefaultMaxParallel, err
	}

	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > runner.MaxParallelLimit {
		return runner.DefaultMaxParallel, nil //nolint:nilerr // a bad value is the default, as the page shows it
	}

	return n, nil
}

// pendingIn reads what waits in a project's inbox. A task's length counts
// what people wrote, not the bot's own replies: a reply is no news.
func pendingIn(dir string) ([]runner.Pending, error) {
	tasks, err := projectInbox(store.Project{Dir: dir}).PendingTasks()
	if err != nil {
		return nil, err
	}

	out := make([]runner.Pending, 0, len(tasks))

	for _, t := range tasks {
		n := 0

		for _, e := range t.Entries {
			if e.Kind != inbox.EntryBot {
				n++
			}
		}

		out = append(out, runner.Pending{ID: t.ID, Entries: n})
	}

	return out, nil
}

// checkBotCommand reports a project where Claude Code has no /bot: not in
// the project, not in the user's own commands or skills.
func checkBotCommand(dir string) error {
	places := []string{
		filepath.Join(dir, ".claude", "commands", "bot.md"),
		filepath.Join(dir, ".claude", "skills", "bot", "SKILL.md"),
	}

	if home, err := os.UserHomeDir(); err == nil {
		places = append(places,
			filepath.Join(home, ".claude", "commands", "bot.md"),
			filepath.Join(home, ".claude", "skills", "bot", "SKILL.md"))
	}

	for _, p := range places {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
	}

	return fmt.Errorf("%s: %w", dir, runner.ErrNoBotCommand)
}

// cmdHook forwards a Claude Code hook event, read from stdin, to the
// gateway that runs the agent. It never fails the hook: an agent the
// gateway does not run reports nothing, and Claude goes on.
func cmdHook(ctx context.Context, _ string, _ []string) error {
	forwardHook(ctx, os.Getenv(envAgentURL), os.Getenv(envAgentToken), os.Getenv(agent.EnvAgentID), os.Stdin)

	return nil
}

// forwardHook posts the hook event read from in to url, as agent id.
func forwardHook(ctx context.Context, url, token, id string, in io.Reader) {
	body, err := io.ReadAll(io.LimitReader(in, maxHookInput))
	if err != nil || url == "" || token == "" || id == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body)) //nolint:gosec // the gateway's own address, given by the gateway
	if err != nil {
		return
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerAgent, id)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // the gateway's own loopback address, given by the gateway
	if err == nil {
		_ = resp.Body.Close()
	}
}

func newHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// shellQuote quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
