// Package runner runs Claude Code with claude-bot's /bot in the gateway's
// projects, so nobody has to keep a terminal open in each repo.
//
// A project whose runner is on gets one agent at a time, in its own
// folder; all projects together get at most MaxParallel. When its inbox has
// tasks, the runner starts a new Claude Code there and gives it /bot: what
// to take, in what order and how to answer is the project's
// .claude/commands/bot.md, not the runner's.
//
// One agent does one round: the batch its inbox held. It says it is done
// with `claude-bot finish` (Finish), and its Stop hook does not let it end
// its turn before (StopHook). Then the runner ends it, and all it started,
// and starts a new one for what waits, if anything does.
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
	// Waiting has its agent stopped by a person: interrupted, or a
	// permission declined. It is theirs until it finishes or they let the
	// runner go on.
	Waiting State = "waiting"
	// Resting has no agent: news starts one.
	Resting State = "resting"
	// Failed cannot start until a person fixes it and resumes it.
	Failed State = "failed"
	// Off is a project whose runner a person turned off.
	Off State = "off"
)

// stateIdle is a state of runners before one agent did one round: an
// agent waiting for news. Saved runs in it rest.
const stateIdle State = "idle"

// Live reports whether an agent holds a place for the run.
func (s State) Live() bool {
	return s == Running || s == Blocked || s == Waiting
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
	// leftoverAfter is how long what an agent left in its inbox, after a
	// round that answered none of it, waits before another round; news
	// starts one at once.
	leftoverAfter = 30 * time.Minute
	// maxBlocks is how often a round's Stop hook keeps the agent from
	// ending its turn before it finished: then it lets it.
	maxBlocks = 3
	// maxLog is how many log lines a run keeps.
	maxLog = 100
	// summaryMax bounds the last thing the agent said, kept as the summary.
	summaryMax = 500
)

// Details shown with a run.
const (
	detailStarting    = "启动中"
	detailNews        = "有新消息"
	detailMore        = "还有待办，开新的一轮"
	detailLeftover    = "还有没处理完的，再跑一轮"
	detailDone        = "这一轮处理完了"
	detailNoProgress  = "这一轮一件都没处理掉，有新消息再启动"
	detailNoFinish    = "没发完成信号就停了"
	detailSubagents   = "等子 Agent 回来"
	detailInterrupted = "这一轮被中断了"
	detailDeclined    = "拒绝授权后停下了"
	detailRest        = "agent 退出了，有新消息再启动"
	detailResuming    = "gateway 重启，重新开一轮"
	detailStartRetry  = "启动失败，稍后重试"
	detailResumed     = "手动继续"
	detailLostOften   = "agent 反复中断，不再自动恢复"
	detailAgentGone   = "agent 不见了: "
	detailInboxError  = "读不了 inbox: "
	detailTurnedOff   = "已关闭自动处理"
	detailTurnedOn    = "已打开自动处理，有待办就启动"
)

// ErrNoBotCommand reports a project where Claude Code has no /bot command.
var ErrNoBotCommand = errors.New("没有 /bot 命令：在项目里运行 `claude-bot init --bot-md` 生成 .claude/commands/bot.md")

// ErrNoRun reports a project the runner does not run.
var ErrNoRun = errors.New("这个项目没有打开自动处理")

// ErrNotARound reports an agent that does no round of the runner's, e.g.
// one a person started.
var ErrNotARound = errors.New("这个会话不是 gateway 的自动处理启动的，不用 finish")

// stopReason is what an agent's Stop hook tells it when it would end its
// turn before it finished its round.
const stopReason = "这一轮还没结束：你还没有运行 `claude-bot finish`。" +
	"把这一批的每件事都处理完（回复、`claude-bot done`，或者用 `claude-bot reply --keep` 说明进度），" +
	"然后运行 `claude-bot finish`，再结束回答。" +
	"如果你在等后台的命令，就在前台等它结束；如果在等子 Agent，等它们回来再结束。"

// Project is a gateway project, and whether its runner is on.
type Project struct {
	ID   int64
	Name string
	Dir  string
	On   bool
}

// Pending is a task waiting in an inbox: its id, how long its
// conversation is, which grows with news, and when the bot last replied
// to it.
type Pending struct {
	ID      string
	Entries int
	Replied time.Time
}

// Agents runs the agents.
type Agents interface {
	Spawn(o agent.SpawnOptions) (agent.Info, error)
	AgentInfo(id string) (agent.Info, error)
	WaitAgent(id string, timeout time.Duration)
	KillAgent(id string) error
	// Remove forgets an agent whose program ended.
	Remove(id string) error
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
	// TurnBase is the agent's turn count when its round began, or a turn
	// of it last ended; a turn after it ends the round or goes on.
	TurnBase int `json:"turn_base"`
	// Starts is how often the run was given to an agent.
	Starts int `json:"starts"`
	// Recoveries is how often a round started again after the gateway
	// went away under it, since a round last ended.
	Recoveries int `json:"recoveries,omitempty"`
	// Round is what waited as the round began, RoundSig with how long
	// each was, RoundAt when: one of them answered is progress, and each
	// must be answered since RoundAt for the agent to finish.
	Round    []string  `json:"round,omitempty"`
	RoundSig string    `json:"round_sig,omitempty"`
	RoundAt  time.Time `json:"round_at,omitzero"`
	// Finished is set once the round's agent said it is done; Blocks is
	// how often its Stop hook kept it from ending its turn before.
	Finished bool `json:"finished,omitempty"`
	Blocks   int  `json:"blocks,omitempty"`
	// LeftSig is what an agent left that does not start another round
	// at once, LeftAt when: news does, and so does the same after a
	// while. Empty, anything waiting starts one.
	LeftSig   string    `json:"left_sig,omitempty"`
	LeftAt    time.Time `json:"left_at,omitzero"`
	NextTry   time.Time `json:"next_try,omitzero"`
	UpdatedAt time.Time `json:"updated_at"`
	Log       []LogLine `json:"log,omitempty"`

	// starting is set while start hands the run to an agent.
	starting bool
}

// Saved is what the runner keeps across restarts of the gateway.
type Saved struct {
	Runs []Run `json:"runs"`
}

// Runner runs the projects' agents.
type Runner struct {
	o    Options
	kick chan struct{}

	// starts are the runs being given to agents, which Run waits for: the
	// owner ends every agent once it returns, a half-started one too.
	starts sync.WaitGroup

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
		switch {
		case run.State == stateIdle:
			// Its agent waited for news, as agents once did: what waits
			// starts a new one.
			run.State, run.LeftSig = Resting, ""
		case run.State.Live() && run.AgentID == "":
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
			r.forgetAgent(run)
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
// resting. A new agent does the round; one a person stopped ends.
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

// Run runs the projects until ctx ends, and the starts it began are done.
func (r *Runner) Run(ctx context.Context) {
	defer r.starts.Wait()

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

		if run, ok := r.runs[id]; ok && r.setPending(run, pending, err) == nil {
			r.watch(run)
		}
		r.mu.Unlock()
	}
}

// readPending reads a project's inbox now. Call it with the lock held:
// the files are local and few. If it cannot, what was read last stays.
func (r *Runner) readPending(run *Run) error {
	pending, err := r.o.Pending(run.Dir)

	return r.setPending(run, pending, err)
}

// setPending records what a read of a project's inbox found, or that it
// failed: the run shows the error until a read succeeds. Call it with the
// lock held.
func (r *Runner) setPending(run *Run, pending []Pending, err error) error {
	if err != nil {
		if msg := detailInboxError + err.Error(); run.Error != msg {
			run.Error = msg
			r.logf(run, "%s", msg)
			r.changedLater()
		}

		return err
	}

	if strings.HasPrefix(run.Error, detailInboxError) {
		run.Error = ""

		r.changedLater()
	}

	r.pending[run.Project] = pending

	if run.Pending != len(pending) {
		run.Pending = len(pending)

		r.changedLater()
	}

	return nil
}

// watch queues a resting run when anything waits that its last agent did
// not leave behind, or what it left has waited a while. Call it with the
// lock held.
func (r *Runner) watch(run *Run) {
	pending := r.pending[run.Project]

	switch {
	case run.State != Resting || len(pending) == 0:
		return
	case run.LeftSig == "":
		r.queue(run, detailMore)
	case hasNews(pending, run.LeftSig):
		r.queue(run, detailNews)
	case time.Since(run.LeftAt) >= leftoverAfter:
		r.queue(run, detailLeftover)
	default:
		return
	}

	r.changedLater()
	r.Kick()
}

// Finish records that the agent says its round is done. It refuses while
// a task of the round has had no word from the bot since the round began:
// a reply, a reply that keeps it open, or done.
func (r *Runner) Finish(agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := r.roundOf(agentID)
	if run == nil {
		return ErrNotARound
	}

	if err := r.readPending(run); err != nil {
		// What it answered is not known: it tries again.
		return fmt.Errorf("%s%w。过一会儿再运行 `claude-bot finish`", detailInboxError, err)
	}

	var left []string

	for _, p := range r.pending[run.Project] {
		if slices.Contains(run.Round, p.ID) && p.Replied.Before(run.RoundAt) {
			left = append(left, p.ID)
		}
	}

	if len(left) > 0 {
		slices.Sort(left)

		return fmt.Errorf("这一批还有 %d 件没处理：%s。每件都回复、`claude-bot done`，或者用 `claude-bot reply --keep` 说明进度，再运行 `claude-bot finish`",
			len(left), strings.Join(left, " "))
	}

	if !run.Finished {
		run.Finished = true
		r.logf(run, "agent 说这一轮处理完了")
		r.changedLater()
	}

	return nil
}

// StopHook decides whether the agent may end its turn, from its Stop hook,
// and if not, what to tell it. It may once it finished its round, while a
// subagent of it runs (its turn goes on when it reports), when a person
// stopped it, or when it was told maxBlocks times. Agents the runner does
// not run always may.
func (r *Runner) StopHook(agentID string, subagents int) (block bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	run := r.roundOf(agentID)
	if run == nil || run.Finished || subagents > 0 || run.State == Waiting {
		return false, ""
	}

	if run.Blocks >= maxBlocks {
		r.logf(run, "拦了 %d 次还是没发完成信号，让它停下", run.Blocks)

		return false, ""
	}

	run.Blocks++
	r.logf(run, "它要停下但还没发完成信号，拦下（第 %d 次）", run.Blocks)
	r.changedLater()

	return true, stopReason
}

// roundOf is the run whose round the agent does. Call it with the lock
// held.
func (r *Runner) roundOf(agentID string) *Run {
	if agentID == "" {
		return nil
	}

	for _, run := range r.runs {
		if run.AgentID == agentID && run.State.Live() {
			return run
		}
	}

	return nil
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

	switch {
	case run.State == "":
		r.logf(run, "%s %s", label(st), detail)
	case run.State != st:
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
	case Resting:
		return "休息"
	case Failed:
		return "失败"
	case Off:
		return "关闭"
	case stateIdle:
		// Only saved runs have it, and New lets them rest.
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
