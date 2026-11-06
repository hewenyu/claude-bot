// Package runner runs Claude Code with claude-bot's /bot in the gateway's
// projects, so nobody has to keep `/loop /bot` open in each repo.
//
// A project whose runner is on gets one agent at a time, in its own
// folder; all projects together get at most MaxParallel. When its inbox has
// tasks, the runner starts Claude Code there and gives it /bot: what to take,
// in what order and how to answer is the project's .claude/commands/bot.md,
// not the runner's. After a round, /bot waits for news itself; the runner
// only lets an idle agent go when another project needs its place, and
// starts it again, in the same session, when news comes.
package runner

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
)

// BotCommand is what every agent is given: claude-bot's own command, which
// each project may adapt in its .claude/commands/bot.md.
const BotCommand = "/bot"

// State is where a project's run is.
type State string

// Run states. An agent holds a place while its run is running, blocked,
// waiting or idle.
const (
	// Queued wants an agent, once there is a place.
	Queued State = "queued"
	// Running has its agent at work.
	Running State = "running"
	// Blocked has its agent stopped at a dialog a person answers.
	Blocked State = "blocked"
	// Waiting ended a round a person should look at: interrupted, or
	// nothing answered.
	Waiting State = "waiting"
	// Idle ended a round; /bot waits for news by itself.
	Idle State = "idle"
	// Resting has no agent: news starts one.
	Resting State = "resting"
	// Failed cannot start until a person fixes it and resumes it.
	Failed State = "failed"
	// Off is a project whose runner a person turned off.
	Off State = "off"
)

// Live reports whether an agent holds a place for the run.
func (s State) Live() bool {
	return s == Running || s == Blocked || s == Waiting || s == Idle
}

// Bounds of how many agents run at once, in all projects.
const (
	DefaultMaxParallel = 3
	MaxParallelLimit   = 32
)

const (
	// defaultPoll is how often the inboxes are read.
	defaultPoll = 3 * time.Second
	// maxRecoveries bounds how often a run resumes by itself after its
	// agent was lost with the gateway, without a round ending in between.
	maxRecoveries = 3
	// retryAfter is how long a run whose start failed waits to try again.
	retryAfter = 30 * time.Second
	// stopWait is how long a start waits for an agent being ended.
	stopWait = 10 * time.Second
	// leftoverAfter is how long what an agent left in its inbox waits
	// before it gets another round; news starts one at once.
	leftoverAfter = 30 * time.Minute
	// nudgeAfter is how long news waits for an idle agent to wake for it
	// before the runner gives it /bot again.
	nudgeAfter = 2 * time.Minute
	// maxLog is how many log lines a run keeps.
	maxLog = 100
	// summaryMax bounds the last thing the agent said, kept as the summary.
	summaryMax = 500
)

// Details shown with a run.
const (
	detailStarting    = "启动中"
	detailNews        = "有新消息"
	detailLeftover    = "还有没处理完的，再跑一轮"
	detailWatching    = "这一轮处理完，等新消息"
	detailNudge       = "有新消息但它没醒，再给它 /bot"
	detailNoProgress  = "这一轮一件都没处理掉"
	detailInterrupted = "这一轮被中断了"
	detailDeclined    = "拒绝授权后停下了"
	detailYield       = "让出名额，有新消息再启动"
	detailRest        = "agent 退出了，有新消息再启动"
	detailResuming    = "gateway 重启，恢复会话"
	detailStartRetry  = "启动失败，稍后重试"
	detailResumed     = "手动继续"
	detailLostOften   = "agent 反复中断，不再自动恢复"
	detailAgentGone   = "agent 不见了: "
	detailTurnedOff   = "已关闭自动处理"
	detailTurnedOn    = "已打开自动处理，有待办就启动"
)

// ErrNoBotCommand reports a project where Claude Code has no /bot command.
var ErrNoBotCommand = errors.New("没有 /bot 命令：在项目里运行 `claude-bot init --bot-md` 生成 .claude/commands/bot.md")

// ErrNoRun reports a project the runner does not run.
var ErrNoRun = errors.New("这个项目没有打开自动处理")

// Project is a gateway project, and whether its runner is on.
type Project struct {
	ID   int64
	Name string
	Dir  string
	On   bool
}

// Pending is a task waiting in an inbox: its id and how long its
// conversation is, which grows with news.
type Pending struct {
	ID      string
	Entries int
}

// Agents runs the agents.
type Agents interface {
	Spawn(o agent.SpawnOptions) (agent.Info, error)
	Resume(id, prompt string) (agent.Info, error)
	AgentInfo(id string) (agent.Info, error)
	WaitAgent(id string, timeout time.Duration)
	SendText(id, text string) error
	KillAgent(id string) error
}

// Options configure a runner.
type Options struct {
	Agents Agents
	// Pending reads what waits in a project's inbox.
	Pending func(dir string) ([]Pending, error)
	// Check reports why /bot cannot run in a project, if it cannot.
	Check func(dir string) error
	// OnChange is called after any run changes.
	OnChange func()
	// Poll is how often the inboxes are read.
	Poll time.Duration
}

// LogLine is one thing that happened to a run.
type LogLine struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Run is a project's agent and what the runner knows about it.
type Run struct {
	Project int64  `json:"project"`
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	State   State  `json:"state"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	// Pending is how many tasks wait in its inbox.
	Pending int `json:"pending"`
	// Summary is the last thing the agent said at the end of a round.
	Summary string `json:"summary,omitempty"`
	// TurnBase is the agent's turn count when its round began; a turn
	// after it ends the round.
	TurnBase int `json:"turn_base"`
	// Starts is how often the run was given to an agent.
	Starts int `json:"starts"`
	// Recoveries is how often its agent was resumed after the gateway
	// went away under it, since a round last ended.
	Recoveries int `json:"recoveries,omitempty"`
	// Round is what waited as the round began: one of them answered is
	// progress.
	Round []string `json:"round,omitempty"`
	// LeftSig is what waited as its agent last ended a round or its
	// program, LeftAt when. What it left does not start it again; news
	// does, and so does the same after a while.
	LeftSig string    `json:"left_sig,omitempty"`
	LeftAt  time.Time `json:"left_at,omitzero"`
	NextTry time.Time `json:"next_try,omitzero"`
	// Stopping is set while the runner ends the agent: a start waits for
	// it before resuming.
	Stopping  bool      `json:"stopping,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Log       []LogLine `json:"log,omitempty"`

	// starting is set while start hands the run to an agent.
	starting bool
	// newsAt is when news first waited for an idle agent that has not
	// woken for it.
	newsAt time.Time
}

// Saved is what the runner keeps across restarts of the gateway.
type Saved struct {
	Runs []Run `json:"runs"`
}

// Runner runs the projects' agents.
type Runner struct {
	o    Options
	kick chan struct{}

	mu          sync.Mutex
	runs        map[int64]*Run
	pending     map[int64][]Pending
	maxParallel int
}

// New returns a runner that picks up where saved left off.
func New(o Options, saved Saved) *Runner {
	if o.Poll <= 0 {
		o.Poll = defaultPoll
	}

	if o.Check == nil {
		o.Check = func(string) error { return nil }
	}

	r := &Runner{
		o: o, kick: make(chan struct{}, 1), runs: map[int64]*Run{}, pending: map[int64][]Pending{},
		maxParallel: DefaultMaxParallel,
	}

	for _, run := range saved.Runs {
		if run.State.Live() && run.AgentID == "" {
			// The gateway stopped while it was starting: nothing runs it.
			run.State, run.Detail = Queued, detailResuming
		}

		r.runs[run.Project] = &run
	}

	return r
}

// Saved returns every run, to save or to show, by project id.
func (r *Runner) Saved() Saved {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := Saved{Runs: make([]Run, 0, len(r.runs))}

	for _, run := range r.runs {
		c := *run
		c.Log = slices.Clone(run.Log)
		c.Round = slices.Clone(run.Round)
		out.Runs = append(out.Runs, c)
	}

	slices.SortFunc(out.Runs, func(a, b Run) int { return cmp.Compare(a.Project, b.Project) })

	return out
}

// MaxParallel is how many agents may run at once, in all projects.
func (r *Runner) MaxParallel() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.maxParallel
}

// SetMaxParallel sets how many agents may run at once. Fewer than run now
// stops none: no more start until they are fewer.
func (r *Runner) SetMaxParallel(n int) error {
	if n < 1 || n > MaxParallelLimit {
		return fmt.Errorf("同时运行的 agent 数要在 1 到 %d 之间，不是 %d", MaxParallelLimit, n)
	}

	r.mu.Lock()
	changed := r.maxParallel != n
	r.maxParallel = n
	r.mu.Unlock()

	if changed {
		r.changed()
		r.Kick()
	}

	return nil
}

// SetProjects tells the runner which projects there are and which it runs.
// A project turned on rests until its inbox has tasks; one turned off, or
// gone, has its agent ended.
func (r *Runner) SetProjects(projects []Project) {
	r.mu.Lock()

	changed := false
	seen := map[int64]bool{}

	for _, p := range projects {
		seen[p.ID] = true

		if r.setProject(p) {
			changed = true
		}
	}

	for id, run := range r.runs {
		if !seen[id] {
			r.stopAgent(run)
			delete(r.runs, id)
			delete(r.pending, id)

			changed = true
		}
	}
	r.mu.Unlock()

	if changed {
		r.changed()
		r.Kick()
	}
}

// setProject applies one project. Call it with the lock held.
func (r *Runner) setProject(p Project) bool {
	run, ok := r.runs[p.ID]

	switch {
	case !ok && !p.On:
		return false
	case !ok:
		run = &Run{Project: p.ID, Name: p.Name, Dir: p.Dir}
		r.runs[p.ID] = run
		r.set(run, Resting, detailTurnedOn)

		return true
	}

	changed := run.Name != p.Name || run.Dir != p.Dir
	run.Name, run.Dir = p.Name, p.Dir

	switch {
	case !p.On && run.State != Off:
		r.stopAgent(run)
		r.set(run, Off, detailTurnedOff)

		return true
	case p.On && run.State == Off:
		run.LeftSig, run.Error = "", ""
		r.set(run, Resting, detailTurnedOn)

		return true
	}

	return changed
}

// Resume queues a run a person wants going again: one waiting, failed or
// resting. An idle agent gets /bot again for another round.
func (r *Runner) Resume(project int64) error {
	r.mu.Lock()

	run, ok := r.runs[project]

	switch {
	case !ok || run.State == Off:
		r.mu.Unlock()

		return ErrNoRun
	case run.State == Queued || run.State == Running || run.State == Blocked:
		r.mu.Unlock()

		return fmt.Errorf("它正在%s", label(run.State))
	}

	r.queue(run, detailResumed)
	r.mu.Unlock()

	r.changed()
	r.Kick()

	return nil
}

// Kick asks the runner to look at everything now, e.g. after an agent
// changed.
func (r *Runner) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run runs the projects until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	tick := time.NewTicker(r.o.Poll)
	defer tick.Stop()

	lastPoll := time.Time{}

	for {
		if time.Since(lastPoll) >= r.o.Poll {
			r.Poll()

			lastPoll = time.Now()
		}

		r.Step()

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-r.kick:
		}
	}
}

// Poll reads the inboxes of the projects it runs, and queues those with
// news.
func (r *Runner) Poll() {
	r.mu.Lock()

	dirs := map[int64]string{}

	for id, run := range r.runs {
		if run.State != Off {
			dirs[id] = run.Dir
		}
	}
	r.mu.Unlock()

	for id, dir := range dirs {
		pending, err := r.o.Pending(dir)

		r.mu.Lock()

		if run, ok := r.runs[id]; ok {
			if err != nil {
				run.Error = err.Error()
			} else {
				r.setPending(run, pending)
				r.watch(run)
			}
		}
		r.mu.Unlock()
	}
}

// readPending reads a project's inbox now. Call it with the lock held:
// the files are local and few.
func (r *Runner) readPending(run *Run) {
	if pending, err := r.o.Pending(run.Dir); err == nil {
		r.setPending(run, pending)
	}
}

// setPending records what waits in a project's inbox. Call it with the
// lock held.
func (r *Runner) setPending(run *Run, pending []Pending) {
	r.pending[run.Project] = pending

	if run.Pending != len(pending) {
		run.Pending = len(pending)

		r.changedLater()
	}
}

// watch queues a resting run when news waits for it, or what it left has
// waited a while. Call it with the lock held.
func (r *Runner) watch(run *Run) {
	pending := r.pending[run.Project]
	news := hasNews(pending, run.LeftSig)

	switch {
	case run.State == Idle:
		r.nudge(run, news)

		return
	case run.State != Resting || len(pending) == 0:
		return
	case news:
		r.queue(run, detailNews)
	case time.Since(run.LeftAt) >= leftoverAfter:
		r.queue(run, detailLeftover)
	default:
		return
	}

	r.changedLater()
	r.Kick()
}

// nudge gives an idle agent /bot again when news waited a while and it
// did not wake for it: its wait in the background ended or never began.
// Call it with the lock held.
func (r *Runner) nudge(run *Run, news bool) {
	switch {
	case !news:
		run.newsAt = time.Time{}
	case run.newsAt.IsZero():
		run.newsAt = time.Now()
	case time.Since(run.newsAt) >= nudgeAfter:
		run.newsAt = time.Time{}
		r.queue(run, detailNudge)

		r.changedLater()
		r.Kick()
	}
}

// queue marks a run as wanting an agent. Call it with the lock held.
func (r *Runner) queue(run *Run, why string) {
	run.NextTry, run.Error = time.Time{}, ""
	r.set(run, Queued, why)
}

// set moves a run to a state. Call it with the lock held.
func (r *Runner) set(run *Run, st State, detail string) bool {
	if run.State == st && run.Detail == detail {
		return false
	}

	if run.State != st {
		r.logf(run, "%s → %s %s", label(run.State), label(st), detail)
	}

	run.State, run.Detail, run.UpdatedAt = st, detail, time.Now()

	return true
}

func (r *Runner) logf(run *Run, format string, args ...any) {
	run.Log = append(run.Log, LogLine{At: time.Now(), Text: fmt.Sprintf(format, args...)})
	if over := len(run.Log) - maxLog; over > 0 {
		run.Log = run.Log[over:]
	}
}

func (r *Runner) changed() {
	if r.o.OnChange != nil {
		r.o.OnChange()
	}
}

func (r *Runner) changedLater() {
	if r.o.OnChange != nil {
		go r.o.OnChange()
	}
}

// label is a state in words, for the log.
func label(s State) string {
	switch s {
	case Queued:
		return "排队"
	case Running:
		return "处理中"
	case Blocked:
		return "等你确认"
	case Waiting:
		return "需要你看看"
	case Idle:
		return "值守中"
	case Resting:
		return "休息"
	case Failed:
		return "失败"
	case Off:
		return "关闭"
	}

	return string(s)
}

// signature is what waits in an inbox, to tell news from what was there.
func signature(pending []Pending) string {
	parts := make([]string, 0, len(pending))
	for _, p := range pending {
		parts = append(parts, p.ID+":"+strconv.Itoa(p.Entries))
	}

	slices.Sort(parts)

	return strings.Join(parts, "\n")
}

// hasNews reports whether anything waits that did not, as it was when left
// was taken: a new task, or one that grew. One answered since is not news.
func hasNews(pending []Pending, left string) bool {
	was := map[string]bool{}
	for line := range strings.SplitSeq(left, "\n") {
		was[line] = true
	}

	for _, p := range pending {
		if !was[p.ID+":"+strconv.Itoa(p.Entries)] {
			return true
		}
	}

	return false
}

func ids(pending []Pending) []string {
	out := make([]string, 0, len(pending))
	for _, p := range pending {
		out = append(out, p.ID)
	}

	slices.Sort(out)

	return out
}

// progressed reports whether a round answered any of what waited as it
// began: a round that began with nothing to do did not fail.
func progressed(round []string, pending []Pending) bool {
	if len(round) == 0 {
		return true
	}

	left := map[string]bool{}
	for _, p := range pending {
		left[p.ID] = true
	}

	for _, id := range round {
		if !left[id] {
			return true
		}
	}

	return false
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}

	return s
}
