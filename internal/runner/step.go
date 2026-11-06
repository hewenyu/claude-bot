package runner

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
)

// Step brings every run up to date with its agent and starts what can
// start.
func (r *Runner) Step() {
	r.mu.Lock()

	changed := false

	for _, run := range r.sorted() {
		if r.follow(run) {
			changed = true
		}
	}

	starts := r.startable()
	r.mu.Unlock()

	for _, run := range starts {
		r.start(run)

		changed = true
	}

	if changed {
		r.changed()
	}
}

// follow updates a run from its agent. Call it with the lock held.
func (r *Runner) follow(run *Run) bool {
	if !run.State.Live() || run.AgentID == "" || run.starting {
		return false
	}

	info, err := r.o.Agents.AgentInfo(run.AgentID)
	if err != nil {
		run.AgentID, run.Error = "", err.Error()

		return r.set(run, Failed, detailAgentGone+err.Error())
	}

	switch info.Status {
	case agent.Starting, agent.Working, agent.Blocked:
		return r.working(run, info)
	case agent.Idle:
		if info.Turns > run.TurnBase {
			return r.roundEnded(run, info)
		}

		if run.State == Running && run.Detail == detailStarting {
			// Started, not yet on its message.
			return r.set(run, Running, "")
		}
	case agent.Exited, agent.Lost:
		return r.agentEnded(run, info)
	}

	return false
}

// working follows an agent at work. Call it with the lock held.
func (r *Runner) working(run *Run, info agent.Info) bool {
	woke := false

	if run.State == Idle || run.State == Waiting {
		// It began a round itself: news woke its /bot, or a person typed
		// to it.
		r.beginRound(run, info.Turns)
		r.logf(run, "开始新一轮")

		woke = true
	}

	if info.Status == agent.Blocked {
		return r.set(run, Blocked, info.Detail) || woke
	}

	return r.set(run, Running, "") || woke
}

// beginRound notes what waits as a round begins. Call it with the lock
// held.
func (r *Runner) beginRound(run *Run, turns int) {
	r.readPending(run)
	run.TurnBase, run.Round, run.newsAt = turns, ids(r.pending[run.Project]), time.Time{}
}

// roundEnded follows an agent that went through its inbox: /bot waits for
// news in the background. Call it with the lock held.
func (r *Runner) roundEnded(run *Run, info agent.Info) bool {
	run.TurnBase, run.Recoveries = info.Turns, 0
	run.Summary = clip(info.LastMessage, summaryMax)
	r.readPending(run)
	r.left(run)

	pending := r.pending[run.Project]
	r.logf(run, "这一轮结束，inbox 里还有 %d 件", len(pending))

	switch {
	case info.Ended == agent.EndInterrupt:
		r.set(run, Waiting, detailInterrupted)
	case info.Ended == agent.EndDeclined:
		r.set(run, Waiting, detailDeclined)
	case !progressed(run.Round, pending):
		r.set(run, Idle, detailNoProgress)
	default:
		r.set(run, Idle, detailWatching)
	}

	run.Round = nil

	return true
}

// left notes what an agent leaves in its inbox as it ends a round or its
// program. Call it with the lock held.
func (r *Runner) left(run *Run) {
	run.LeftSig, run.LeftAt = signature(r.pending[run.Project]), time.Now()
}

// agentEnded follows a run whose agent's program ended. Call it with the
// lock held.
func (r *Runner) agentEnded(run *Run, info agent.Info) bool {
	working := run.State == Running || run.State == Blocked
	if info.Status == agent.Lost && working {
		if run.Recoveries >= maxRecoveries {
			run.Error = detailLostOften

			return r.set(run, Failed, detailLostOften)
		}

		// The gateway restarted under it: resume where it was.
		run.Recoveries++
		run.NextTry = time.Time{}
		r.logf(run, "agent 随 gateway 中断，重新排队恢复")

		return r.set(run, Queued, detailResuming)
	}

	// Its program ended, by a person, after a round, or right away: news
	// starts it again, not what waits now, or a program that cannot start
	// would start again on every poll.
	r.readPending(run)
	r.left(run)

	return r.set(run, Resting, detailRest)
}

// stopAgent ends a run's agent. Call it with the lock held.
func (r *Runner) stopAgent(run *Run) {
	if run.AgentID == "" {
		return
	}

	id := run.AgentID
	run.Stopping = true

	go func() { _ = r.o.Agents.KillAgent(id) }()
}

// startable picks the queued runs that can start, the longest waiting
// first, and marks them running. All projects together run maxParallel
// agents; an idle one gives its place to a run that needs it. Call it with
// the lock held.
func (r *Runner) startable() []*Run {
	active := 0

	for _, run := range r.runs {
		if run.State.Live() {
			active++
		}
	}

	var starts []*Run

	now := time.Now()

	for _, run := range r.sorted() {
		if run.State != Queued || now.Before(run.NextTry) {
			continue
		}

		if active >= r.maxParallel {
			if !r.yield(r.idlest()) {
				continue
			}

			active--
		}

		active++
		run.starting = true
		r.set(run, Running, detailStarting)
		starts = append(starts, run)
	}

	return starts
}

// idlest is the run whose agent is idle the longest. Call it with the
// lock held.
func (r *Runner) idlest() *Run {
	var idle *Run

	for _, run := range r.runs {
		if run.State == Idle && (idle == nil || run.UpdatedAt.Before(idle.UpdatedAt)) {
			idle = run
		}
	}

	return idle
}

// yield ends an idle agent so another run gets its place, and reports
// whether it did. Call it with the lock held.
func (r *Runner) yield(run *Run) bool {
	if run == nil || run.State != Idle {
		return false
	}

	r.stopAgent(run)
	r.set(run, Resting, detailYield)

	return true
}

// start gives a run to an agent: the one it had, given /bot again or
// resumed with its conversation, or a new one.
func (r *Runner) start(run *Run) {
	r.mu.Lock()
	c := *run
	r.mu.Unlock()

	info, err := r.launch(c)

	r.mu.Lock()
	defer r.mu.Unlock()

	run.starting = false

	if r.runs[run.Project] != run || run.State != Running {
		// Turned off or gone while it started: the agent has nothing to
		// do.
		if err == nil {
			run.AgentID = info.ID
			r.stopAgent(run)
		}

		return
	}

	if err != nil {
		r.startFailed(run, err)

		return
	}

	run.AgentID, run.Stopping, run.Error = info.ID, false, ""
	run.Starts++
	r.beginRound(run, info.Turns)
	r.logf(run, "交给 agent %s", info.ID)
}

// startFailed follows a start that failed. Call it with the lock held.
func (r *Runner) startFailed(run *Run, err error) {
	run.Error = err.Error()

	if errors.Is(err, ErrNoBotCommand) {
		// Waiting will not make the command appear.
		r.set(run, Failed, err.Error())

		return
	}

	run.NextTry = time.Now().Add(retryAfter)
	r.logf(run, "启动失败: %v", err)
	r.set(run, Queued, detailStartRetry)
}

// launch starts a run's agent: live and idle, it gets /bot as a message;
// ended with a saved conversation, it resumes; else a new one starts.
func (r *Runner) launch(c Run) (agent.Info, error) {
	if err := r.o.Check(c.Dir); err != nil {
		return agent.Info{}, err
	}

	if c.AgentID != "" {
		if c.Stopping {
			r.o.Agents.WaitAgent(c.AgentID, stopWait)
		}

		info, err := r.o.Agents.AgentInfo(c.AgentID)

		switch {
		case err != nil:
		case info.Status == agent.Idle && !c.Stopping:
			return info, r.o.Agents.SendText(c.AgentID, BotCommand)
		case info.Status.Live():
			return agent.Info{}, fmt.Errorf("agent %s 还在运行（%s）", c.AgentID, info.Status)
		case info.Resumable():
			return r.o.Agents.Resume(c.AgentID, BotCommand)
		}
	}

	return r.o.Agents.Spawn(agent.SpawnOptions{Name: c.Name, Cwd: c.Dir, Project: c.Project, Prompt: BotCommand})
}

// sorted is the runs, the one changed longest ago first. Call it with the
// lock held.
func (r *Runner) sorted() []*Run {
	out := make([]*Run, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run)
	}

	slices.SortFunc(out, func(a, b *Run) int {
		if c := a.UpdatedAt.Compare(b.UpdatedAt); c != 0 {
			return c
		}

		return int(a.Project - b.Project)
	})

	return out
}
