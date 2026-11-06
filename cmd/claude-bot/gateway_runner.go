package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/runner"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The gateway runs Claude Code with /bot in the projects whose runner is
// on, so nobody has to keep a terminal open in each: the runner decides
// when an agent starts, /bot what it does. The agents run in terminals the
// gateway owns; the page shows them. They report their hooks to the
// gateway with `claude-bot hook`, and the end of their round with
// `claude-bot finish`.
const (
	runnerFile = "runner.json"
	// runnerSaveDelay gathers a burst of changes into one save.
	runnerSaveDelay = 500 * time.Millisecond
	// hookTokenBytes is the size of the agents' token.
	hookTokenBytes = 24
)

// How the gateway starts the agents, from its own environment.
const (
	envRunnerAgent = "CLAUDE_BOT_RUNNER_AGENT" // the agent command, agent.DefaultProgram by default
	envRunnerArgs  = "CLAUDE_BOT_RUNNER_ARGS"  // its extra arguments, space separated
)

// botAllow are the commands /bot runs that an agent may run unasked, when
// its permission mode asks for others.
var botAllow = []string{
	"Bash(claude-bot reply:*)", "Bash(claude-bot inbox:*)", "Bash(claude-bot done:*)", "Bash(claude-bot github:*)",
	"Bash(claude-bot finish:*)",
}

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
	final    bool // under saveMu: shutdown saved the state for the last time
	stopping atomic.Bool
}

// agentCommand is the agent command and its arguments, from the gateway's
// environment.
func agentCommand() (string, []string) {
	return cmp.Or(os.Getenv(envRunnerAgent), "claude"), agent.WithPermissionMode(strings.Fields(os.Getenv(envRunnerArgs)))
}

// startBots starts the runner, its agents running program with args and
// reporting to the gateway at url, its base URL, and runs it until ctx
// ends. Wait for it with wait.
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
	if err := atomicfile.ReadJSON(b.path, &saved); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	b.agents = agent.NewManager(agent.Config{
		Program:     program,
		Args:        args,
		HookCommand: shellQuote(self) + " hook",
		Allow:       botAllow,
		Env: map[string]string{
			envAgentURL:   strings.TrimSuffix(url, "/"),
			envAgentToken: b.token,
			// /bot runs claude-bot: this one, whatever the PATH says.
			"PATH": filepath.Dir(self),
		},
	}, func() {
		b.changed()
		b.runner.Kick()
	})

	// The runner is made before any agent is, so that what an agent
	// reports always finds it.
	b.runner = runner.New(runner.Options{
		Agents:   b.agents,
		Pending:  pendingIn,
		Check:    checkBotCommand,
		OnChange: b.changed,
	}, runner.Saved{Runs: saved.Runs})

	// What its agents left when the gateway last stopped, e.g. crashed,
	// ends now; each project keeps only its latest agent, to show.
	if n := agent.ReapAll(saved.Agents); n > 0 {
		log.Info("ended processes agents of the last run left", "count", n)
	}

	b.agents.Restore(latestAgents(saved))

	go func() {
		defer close(b.done)

		b.runner.Run(ctx)
		b.shutdown()
	}()

	log.Info("runner started", "agent", program, "args", strings.Join(args, " "))

	return b, nil
}

// latestAgents are the saved agents a run still names; the others did
// earlier rounds.
func latestAgents(saved runnerState) []agent.Info {
	named := map[string]bool{}
	for _, run := range saved.Runs {
		named[run.AgentID] = true
	}

	var out []agent.Info

	for _, a := range saved.Agents {
		if named[a.ID] {
			out = append(out, a)
		}
	}

	return out
}

// shutdown saves the runner and ends its agents. They stay saved as they
// were, to come back lost: a new agent does the round they were doing.
func (b *bots) shutdown() {
	b.stopping.Store(true)
	b.save(true)
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

			b.save(false)
		})
	}
}

// save writes the runner's state; final is shutdown's save, after which
// nothing is saved: a save scheduled before it would record the agents
// ending with the gateway as ended.
func (b *bots) save(final bool) {
	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	if b.final {
		return
	}

	b.final = final

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

	on, err := s.RunnersOn(ctx)
	if err != nil {
		return err
	}

	list := make([]runner.Project, 0, len(projects))
	for _, p := range projects {
		list = append(list, runner.Project{ID: p.ID, Name: p.Name, Dir: p.Dir, On: on[p.ID]})
	}

	b.runner.SetProjects(list)

	n, err := runnerMaxParallel(ctx, s)
	if err != nil {
		return err
	}

	return b.runner.SetMaxParallel(n)
}

// runnerMaxParallel is how many agents may run at once.
func runnerMaxParallel(ctx context.Context, s *store.Store) (int, error) {
	v, err := s.Setting(ctx, store.SettingRunnerMaxParallel)
	if err != nil || v == "" {
		return runner.DefaultMaxParallel, err
	}

	n, err := strconv.Atoi(v)
	if err != nil || !validMaxParallel(n) {
		return runner.DefaultMaxParallel, nil //nolint:nilerr // a bad value is the default, as the page shows it
	}

	return n, nil
}

// validMaxParallel reports whether the runner takes n agents at once.
func validMaxParallel(n int) bool {
	return n >= 1 && n <= runner.MaxParallelLimit
}

// pendingIn reads what waits in a project's inbox. A task's length counts
// what people wrote, not the bot's own replies: a reply is no news, but
// when the latest was is how the runner tells a task answered.
func pendingIn(dir string) ([]runner.Pending, error) {
	tasks, err := inboxOf(config.BotDir(dir)).PendingTasks()
	if err != nil {
		return nil, err
	}

	out := make([]runner.Pending, 0, len(tasks))

	for _, t := range tasks {
		p := runner.Pending{ID: t.ID}

		for _, e := range t.Entries {
			if e.Kind == inbox.EntryBot {
				p.Replied = e.At
			} else {
				p.Entries++
			}
		}

		out = append(out, p)
	}

	return out, nil
}

// checkBotCommand reports a project where Claude Code has no /bot: not in
// the project, not in the user's own commands or skills.
func checkBotCommand(dir string) error {
	places := []string{botCommandPath(dir), botSkillPath(dir)}

	if home, err := os.UserHomeDir(); err == nil {
		places = append(places, botCommandPath(home), botSkillPath(home))
	}

	for _, p := range places {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
	}

	return fmt.Errorf("%s: %w", dir, runner.ErrNoBotCommand)
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
