package runner

import (
	"errors"
	"fmt"
	"os"
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
	// Any file that exists stands for its saved conversation.
	f.agents[id] = &agent.Info{ID: id, Cwd: o.Cwd, Project: o.Project, SessionID: "s-" + id, Transcript: os.TempDir(), Status: agent.Starting}
	f.calls = append(f.calls, "spawn "+id+" "+o.Cwd+": "+o.Prompt)

	return *f.agents[id], nil
}

func (f *fakeAgents) Resume(id, prompt string) (agent.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	a := f.agents[id]
	a.Status = agent.Starting

	f.calls = append(f.calls, "resume "+id+": "+prompt)

	return *a, nil
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

func (f *fakeAgents) SendText(id, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.agents[id].Status != agent.Idle {
		return agent.ErrBusy
	}

	f.calls = append(f.calls, "send "+id+": "+text)

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
func (f *fakeAgents) endTurn(id string, turns int, ended string) {
	f.set(id, agent.Idle, turns)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.agents[id].Ended = ended
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
}

func (i *fakeInbox) set(dir string, tasks ...Pending) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.tasks[dir] = tasks
}

func (i *fakeInbox) pending(dir string) ([]Pending, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

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

// tick reads the inboxes and steps once.
func (f *fixture) tick() {
	f.r.Poll()
	f.r.Step()
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

func TestRoundAndNews(t *testing.T) {
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
	f.inbox.set("/repo/1")
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()

	run = f.expect(t, 1, Idle)
	if run.Detail != detailWatching || run.Summary != "turn 1" {
		t.Fatalf("run %+v", run)
	}

	// News: /bot wakes for it by itself.
	f.inbox.set("/repo/1", task("t2", 1))
	f.tick()
	f.a.set("a1", agent.Working, 1)
	f.tick()
	f.expect(t, 1, Running)

	if f.a.count("send") != 0 || f.a.count("spawn") != 1 {
		t.Fatalf("calls %v", f.a.calls)
	}
}

func TestNoProgressStaysIdle(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()

	if run := f.expect(t, 1, Idle); run.Detail != detailNoProgress {
		t.Fatalf("detail = %q", run.Detail)
	}
}

func TestEndedRoundsWait(t *testing.T) {
	t.Parallel()

	for _, ended := range []string{agent.EndInterrupt, agent.EndDeclined} {
		t.Run(ended, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil, on(1))
			f.inbox.set("/repo/1", task("t1", 1))
			f.tick()
			f.a.endTurn("a1", 1, ended)
			f.tick()
			f.expect(t, 1, Waiting)

			// A person typed to it.
			f.a.set("a1", agent.Working, 1)
			f.tick()

			if run := f.expect(t, 1, Running); run.TurnBase != 1 {
				t.Fatalf("turn base = %d", run.TurnBase)
			}
		})
	}
}

func TestIdleYieldsItsPlace(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1), on(2))
	if err := f.r.SetMaxParallel(1); err != nil {
		t.Fatal(err)
	}

	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.a.endTurn("a1", 1, agent.EndStop)
	f.inbox.set("/repo/1")
	f.tick()
	f.expect(t, 1, Idle)

	f.inbox.set("/repo/2", task("t2", 1))
	f.tick()
	f.a.waitKilled(t, "a1")

	f.expect(t, 1, Resting)
	f.expect(t, 2, Running)

	// Back with news, it waits for a place; the busy one keeps it.
	f.inbox.set("/repo/1", task("t3", 1))
	f.tick()
	f.expect(t, 1, Queued)

	f.a.endTurn("a2", 1, agent.EndStop)
	f.tick()
	f.a.waitKilled(t, "a2")
	f.tick()
	f.expect(t, 1, Running)

	if f.a.count("resume a1: /bot") != 1 {
		t.Fatalf("calls %v", f.a.calls)
	}
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

	if f.a.count("resume a1") != 1 {
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

func TestNudgeAnIdleAgent(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()
	f.inbox.set("/repo/1")
	f.a.endTurn("a1", 1, agent.EndStop)
	f.tick()

	f.inbox.set("/repo/1", task("t2", 1))
	f.tick()
	f.expect(t, 1, Idle)

	f.r.mu.Lock()
	f.r.runs[1].newsAt = time.Now().Add(-nudgeAfter)
	f.r.mu.Unlock()

	f.tick()
	f.expect(t, 1, Running)

	if f.a.count("send a1: /bot") != 1 {
		t.Fatalf("calls %v", f.a.calls)
	}
}

func TestLostAgentResumes(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil, on(1))
	f.inbox.set("/repo/1", task("t1", 1))
	f.tick()

	for i := range maxRecoveries {
		f.a.set("a1", agent.Lost, 0)
		f.tick()

		if run := f.expect(t, 1, Running); run.Recoveries != i+1 {
			t.Fatalf("recoveries = %d", run.Recoveries)
		}
	}

	f.a.set("a1", agent.Lost, 0)
	f.tick()
	f.expect(t, 1, Failed)

	if f.a.count("resume a1") != maxRecoveries {
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
	f.a.waitKilled(t, "a1")

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
		{Project: 2, Dir: "/repo/2", State: Idle, AgentID: "gone"},
	}}

	r := New(Options{Agents: a, Pending: inbox.pending}, saved)
	r.SetProjects([]Project{on(1), on(2)})
	r.Step()

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
