package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/agent"
)

// The agents the gateway runs report to it over HTTP, on the page's server:
// their Claude Code hooks with `claude-bot hook`, the end of their round
// with `claude-bot finish`. The gateway gives each agent, besides its id
// (agent.EnvAgentID), the base URL to report to and a token.
const (
	envAgentURL   = agent.EnvPrefix + "URL" // the gateway's base URL, e.g. http://127.0.0.1:7600
	envAgentToken = agent.EnvPrefix + "TOKEN"
	// headerAgent names the agent a report comes from; the token comes as
	// a bearer token.
	headerAgent = "X-Claude-Bot-Agent"
	// hookPath is where agents report their hooks, finishPath the end of
	// their round.
	hookPath   = "/agent/hook"
	finishPath = "/agent/finish"
	// hookTimeout is how long a hook waits for the gateway: never long, so
	// a gateway that went away does not hold Claude up.
	hookTimeout = 2 * time.Second
	// maxHookInput bounds the hook event read from stdin, and the answer.
	maxHookInput = 1 << 20
)

// hookAnswer is what the gateway answers a report: for a Stop, whether the
// agent may end its turn, as Claude Code reads it from the hook's output.
type hookAnswer struct {
	OK       bool   `json:"ok"`
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// gatewayClient is how an agent reaches the gateway that runs it.
type gatewayClient struct {
	base, token, id string
}

// agentClient is the gateway of the agent this runs in; ok is false
// outside an agent the gateway runs.
func agentClient() (c gatewayClient, ok bool) {
	c = gatewayClient{base: os.Getenv(envAgentURL), token: os.Getenv(envAgentToken), id: os.Getenv(agent.EnvAgentID)}

	return c, c.base != "" && c.token != "" && c.id != ""
}

// cmdHook forwards a Claude Code hook event, read from stdin, to the
// gateway that runs the agent, and prints what the gateway decided, e.g.
// that a Stop must wait. It never fails the hook: an agent the gateway does
// not run reports nothing, a gateway that does not answer decides nothing,
// and Claude goes on.
func cmdHook(ctx context.Context, _ string, _ []string) error {
	if c, ok := agentClient(); ok {
		c.forwardHook(ctx, os.Stdin, os.Stdout)
	}

	return nil
}

// forwardHook posts the hook event read from in to the gateway, and writes
// a decision the gateway answered to out.
func (c gatewayClient) forwardHook(ctx context.Context, in io.Reader, out io.Writer) {
	body, err := io.ReadAll(io.LimitReader(in, maxHookInput))
	if err != nil {
		return
	}

	var answer hookAnswer
	if c.post(ctx, hookPath, body, &answer) != nil || answer.Decision == "" {
		return
	}

	b, err := json.Marshal(map[string]string{"decision": answer.Decision, "reason": answer.Reason})
	if err == nil {
		_, _ = out.Write(append(b, '\n'))
	}
}

// cmdFinish tells the gateway that runs the agent that its round is done:
// it ends the agent once its turn ends. Run elsewhere, it does nothing.
func cmdFinish(ctx context.Context, _ string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: finish takes no arguments", errUsage)
	}

	c, ok := agentClient()
	if !ok {
		fmt.Println("not an agent the gateway runs: nothing to finish")

		return nil
	}

	if err := c.post(ctx, finishPath, nil, nil); err != nil {
		return err
	}

	fmt.Println("finished: end your turn now; the gateway ends this session, and starts a new one for what comes next")

	return nil
}

// post posts body to path on the gateway and decodes its answer into out,
// if out is not nil. An answer that is not a 200 is an error, with what the
// gateway said.
func (c gatewayClient) post(ctx context.Context, path string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	url := strings.TrimSuffix(c.base, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set(headerAuthorization, bearerPrefix+c.token)
	req.Header.Set(headerAgent, c.id)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the gateway: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read to the end below

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHookInput))
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}

		_ = json.Unmarshal(raw, &e)

		return errors.New(cmp.Or(e.Error, resp.Status))
	}

	if out == nil {
		return nil
	}

	return json.Unmarshal(raw, out)
}
