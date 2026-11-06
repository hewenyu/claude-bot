package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/agent"
	"github.com/hewenyu/claude-bot/internal/runner"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The page shows each project's agent and its terminal. A browser cannot
// set headers on a WebSocket, so the terminal takes the page token as a
// subprotocol: "token.<token>", next to wsProtocol, which it answers with.
const (
	wsProtocol      = "claude-bot"
	wsTokenPrefix   = "token."
	maxTermMessage  = 64 << 10
	termWriteWait   = 10 * time.Second
	statusTermAgain = 4000 // the viewer fell behind: reconnect and replay
)

var termUpgrader = websocket.Upgrader{
	Subprotocols:    []string{wsProtocol},
	ReadBufferSize:  4096,
	WriteBufferSize: 32 << 10,
}

// errNoRunner reports a gateway that runs no agents: its page is not
// served, or the runner did not start.
var errNoRunner = errors.New("the gateway runs no agents; see its log")

// runnerOverview is the runner as the overview shows it.
type runnerOverview struct {
	MaxParallel int       `json:"max_parallel"`
	Runs        []runView `json:"runs"`
}

// runView is a project's run and its agent.
type runView struct {
	runner.Run

	Agent *agent.Info `json:"agent,omitempty"`
}

// runnerOverview reports the runner, without the runs' logs; nil if it
// does not run.
func (w *web) runnerOverview() *runnerOverview {
	b := w.g.bots
	if b == nil {
		return nil
	}

	runs := b.runner.Saved().Runs
	out := &runnerOverview{MaxParallel: b.runner.MaxParallel(), Runs: make([]runView, 0, len(runs))}

	for _, run := range runs {
		run.Log = nil
		out.Runs = append(out.Runs, w.viewRun(run))
	}

	return out
}

func (w *web) viewRun(run runner.Run) runView {
	v := runView{Run: run}

	if run.AgentID != "" {
		if info, err := w.g.bots.agents.AgentInfo(run.AgentID); err == nil {
			v.Agent = &info
		}
	}

	return v
}

// projectRun is a project's run with its log; a project whose runner was
// never on is off.
func (w *web) projectRun(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	if w.g.bots == nil {
		w.fail(rw, errNoRunner)

		return
	}

	for _, run := range w.g.bots.runner.Saved().Runs {
		if run.Project == p.ID {
			writeAPI(rw, http.StatusOK, w.viewRun(run))

			return
		}
	}

	writeAPI(rw, http.StatusOK, runView{Project: p.ID, Name: p.Name, Dir: p.Dir, State: runner.Off})
}

// putProjectRunner turns a project's runner on or off.
func (w *web) putProjectRunner(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}

	p, err := w.project(r)
	if err == nil {
		err = decodeBody(r, &in)
	}

	if err == nil {
		v := ""
		if in.On {
			v = runnerOn
		}

		err = w.g.store.SetSetting(r.Context(), runnerKey(p.ID), v)
	}

	if err == nil {
		err = w.syncBots(r.Context())
	}

	if err != nil {
		w.fail(rw, err)

		return
	}

	w.projectRun(rw, r)
}

// resumeProjectRunner gives a project's agent /bot again.
func (w *web) resumeProjectRunner(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err == nil && w.g.bots == nil {
		err = errNoRunner
	}

	if err == nil {
		if err = w.g.bots.runner.Resume(p.ID); err != nil {
			err = fmt.Errorf("%w: %w", errConflict, err)
		}
	}

	if err != nil {
		w.fail(rw, err)

		return
	}

	w.projectRun(rw, r)
}

// putRunner sets how many agents run at once.
func (w *web) putRunner(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		MaxParallel int `json:"max_parallel"`
	}

	err := decodeBody(r, &in)
	if err == nil && (in.MaxParallel < 1 || in.MaxParallel > runner.MaxParallelLimit) {
		err = fmt.Errorf("%w: 同时运行的 agent 数要在 1 到 %d 之间", errBadRequest, runner.MaxParallelLimit)
	}

	if err == nil {
		err = w.g.store.SetSetting(r.Context(), store.SettingRunnerMaxParallel, strconv.Itoa(in.MaxParallel))
	}

	if err == nil {
		err = w.syncBots(r.Context())
	}

	if err != nil {
		w.fail(rw, err)

		return
	}

	writeAPI(rw, http.StatusOK, w.runnerOverview())
}

func (w *web) syncBots(ctx context.Context) error {
	if w.g.bots == nil {
		return errNoRunner
	}

	return w.g.bots.sync(ctx, w.g.store)
}

// agentAction interrupts or ends an agent, or types to it.
func (w *web) agentAction(rw http.ResponseWriter, r *http.Request) {
	a, err := w.agent(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	var in struct {
		Text string `json:"text"`
	}

	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			w.fail(rw, err)

			return
		}
	}

	switch r.PathValue("action") {
	case "interrupt":
		err = a.Interrupt()
	case "kill":
		err = a.Kill()
	case "keys":
		err = a.Write([]byte(in.Text))
	case "send":
		err = a.Send(in.Text, false)
	default:
		apiError(rw, http.StatusNotFound, "no such action")

		return
	}

	if err != nil {
		w.fail(rw, fmt.Errorf("%w: %w", errConflict, err))

		return
	}

	w.g.bots.runner.Kick()
	writeAPI(rw, http.StatusOK, a.Info())
}

func (w *web) agent(r *http.Request) (*agent.Agent, error) {
	if w.g.bots == nil {
		return nil, errNoRunner
	}

	a, err := w.g.bots.agents.Get(r.PathValue("agent"))
	if errors.Is(err, agent.ErrNotFound) {
		return nil, fmt.Errorf("%w: %w", errNotFound, err)
	}

	return a, err
}

// term streams an agent's terminal: first what it printed so far, then its
// output as it comes. Binary messages from the viewer are keys; text
// messages are JSON, e.g. {"type":"resize","cols":120,"rows":40}.
func (w *web) term(rw http.ResponseWriter, r *http.Request) {
	a, err := w.agent(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	c, err := termUpgrader.Upgrade(rw, r, nil)
	if err != nil {
		return // the upgrader answered
	}
	defer c.Close() //nolint:errcheck // the connection is done either way

	c.SetReadLimit(maxTermMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	replay, out, detach := a.Attach()
	defer detach()

	go termInput(cancel, c, a)

	if termWrite(c, websocket.BinaryMessage, replay) != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-out:
			if !ok {
				code, why := websocket.CloseNormalClosure, "exited"
				if a.Info().Status.Live() {
					code, why = statusTermAgain, "resync"
				}

				_ = termWrite(c, websocket.CloseMessage, websocket.FormatCloseMessage(code, why))

				return
			}

			if termWrite(c, websocket.BinaryMessage, p) != nil {
				return
			}
		}
	}
}

func termWrite(c *websocket.Conn, typ int, p []byte) error {
	_ = c.SetWriteDeadline(time.Now().Add(termWriteWait))

	return c.WriteMessage(typ, p)
}

func termInput(cancel func(), c *websocket.Conn, a *agent.Agent) {
	defer cancel()

	for {
		typ, p, err := c.ReadMessage()
		if err != nil {
			return
		}

		if typ == websocket.BinaryMessage {
			_ = a.Write(p)

			continue
		}

		var m struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}

		if json.Unmarshal(p, &m) == nil && m.Type == "resize" {
			_ = a.Resize(m.Rows, m.Cols)
		}
	}
}

// agentBots is the runner and its agents, for a request of an agent with
// the agents' token, not the page's; nil after it answered.
func (w *web) agentBots(rw http.ResponseWriter, r *http.Request) *bots {
	b := w.g.bots
	if b == nil {
		apiError(rw, http.StatusNotFound, errNoRunner.Error())

		return nil
	}

	want := []byte("Bearer " + b.token)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
		apiError(rw, http.StatusUnauthorized, "wrong agent token")

		return nil
	}

	return b
}

// hookAnswer is what the gateway answers a hook: for a Stop, whether the
// agent may end its turn, as Claude Code reads it from the hook's output.
type hookAnswer struct {
	OK       bool   `json:"ok"`
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// agentHook takes a hook event from an agent's `claude-bot hook`. A Stop
// the runner does not allow yet does not end the agent's turn: Claude
// Code goes on with the reason.
func (w *web) agentHook(rw http.ResponseWriter, r *http.Request) {
	b := w.agentBots(rw, r)
	if b == nil {
		return
	}

	var h agent.Hook

	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, maxHookInput))
	if err == nil {
		err = json.Unmarshal(body, &h)
	}

	if err != nil {
		apiError(rw, http.StatusBadRequest, err.Error())

		return
	}

	id := r.Header.Get(headerAgent)

	if h.Event == agent.EventStop && h.SubagentID == "" {
		if info, err := b.agents.AgentInfo(id); err == nil {
			if block, reason := b.runner.StopHook(id, info.Subagents); block {
				writeAPI(rw, http.StatusOK, hookAnswer{OK: true, Decision: "block", Reason: reason})

				return
			}
		}
	}

	if err := b.agents.Hook(id, h); err != nil {
		apiError(rw, http.StatusNotFound, err.Error())

		return
	}

	writeAPI(rw, http.StatusOK, hookAnswer{OK: true})
}

// agentFinish takes an agent's `claude-bot finish`: its round is done.
func (w *web) agentFinish(rw http.ResponseWriter, r *http.Request) {
	b := w.agentBots(rw, r)
	if b == nil {
		return
	}

	if err := b.runner.Finish(r.Header.Get(headerAgent)); err != nil {
		apiError(rw, http.StatusConflict, err.Error())

		return
	}

	writeAPI(rw, http.StatusOK, hookAnswer{OK: true})
}

// wsToken is the page token a WebSocket offers as a subprotocol, as an
// Authorization header would carry it.
func wsToken(r *http.Request) string {
	if !websocket.IsWebSocketUpgrade(r) {
		return ""
	}

	for _, p := range websocket.Subprotocols(r) {
		if t, ok := strings.CutPrefix(p, wsTokenPrefix); ok {
			return "Bearer " + t
		}
	}

	return ""
}

func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: %w", errBadRequest, err)
	}

	return nil
}
