// Package jev is a minimal client for TypeSafe's System One API
// (https://docs.typesafe.ai). Jev does not generate text: it answers typed
// questions about a state with calibrated probabilities. claude-bot only asks
// Noul (yes/no) questions.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

// Defaults of a Client.
const (
	DefaultURL   = "https://api.typesafe.ai"
	DefaultModel = "jev-latest"
)

const (
	// batchSize bounds the questions per request: each costs ~70 tokens and
	// a request (state plus questions) must fit in 64k.
	batchSize = 250
	attempts  = 4
	timeout   = 2 * time.Minute
	// maxResponse bounds a response body; answers are a few numbers each.
	maxResponse = 4 << 20
)

// Noul is a yes/no question. True and False, if set, say what counts as
// each answer.
type Noul struct {
	Instructions string
	True, False  string
}

// Config is the "typesafe" entry of config.json.
type Config struct {
	APIKey string `json:"api_key"`
}

// Client asks Jev questions.
type Client struct {
	URL   string
	Key   string
	Model string
	HTTP  *http.Client
}

// New returns a client using an API key from the TypeSafe console.
func New(key string) *Client {
	return &Client{URL: DefaultURL, Key: key, Model: DefaultModel, HTTP: &http.Client{Timeout: timeout}}
}

// Check confirms the key works by listing the models it can use.
func (c *Client) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/v1/models", nil)
	if err != nil {
		return fmt.Errorf("jev: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Key)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("jev: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jev: list models: %s", resp.Status)
	}

	return nil
}

// Nouls asks every question about state and returns, by question id, the
// probability that the answer is yes. Questions beyond one request's worth
// go in further requests with the same state.
func (c *Client) Nouls(ctx context.Context, state any, questions map[string]Noul) (map[string]float64, error) {
	ids := slices.Sorted(maps.Keys(questions))
	answers := make(map[string]float64, len(ids))

	for batch := range slices.Chunk(ids, batchSize) {
		got, err := c.ask(ctx, state, batch, questions)
		if err != nil {
			return nil, err
		}

		for _, id := range batch {
			p, ok := got[id]
			if !ok {
				return nil, fmt.Errorf("jev: no answer to %q", id)
			}

			answers[id] = p
		}
	}

	return answers, nil
}

type wireNoul struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]wireNoul `json:"questions"`
}

type response struct {
	Answers map[string]struct {
		Noul *float64 `json:"noul"`
	} `json:"answers"`
}

func (c *Client) ask(ctx context.Context, state any, ids []string, questions map[string]Noul) (map[string]float64, error) {
	req := request{Model: c.Model, State: state, Questions: make(map[string]wireNoul, len(ids))}

	for _, id := range ids {
		q := questions[id]

		w := wireNoul{Type: "noul", Instructions: q.Instructions}
		if q.True != "" || q.False != "" {
			w.Criteria = map[string]string{"true": q.True, "false": q.False}
		}

		req.Questions[id] = w
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("jev: encode request: %w", err)
	}

	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}

	answers := make(map[string]float64, len(resp.Answers))

	for id, a := range resp.Answers {
		if a.Noul != nil {
			answers[id] = *a.Noul
		}
	}

	return answers, nil
}

// post sends a request, retrying when TypeSafe is rate limited or
// overloaded. Errors carry the status only: error bodies can quote the
// request, which is user content.
func (c *Client) post(ctx context.Context, body []byte) (response, error) {
	backoff := transport.Backoff{Start: time.Second, Cap: 8 * time.Second}

	for attempt := 1; ; attempt++ {
		resp, retryAfter, err := c.postOnce(ctx, body)
		if err == nil || !errors.Is(err, errRetryable) || attempt == attempts {
			return resp, err
		}

		if !transport.Sleep(ctx, max(retryAfter, backoff.Next())) {
			return resp, fmt.Errorf("jev: %w", ctx.Err())
		}
	}
}

var errRetryable = errors.New("retryable")

// statusOverloaded is TypeSafe's "temporarily overloaded" status.
const statusOverloaded = 529

func (c *Client) postOnce(ctx context.Context, body []byte) (response, time.Duration, error) {
	var out response

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return out, 0, fmt.Errorf("jev: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return out, 0, fmt.Errorf("jev: %w: %w", errRetryable, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == statusOverloaded || resp.StatusCode >= http.StatusInternalServerError:
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))

		return out, time.Duration(secs) * time.Second, fmt.Errorf("jev: %w: %s", errRetryable, resp.Status)
	case resp.StatusCode == http.StatusUnauthorized:
		return out, 0, fmt.Errorf("jev: %s: check the TypeSafe API key", resp.Status)
	default:
		return out, 0, fmt.Errorf("jev: %s", resp.Status)
	}

	dec := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, maxResponse))
	if err := dec.Decode(&out); err != nil {
		return out, 0, fmt.Errorf("jev: decode response: %w", err)
	}

	return out, 0, nil
}
