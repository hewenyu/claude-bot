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
			return r.turnEnded(run, info)
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

// working follows an agent at work. One a person stopped is theirs while
// they talk to it. Call it with the lock held.
func (r *Runner) working(run *Run, info agent.Info) bool {
	switch {
	case run.State == Waiting:
		return false
	case info.Status == agent.Blocked:
		return r.set(run, Blocked, info.Detail)
	}

	return r.set(run, Running, "")
}

// beginRound notes what waits as a round begins. Call it with the lock
// held.
func (r *Runner) beginRound(run *Run, turns int) {
	r.readPending(run)

	pending := r.pending[run.Project]
	run.TurnBase, run.Round, run.RoundSig, run.RoundAt = turns, ids(pending), signature(pending), time.Now()
	run.Finished, run.Blocks = false, 0
}

// turnEnded follows an agent that ended a turn: its round is over unless
// a person stopped it, or a subagent of it still runs. Call it with the
// lock held.
func (r *Runner) turnEnded(run *Run, info agent.Info) bool {
	run.TurnBase = info.Turns
	run.Summary = clip(info.LastMessage, summaryMax)

	switch {
	case info.Ended == agent.EndInterrupt:
		return r.set(run, Waiting, detailInterrupted)
	case info.Ended == agent.EndDeclined:
		return r.set(run, Waiting, detailDeclined)
	case run.Finished:
	case run.State == Waiting:
		// A person talks to it.
		return false
	case info.Subagents > 0:
		// It goes on when they report.
		return r.set(run, Running, detailSubagents)
	}

	r.endRound(run)

	return true
}

// endRound ends a round's agent, and all it started, and leaves the run to
// rest: what waits starts the next round at once if this one answered any
// of its tasks; else only news does, or the same after a while. Call it
// with the lock held.
func (r *Runner) endRound(run *Run) {
	r.readPending(run)

	pending := r.pending[run.Project]
	progress := progressed(run.Round, pending)

	// Nothing of what it was given answered, e.g. all of it waits for CI:
	// what it was given, as it was, starts no round; a reply to it does.
	run.LeftSig = ""
	if !progress {
		run.LeftSig = run.RoundSig
	}

	detail := detailDone

	switch {
	case run.Finished:
	case progress:
		detail = detailNoFinish
	default:
		detail = detailNoProgress
	}

	r.logf(run, "这一轮结束（%s），inbox 里还有 %d 件", detail, len(pending))

	run.LeftAt, run.Recoveries, run.Round = time.Now(), 0, nil
	r.stopAgent(run)
	r.set(run, Resting, detail)
	r.watch(run)
}

// agentEnded follows a run whose agent's program ended under it. Call it
// with the lock held.
func (r *Runner) agentEnded(run *Run, info agent.Info) bool {
	working := run.State == Running || run.State == Blocked
	if info.Status == agent.Lost && working {
		if run.Recoveries >= maxRecoveries {
			run.Error = detailLostOften

			return r.set(run, Failed, detailLostOften)
		}

		// The gateway restarted under it: a new agent does the round.
		run.Recoveries++
		run.NextTry = time.Time{}
		r.logf(run, "agent 随 gateway 中断，重新开一轮")

		return r.set(run, Queued, detailResuming)
	}

	// Its program ended, by a person or right away: news starts it
	// again, not what waits now, or a program that cannot start would
	// start again on every poll.
	r.readPending(run)
	run.LeftSig, run.LeftAt = signature(r.pending[run.Project]), time.Now()

	return r.set(run, Resting, detailRest)
}

// stopAgent ends a run's agent and what it started. Call it with the lock
// held.
func (r *Runner) stopAgent(run *Run) {
	if run.AgentID == "" {
		return
	}

	id := run.AgentID
	run.Stopping = true

	go func() { _ = r.o.Agents.KillAgent(id) }()
}

// forgetAgent ends a run's agent, if it runs, and forgets it. Call it with
// the lock held.
func (r *Runner) forgetAgent(run *Run) {
	if run.AgentID == "" {
		return
	}

	id := run.AgentID

	go func() {
		_ = r.o.Agents.KillAgent(id)
		r.o.Agents.WaitAgent(id, stopWait)
		_ = r.o.Agents.Remove(id)
	}()
}

// startable picks the queued runs that can start, the longest waiting
// first, and marks them running. All projects together run maxParallel
// agents. Call it with the lock held.
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
		if run.State != Queued || now.Before(run.NextTry) || active >= r.maxParallel {
			continue
		}

		active++
		run.starting = true
		r.set(run, Running, detailStarting)
		starts = append(starts, run)
	}

	return starts
}

// start gives a run to a new agent.
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
			r.forgetAgent(run)
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

// launch starts a new agent for a run, once the one before it, if any,
// ended (a person's, one they stopped, is ended now) and is forgotten.
func (r *Runner) launch(c Run) (agent.Info, error) {
	if err := r.o.Check(c.Dir); err != nil {
		return agent.Info{}, err
	}

	if err := r.endBefore(c.AgentID); err != nil {
		return agent.Info{}, err
	}

	return r.o.Agents.Spawn(agent.SpawnOptions{Name: c.Name, Cwd: c.Dir, Project: c.Project, Prompt: BotCommand})
}

// endBefore ends the agent a run had, if it still runs, and forgets it.
func (r *Runner) endBefore(id string) error {
	if id == "" {
		return nil
	}

	info, err := r.o.Agents.AgentInfo(id)
	if err != nil {
		return nil //nolint:nilerr // gone already
	}

	if info.Status.Live() {
		_ = r.o.Agents.KillAgent(id)
	}

	r.o.Agents.WaitAgent(id, stopWait)

	if err := r.o.Agents.Remove(id); err != nil {
		return fmt.Errorf("agent %s 还没退出: %w", id, err)
	}

	return nil
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
