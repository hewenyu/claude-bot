package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
)

// fakeAgents keeps agents as plain records the test changes by hand.
type fakeAgents struct {
	mu     sync.Mutex
	n      int
	agents map[string]*agent.Info
	// calls records what the runner asked, e.g. "spawn a1: /bot".
	calls []string
}

func newFakeAgents() *fakeAgents {
	return &fakeAgents{agents: map[string]*agent.Info{}}
}

func (f *fakeAgents) Spawn(o agent.SpawnOptions) (agent.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.n++
	id := fmt.Sprintf("a%d", f.n)
	f.agents[id] = &agent.Info{ID: id, Cwd: o.Cwd, Project: o.Project, SessionID: "s-" + id, Status: agent.Starting}
	f.calls = append(f.calls, "spawn "+id+" "+o.Cwd+": "+o.Prompt)

	return *f.agents[id], nil
}

func (f *fakeAgents) AgentInfo(id string) (agent.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.agents[id]
	if !ok {
		return agent.Info{}, agent.ErrNotFound
	}

	return *a, nil
}

func (f *fakeAgents) WaitAgent(id string, _ time.Duration) {
	for range 200 {
		if a, _ := f.AgentInfo(id); !a.Status.Live() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fakeAgents) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.agents[id]
	switch {
	case !ok:
		return agent.ErrNotFound
	case a.Status.Live():
		return agent.ErrLive
	}

	delete(f.agents, id)
	f.calls = append(f.calls, "remove "+id)

	return nil
}

func (f *fakeAgents) KillAgent(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.agents[id]
	if !ok {
		return agent.ErrNotFound
	}

	a.Status = agent.Exited

	f.calls = append(f.calls, "kill "+id)

	return nil
}

func (f *fakeAgents) set(id string, st agent.Status, turns int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	a := f.agents[id]
	a.Status, a.Turns = st, turns
	a.LastMessage = fmt.Sprintf("turn %d", turns)
}

// endTurn ends the agent's turn, the way ended says.
func (f *fakeAgents) endTurn(id string, turns int, ended agent.End) {
	f.set(id, agent.Idle, turns)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.agents[id].Ended = ended
}

func (f *fakeAgents) setSubagents(id string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.agents[id].Subagents = n
}

// waitRemoved waits for the runner to forget an agent in the background.
func (f *fakeAgents) waitRemoved(t *testing.T, id string) {
	t.Helper()

	for range 200 {
		if _, err := f.AgentInfo(id); err != nil {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("agent %s was not removed", id)
}

// count is how many calls start with prefix.
func (f *fakeAgents) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}

	return n
}

// waitKilled waits for the kill a runner sends in the background.
func (f *fakeAgents) waitKilled(t *testing.T, id string) {
	t.Helper()

	f.WaitAgent(id, 0)

	if a, _ := f.AgentInfo(id); a.Status.Live() {
		t.Fatalf("agent %s was not killed", id)
	}
}

// fakeInbox is the projects' inboxes, by folder.
type fakeInbox struct {
	mu    sync.Mutex
	tasks map[string][]Pending
	// err, if set, is what reading any inbox fails with.
	err error
}

func (i *fakeInbox) set(dir string, tasks ...Pending) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.tasks[dir] = tasks
}

func (i *fakeInbox) fail(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.err = err
}

func (i *fakeInbox) pending(dir string) ([]Pending, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.err != nil {
		return nil, i.err
	}

	return i.tasks[dir], nil
}

type fixture struct {
	r     *Runner
	a     *fakeAgents
	inbox *fakeInbox
}

func newFixture(t *testing.T, check func(string) error, projects ...Project) *fixture {
	t.Helper()

	f := &fixture{a: newFakeAgents(), inbox: &fakeInbox{tasks: map[string][]Pending{}}}
	f.r = New(Options{Agents: f.a, Pending: f.inbox.pending, Check: check}, Saved{})
	f.r.SetProjects(projects)

	return f
}

// tick reads the inboxes and steps once, and waits for the starts.
func (f *fixture) tick() {
	f.r.Poll()
	f.r.Step()
	f.r.starts.Wait()
}

func (f *fixture) run(t *testing.T, project int64) Run {
	t.Helper()

	for _, run := range f.r.Saved().Runs {
		if run.Project == project {
			return run
		}
	}

	t.Fatalf("no run for project %d", project)

	return Run{}
}

func (f *fixture) expect(t *testing.T, project int64, st State) Run {
	t.Helper()

	run := f.run(t, project)
	if run.State != st {
		t.Fatalf("project %d is %s (%s), want %s; log %v", project, run.State, run.Detail, st, run.Log)
	}

	return run
}

func on(id int64) Project {
	return Project{ID: id, Name: fmt.Sprintf("p%d", id), Dir: fmt.Sprintf("/repo/%d", id), On: true}
}

func task(id string, entries int) Pending {
	return Pending{ID: id, Entries: entries}
}

func TestOffProjectsHaveNoRun(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, Project{ID: 1, Dir: "/repo/1"})
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	if runs := f.r.Saved().Runs; len(runs) != 0 {
		t.Fatalf("runs = %v", runs)
	}
}

func replied(id string, entries int) Pending {
	return Pending{ID: id, Entries: entries, Replied: time.Now()}
}

// finish has the agent finish its round, and fails the test if it may not.
func (f *fixture) finish(t *testing.T, id string) {
	t.Helper()

	if err := f.r.Finish(id); err != nil {
		t.Fatal(err)
	}
}

func TestARoundEndsItsAgentAndTheNextIsNew(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.tick()
	f.expect(t, 1, Resting)

	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	run := f.expect(t, 1, Running)
	if f.a.count("spawn a1 /repo/1: /bot") != 1 || run.Starts != 1 || len(run.Round) != 1 {
		t.Fatalf("calls %v, run %+v", f.a.calls, run)
	}

	f.a.set("a1", agent.Working, 0)
	f.tick()

	// Not before it answered what it was given.
	if err := f.r.Finish("a1"); err == nil || !strings.Contains(err.Error(), "t1") {
		t.Fatalf("finish = %v", err)
	}

	// t1 answered; t2 came meanwhile.
	f.inbox.set("/repo/1", task("t2", 1))
	f.finish(t, "a1")
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a1")

	// What waits starts the next round at once, with a new agent.
	f.tick()

	run = f.expect(t, 1, Running)
	if run.AgentID != "a2" || run.Summary != "turn 1" || run.Finished || run.Starts != 2 {
		t.Fatalf("run %+v", run)
	}

	if f.a.count("remove a1") != 1 || f.a.count("spawn a2 /repo/1: /bot") != 1 {
		t.Fatalf("calls %v", f.a.calls)
	}

	// Nothing left: it rests.
	f.inbox.set("/repo/1")
	f.finish(t, "a2")
	f.a.endTurn("a2", 1, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a2")
	f.tick()

	if run := f.expect(t, 1, Resting); run.Detail != detailDone {
		t.Fatalf("detail %q", run.Detail)
	}
}

func TestFinishTakesAReplyThatKeepsItOpen(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1), task("t2", 1))
	f.tick()

	f.inbox.set("/repo/1", replied("t1", 1), task("t2", 1))

	if err := f.r.Finish("a1"); err == nil || strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), "t2") {
		t.Fatalf("finish = %v", err)
	}

	f.inbox.set("/repo/1", replied("t1", 1))
	f.finish(t, "a1")

	if err := f.r.Finish("nobody"); !errors.Is(err, ErrNotARound) {
		t.Fatalf("finish of no round = %v", err)
	}
}

func TestStopHook(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	if block, _ := f.r.StopHook("someone", 0); block {
		t.Fatal("an agent of no round was kept going")
	}

	// A subagent runs: its turn goes on when it reports.
	if block, _ := f.r.StopHook("a1", 1); block {
		t.Fatal("kept going while a subagent runs")
	}

	for i := range maxBlocks {
		block, reason := f.r.StopHook("a1", 0)
		if !block || !strings.Contains(reason, "claude-bot finish") {
			t.Fatalf("stop %d: block %v, reason %q", i, block, reason)
		}
	}

	if block, _ := f.r.StopHook("a1", 0); block {
		t.Fatalf("kept going more than %d times", maxBlocks)
	}

	// It stopped without finishing: the round is over all the same.
	f.a.endTurn("a1", 1, agent.EndStop)
	f.inbox.set("/repo/1")
	f.tick()
	f.a.waitKilled(t, "a1")

	if run := f.expect(t, 1, Resting); run.Detail != detailNoFinish {
		t.Fatalf("detail %q", run.Detail)
	}

	// A new round starts afresh.
	f.inbox.set("/repo/1", task("t2", 1))
	f.tick()
	f.tick()
	f.expect(t, 1, Running)

	if run := f.run(t, 1); run.Blocks != 0 || run.Finished {
		t.Fatalf("run %+v", run)
	}

	f.inbox.set("/repo/1")
	f.finish(t, "a2")

	if block, _ := f.r.StopHook("a2", 0); block {
		t.Fatal("kept a finished agent going")
	}
}

func TestASubagentKeepsTheRound(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	f.a.setSubagents("a1", 1)
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()

	if run := f.expect(t, 1, Running); run.Detail != detailSubagents || f.a.count("kill") != 0 {
		t.Fatalf("run %+v, calls %v", run, f.a.calls)
	}

	// It reported: the agent goes on, and finishes.
	f.a.setSubagents("a1", 0)
	f.a.set("a1", agent.Working, 1)
	f.tick()
	f.inbox.set("/repo/1")
	f.finish(t, "a1")
	f.a.endTurn("a1", 2, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a1")
	f.expect(t, 1, Resting)
}

func TestNoProgressWaitsForNews(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a1")
	f.tick()

	if run := f.expect(t, 1, Resting); run.Detail != detailNoProgress {
		t.Fatalf("detail = %q", run.Detail)
	}

	// Someone wrote more.
	f.inbox.set("/repo/1", task("t1", 2))
	f.tick()
	f.tick()
	f.expect(t, 1, Running)
}

func TestAFinishedRoundThatLeftAllWaiting(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	// It said it waits for CI, and finished.
	f.inbox.set("/repo/1", replied("t1", 1))
	f.finish(t, "a1")
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a1")
	f.tick()

	if run := f.expect(t, 1, Resting); run.Detail != detailDone || f.a.count("spawn") != 1 {
		t.Fatalf("run %+v, calls %v", run, f.a.calls)
	}
}

func TestAPersonStoppedIt(t *testing.T) {
	t.Parallel()

	for _, ended := range []agent.End{agent.EndInterrupt, agent.EndDeclined} {
		t.Run(string(ended), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil, on(1))
			f.inbox.set("/repo/1", task("t1", 1))
			f.tick()
			f.a.endTurn("a1", 1, ended)
			f.tick()
			f.expect(t, 1, Waiting)

			// They talk to it: it is theirs.
			f.a.set("a1", agent.Working, 1)
			f.tick()
			f.expect(t, 1, Waiting)

			if block, _ := f.r.StopHook("a1", 0); block {
				t.Fatal("kept a person's agent going")
			}

			f.a.endTurn("a1", 2, agent.EndStop)
			f.tick()
			f.expect(t, 1, Waiting)

			// They let the runner go on: a new agent does the round.
			if err := f.r.Resume(1); err != nil {
				t.Fatal(err)
			}

			f.tick()
			f.expect(t, 1, Running)

			if f.a.count("kill a1") != 1 || f.a.count("remove a1") != 1 || f.a.count("spawn a2") != 1 {
				t.Fatalf("calls %v", f.a.calls)
			}
		})
	}
}

func TestAPersonFinishesItsRound(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.a.endTurn("a1", 1, agent.EndInterrupt)
	f.tick()
	f.expect(t, 1, Waiting)

	// Told to go on, it finishes.
	f.a.set("a1", agent.Working, 1)
	f.tick()
	f.inbox.set("/repo/1")
	f.finish(t, "a1")
	f.a.endTurn("a1", 2, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a1")
	f.expect(t, 1, Resting)
}

func TestRestWaitsForNews(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1), task("t2", 1))
	f.tick()
	f.a.set("a1", agent.Exited, 0)
	f.tick()
	f.expect(t, 1, Resting)

	// What it left, and one answered since, is no news.
	f.tick()
	f.inbox.set("/repo/1", task("t2", 1))
	f.tick()
	f.expect(t, 1, Resting)

	// A reply to what it left is.
	f.inbox.set("/repo/1", task("t2", 2))
	f.tick()
	f.expect(t, 1, Running)

	if f.a.count("remove a1") != 1 || f.a.count("spawn a2") != 1 {
		t.Fatalf("calls %v", f.a.calls)
	}
}

func TestLeftoverAfterAWhile(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.a.set("a1", agent.Exited, 0)
	f.tick()
	f.expect(t, 1, Resting)

	f.r.mu.Lock()
	f.r.runs[1].LeftAt = time.Now().Add(-leftoverAfter)
	f.r.mu.Unlock()

	f.tick()

	if run := f.expect(t, 1, Running); run.Starts != 2 {
		t.Fatalf("starts = %d", run.Starts)
	}
}

func TestLostAgentStartsTheRoundAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	for i := range maxRecoveries {
		id := fmt.Sprintf("a%d", i+1)
		f.a.set(id, agent.Lost, 0)
		f.tick()

		run := f.expect(t, 1, Running)
		if run.Recoveries != i+1 || run.AgentID != fmt.Sprintf("a%d", i+2) {
			t.Fatalf("run %+v", run)
		}
	}

	f.a.set(fmt.Sprintf("a%d", maxRecoveries+1), agent.Lost, 0)
	f.tick()
	f.expect(t, 1, Failed)

	if n := f.a.count("spawn"); n != maxRecoveries+1 || f.a.count("remove") != maxRecoveries {
		t.Fatalf("calls %v", f.a.calls)
	}

	// A person resumes it.
	if err := f.r.Resume(1); err != nil {
		t.Fatal(err)
	}

	f.tick()
	f.expect(t, 1, Running)
}

func TestNoBotCommandFails(t *testing.T) {
	t.Parallel()

	missing := true
	check := func(string) error {
		if missing {
			return fmt.Errorf("in /repo/1: %w", ErrNoBotCommand)
		}

		return nil
	}

	f := newFixture(t, check, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	if run := f.expect(t, 1, Failed); !strings.Contains(run.Error, "bot-md") {
		t.Fatalf("error = %q", run.Error)
	}

	f.tick()
	f.expect(t, 1, Failed)

	missing = false

	if err := f.r.Resume(1); err != nil {
		t.Fatal(err)
	}

	f.tick()
	f.expect(t, 1, Running)
}

func TestStartRetries(t *testing.T) {
	t.Parallel()

	f := newFixture(t, func(string) error { return errors.New("boom") }, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	run := f.expect(t, 1, Queued)
	if run.Detail != detailStartRetry || run.NextTry.IsZero() || run.Error != "boom" {
		t.Fatalf("run %+v", run)
	}
}

func TestTurnOffAndRemove(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	f.r.SetProjects([]Project{{ID: 1, Dir: "/repo/1"}})
	f.a.waitKilled(t, "a1")
	f.tick()
	f.expect(t, 1, Off)

	if err := f.r.Resume(1); !errors.Is(err, ErrNoRun) {
		t.Fatalf("resume = %v", err)
	}

	f.r.SetProjects([]Project{on(1)})
	f.tick()
	f.expect(t, 1, Running)

	f.r.SetProjects(nil)
	f.a.waitRemoved(t, "a2")

	if runs := f.r.Saved().Runs; len(runs) != 0 {
		t.Fatalf("runs = %v", runs)
	}
}

func TestSavedRunsCarryOn(t *testing.T) {
	t.Parallel()

	a := newFakeAgents()
	inbox := &fakeInbox{tasks: map[string][]Pending{}}
	saved := Saved{Runs: []Run{
		{Project: 1, Dir: "/repo/1", State: Running},
		{Project: 2, Dir: "/repo/2", State: Waiting, AgentID: "gone"},
		// An agent of an older gateway waited for news.
		{Project: 3, Dir: "/repo/3", State: stateIdle, AgentID: "old", LeftSig: "t:1"},
	}}

	r := New(Options{Agents: a, Pending: inbox.pending}, saved)
	r.SetProjects([]Project{on(1), on(2), on(3)})
	r.Step()
	r.starts.Wait()

	if runs := r.Saved().Runs; runs[2].State != Resting || runs[2].LeftSig != "" {
		t.Fatalf("run 3 %+v", runs[2])
	}

	runs := r.Saved().Runs
	if runs[0].State != Running || a.count("spawn a1 /repo/1") != 1 {
		t.Fatalf("run 1 %+v, calls %v", runs[0], a.calls)
	}

	if runs[1].State != Failed || !strings.Contains(runs[1].Detail, detailAgentGone) {
		t.Fatalf("run 2 %+v", runs[1])
	}
}

func TestMaxParallel(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1), on(2), on(3))
	for _, n := range []int{0, MaxParallelLimit + 1} {
		if err := f.r.SetMaxParallel(n); err == nil {
			t.Fatalf("max %d was taken", n)
		}
	}

	if err := f.r.SetMaxParallel(2); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/repo/1", "/repo/2", "/repo/3"} {
		f.inbox.set(p, task("t", 1))
	}

	f.tick()

	if n := f.a.count("spawn"); n != 2 || f.r.MaxParallel() != 2 {
		t.Fatalf("spawned %d", n)
	}

	// A round that ends gives its place.
	a1, _ := f.a.AgentInfo("a1")
	f.inbox.set(a1.Cwd)
	f.finish(t, "a1")
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()
	f.tick()

	if n := f.a.count("spawn"); n != 3 {
		t.Fatalf("spawned %d, calls %v", n, f.a.calls)
	}
}

func TestHasNewsAndProgress(t *testing.T) {
	t.Parallel()

	left := signature([]Pending{task("a", 1), task("b", 2)})

	tests := []struct {
		name    string
		pending []Pending
		news    bool
	}{
		{"same", []Pending{task("b", 2), task("a", 1)}, false},
		{"one answered", []Pending{task("a", 1)}, false},
		{"nothing", nil, false},
		{"a new one", []Pending{task("a", 1), task("c", 1)}, true},
		{"a reply", []Pending{task("a", 2)}, true},
	}

	for _, tt := range tests {
		if got := hasNews(tt.pending, left); got != tt.news {
			t.Errorf("%s: news = %v", tt.name, got)
		}
	}

	if !progressed(nil, []Pending{task("a", 1)}) || progressed([]string{"a"}, []Pending{task("a", 2)}) ||
		!progressed([]string{"a", "b"}, []Pending{task("b", 1)}) {
		t.Fatal("progressed is wrong")
	}
}

func TestASlowStartHoldsUpNoOtherRun(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	slow := func(dir string) error {
		if dir == "/repo/1" {
			<-release // e.g. its agent before takes long to end
		}

		return nil
	}

	f := newFixture(t, slow, on(1), on(2))
	f.inbox.set("/repo/1", task("t1", 1))
	f.inbox.set("/repo/2", task("t2", 1))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		f.r.Run(ctx)
	}()

	for f.run(t, 2).AgentID == "" {
		time.Sleep(5 * time.Millisecond)
	}

	if run := f.run(t, 1); run.AgentID != "" || run.State != Running {
		t.Fatalf("run 1 %+v", run)
	}

	// Run returns once the start it began is done, so that what ends
	// every agent after it ends this one too.
	cancel()

	select {
	case <-done:
		t.Fatal("Run returned while a start was under way")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-done

	if run := f.run(t, 1); run.AgentID == "" {
		t.Fatalf("run 1 %+v", run)
	}
}

func TestInboxErrorsShowUntilARead(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	disk := errors.New("disk on fire")
	f.inbox.fail(disk)

	if err := f.r.Finish("a1"); !errors.Is(err, disk) {
		t.Fatalf("finish: %v", err)
	}

	f.tick()

	if run := f.run(t, 1); run.Error != detailInboxError+disk.Error() {
		t.Fatalf("error %q", run.Error)
	}

	f.inbox.fail(nil)
	f.tick()

	if run := f.run(t, 1); run.Error != "" {
		t.Fatalf("error %q after a read", run.Error)
	}
}
