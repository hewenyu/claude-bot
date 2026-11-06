//go:build unix

package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/agent"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/runner"
	"github.com/hewenyu/claude-bot/internal/store"
)

// echoAgent stands for Claude Code: it says hello and echoes what it is
// typed.
const echoAgent = "#!/bin/sh\nprintf 'hello\\r\\n'\nexec cat\n"

// runnerPage is a gateway page served on a real port, with a runner whose
// agents run program.
type runnerPage struct {
	*page

	srv  *httptest.Server
	bots *bots
}

func newRunnerPage(t *testing.T, program string) *runnerPage {
	t.Helper()

	f := newGatewayFixture(t)
	srv := httptest.NewUnstartedServer(nil)
	host := srv.Listener.Addr().String()

	b, err := startBots(t.Context(), t.TempDir(), "http://"+host+"/", slog.New(slog.DiscardHandler), program, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(b.agents.KillAll)

	f.g.bots = b
	p := f.page(host, b)
	srv.Config.Handler = p.h
	srv.Start()
	t.Cleanup(srv.Close)

	return &runnerPage{page: p, srv: srv, bots: b}
}

// agent is how the agent id reaches the page's gateway with token.
func (p *runnerPage) agent(id, token string) gatewayClient {
	return gatewayClient{base: p.srv.URL, token: token, id: id}
}

func writeScript(t *testing.T, script string) string {
	t.Helper()

	prog := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(prog, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return prog
}

func TestRunner_turnOnAndSettings(t *testing.T) {
	t.Parallel()

	p := newRunnerPage(t, "true")
	proj := p.f.register("alpha", "cli_a")
	path := "/api/projects/" + strconv.FormatInt(proj.ID, 10) + "/runner"

	var run runView

	code, raw := p.call(http.MethodGet, path, nil)
	if p.decode(raw, &run); code != http.StatusOK || run.State != runner.Off {
		t.Fatalf("before: %d %s", code, raw)
	}

	code, raw = p.call(http.MethodPut, path, map[string]bool{"on": true})
	if p.decode(raw, &run); code != http.StatusOK || run.State != runner.Resting {
		t.Fatalf("on: %d %s", code, raw)
	}

	if on, _ := p.f.store.RunnersOn(t.Context()); !on[proj.ID] {
		t.Fatalf("runners on = %v", on)
	}

	code, raw = p.call(http.MethodPut, path, map[string]bool{"on": false})
	if p.decode(raw, &run); code != http.StatusOK || run.State != runner.Off {
		t.Fatalf("off: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPost, path+"/resume", nil); code != http.StatusConflict {
		t.Fatalf("resume off: %d %s", code, raw)
	}
}

func TestRunner_maxParallel(t *testing.T) {
	t.Parallel()

	p := newRunnerPage(t, "true")
	proj := p.f.register("alpha", "cli_a")

	if code, raw := p.call(http.MethodPut, "/api/projects/"+strconv.FormatInt(proj.ID, 10)+"/runner", map[string]bool{"on": true}); code != http.StatusOK {
		t.Fatalf("on: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPut, "/api/runner", map[string]int{"max_parallel": 0}); code != http.StatusBadRequest {
		t.Fatalf("max 0: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPut, "/api/runner", map[string]int{"max_parallel": 5}); code != http.StatusOK {
		t.Fatalf("max 5: %d %s", code, raw)
	}

	var ov overviewResponse

	code, raw := p.call(http.MethodGet, "/api/overview", nil)
	if p.decode(raw, &ov); code != http.StatusOK || ov.Runner == nil || ov.Runner.MaxParallel != 5 || len(ov.Runner.Runs) != 1 {
		t.Fatalf("overview: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPost, "/api/projects/999/runner/resume", nil); code != http.StatusNotFound {
		t.Fatalf("resume unknown: %d %s", code, raw)
	}
}

func TestRunner_withoutRunner(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	proj := p.f.register("alpha", "cli_a")

	if code, raw := p.call(http.MethodPut, "/api/projects/"+strconv.FormatInt(proj.ID, 10)+"/runner", map[string]bool{"on": true}); code != http.StatusServiceUnavailable {
		t.Fatalf("on: %d %s", code, raw)
	}
}

func TestRunner_hooksAndAgents(t *testing.T) {
	t.Parallel()

	p := newRunnerPage(t, writeScript(t, echoAgent))

	a, err := p.bots.agents.Spawn(t.Context(), agent.SpawnOptions{Name: "alpha", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	event := `{"hook_event_name":"UserPromptSubmit"}`

	// Wrong tokens and other agents are turned away; Claude goes on.
	p.agent(a.ID, "nope").forwardHook(t.Context(), strings.NewReader(event), io.Discard)
	p.agent("a-other", p.bots.token).forwardHook(t.Context(), strings.NewReader(event), io.Discard)

	if info, _ := p.bots.agents.AgentInfo(a.ID); info.Status == agent.Working {
		t.Fatal("a hook with a wrong token or agent was taken")
	}

	p.agent(a.ID, p.bots.token).forwardHook(t.Context(), strings.NewReader(event), io.Discard)

	if info, _ := p.bots.agents.AgentInfo(a.ID); info.Status != agent.Working {
		t.Fatalf("status after the hook = %s", info.Status)
	}

	if code, raw := p.call(http.MethodPost, "/api/agents/nope/interrupt", nil); code != http.StatusNotFound {
		t.Fatalf("unknown agent: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPost, "/api/agents/"+a.ID+"/explode", nil); code != http.StatusNotFound {
		t.Fatalf("unknown action: %d %s", code, raw)
	}

	if code, raw := p.call(http.MethodPost, "/api/agents/"+a.ID+"/kill", nil); code != http.StatusOK {
		t.Fatalf("kill: %d %s", code, raw)
	}
}

// TestRunner_aRound follows one round through the gateway: the agent may
// not stop before it finished, may not finish before it answered its
// batch, and ends once it did both.
func TestRunner_aRound(t *testing.T) {
	t.Parallel()

	p, box, task := newRound(t)
	run := waitRun(t, p.bots, func(r runner.Run) bool { return r.State == runner.Running && r.AgentID != "" && len(r.Round) == 1 })
	id := run.AgentID
	stop := func() string { return stopHook(t, p, id) }

	if out := stop(); !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "claude-bot finish") {
		t.Fatalf("stop before finishing: %q", out)
	}

	if err := p.agent(id, p.bots.token).post(t.Context(), finishPath, nil, nil); err == nil || !strings.Contains(err.Error(), "t1") {
		t.Fatalf("finish before answering: %v", err)
	}

	if err := p.agent(id, "nope").post(t.Context(), finishPath, nil, nil); err == nil {
		t.Fatal("finish with a wrong token")
	}

	task.Entries = append(task.Entries, inbox.Entry{Kind: inbox.EntryBot, At: time.Now(), Text: "done"})

	if err := box.Locked(func(tx *inbox.Tx) error { return tx.SaveTask(task, false) }); err != nil {
		t.Fatal(err)
	}

	if err := p.agent(id, p.bots.token).post(t.Context(), finishPath, nil, nil); err != nil {
		t.Fatal(err)
	}

	if out := stop(); out != "" {
		t.Fatalf("stop after finishing: %q", out)
	}

	waitRun(t, p.bots, func(r runner.Run) bool { return r.State == runner.Resting })
	waitEnded(t, p.bots, id)
}

// waitEnded waits for an agent's program to end.
func waitEnded(t *testing.T, b *bots, id string) {
	t.Helper()

	a, err := b.agents.Get(id)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-a.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the agent of a round that ended still runs")
	}
}

// stopHook reports a Stop of the agent, and returns what the hook printed.
func stopHook(t *testing.T, p *runnerPage, id string) string {
	t.Helper()

	var out bytes.Buffer

	p.agent(id, p.bots.token).forwardHook(t.Context(), strings.NewReader(`{"hook_event_name":"Stop"}`), &out)

	return out.String()
}

// newRound is a runner page with a project that has /bot and a task, and
// its runner on.
func newRound(t *testing.T) (*runnerPage, *inbox.Inbox, inbox.Task) {
	t.Helper()

	p := newRunnerPage(t, writeScript(t, echoAgent))
	proj := p.f.register("alpha", "cli_a")

	commands := filepath.Join(proj.Dir, ".claude", "commands")
	if err := os.MkdirAll(commands, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(commands, "bot.md"), []byte("bot"), 0o600); err != nil {
		t.Fatal(err)
	}

	box := projectInbox(proj)
	task := inbox.Task{ID: "t1", Entries: []inbox.Entry{{Kind: inbox.EntryUser, At: time.Now(), Text: "hi"}}}

	if err := box.Locked(func(tx *inbox.Tx) error { return tx.SaveTask(task, true) }); err != nil {
		t.Fatal(err)
	}

	if code, raw := p.call(http.MethodPut, "/api/projects/"+strconv.FormatInt(proj.ID, 10)+"/runner", map[string]bool{"on": true}); code != http.StatusOK {
		t.Fatalf("on: %d %s", code, raw)
	}

	return p, box, task
}

// waitRun waits for the only run to be as ok says.
func waitRun(t *testing.T, b *bots, ok func(runner.Run) bool) runner.Run {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		b.runner.Kick()

		runs := b.runner.Saved().Runs
		if len(runs) == 1 && ok(runs[0]) {
			return runs[0]
		}

		if time.Now().After(deadline) {
			t.Fatalf("runs %+v", runs)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestFinishOutsideTheGateway(t *testing.T) {
	t.Setenv(envAgentURL, "")

	if err := cmdFinish(t.Context(), "", nil); err != nil {
		t.Fatal(err)
	}

	if err := cmdFinish(t.Context(), "", []string{"x"}); !errors.Is(err, errUsage) {
		t.Fatalf("finish x = %v", err)
	}
}

func TestRunner_terminal(t *testing.T) {
	t.Parallel()

	p := newRunnerPage(t, writeScript(t, echoAgent))

	a, err := p.bots.agents.Spawn(t.Context(), agent.SpawnOptions{Name: "alpha", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	url := "ws" + strings.TrimPrefix(p.srv.URL, "http") + "/api/agents/" + a.ID + "/term"
	header := http.Header{"Origin": {p.srv.URL}}

	// The page token goes as a subprotocol; without it, no terminal.
	_, resp, err := (&websocket.Dialer{Subprotocols: []string{wsProtocol}}).DialContext(t.Context(), url, header)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: %v", err)
	}

	_ = resp.Body.Close()

	d := &websocket.Dialer{Subprotocols: []string{wsProtocol, wsTokenPrefix + testPageToken}}

	c, resp, err := d.DialContext(t.Context(), url, header)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close() //nolint:errcheck // test cleanup

	_ = resp.Body.Close()

	if resp.Header.Get("Sec-WebSocket-Protocol") != wsProtocol {
		t.Fatalf("protocol = %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}

	expectTerm(t, c, "hello")

	if err := c.WriteMessage(websocket.BinaryMessage, []byte("ping\r")); err != nil {
		t.Fatal(err)
	}

	expectTerm(t, c, "ping")
}

// expectTerm reads the terminal until it printed want.
func expectTerm(t *testing.T, c *websocket.Conn, want string) {
	t.Helper()

	var seen []byte

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))

	for !bytes.Contains(seen, []byte(want)) {
		_, p, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("terminal printed %q, not %q: %v", seen, want, err)
		}

		seen = append(seen, p...)
	}
}

func TestPendingCountsPeople(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := projectInbox(store.Project{Dir: dir})

	replied := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	task := inbox.Task{ID: "t1", Entries: []inbox.Entry{{Kind: inbox.EntryUser, Text: "hi"}, {Kind: inbox.EntryBot, At: replied, Text: "on it"}}}

	if err := b.Locked(func(tx *inbox.Tx) error { return tx.SaveTask(task, true) }); err != nil {
		t.Fatal(err)
	}

	got, err := pendingIn(dir)
	if err != nil || len(got) != 1 || got[0] != (runner.Pending{ID: "t1", Entries: 1, Replied: replied}) {
		t.Fatalf("pending = %v, %v", got, err)
	}
}

func TestCheckBotCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "bot.md")); err != nil {
		if err := checkBotCommand(dir); err == nil {
			t.Fatal("a project without /bot passed")
		}
	}

	cmd := filepath.Join(dir, ".claude", "commands")
	if err := os.MkdirAll(cmd, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(cmd, "bot.md"), []byte("bot"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := checkBotCommand(dir); err != nil {
		t.Fatal(err)
	}
}

func TestLatestAgents(t *testing.T) {
	t.Parallel()

	saved := runnerState{
		Runs:   []runner.Run{{Project: 1, AgentID: "a2"}, {Project: 2}},
		Agents: []agent.Info{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}},
	}

	if got := latestAgents(saved); len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("latest = %+v", got)
	}
}
