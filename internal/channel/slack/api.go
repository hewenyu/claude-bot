package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

const (
	defaultAPI = "https://slack.com/api"
	formType   = "application/x-www-form-urlencoded"
)

// APIError is an error answer of the Web API.
type APIError struct {
	Method     string
	Code       string // e.g. "invalid_auth", "ratelimited"
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
}

// Error codes that retrying cannot fix.
var fatalCodes = map[string]bool{
	"invalid_auth":           true,
	"not_authed":             true,
	"account_inactive":       true,
	"token_revoked":          true,
	"token_expired":          true,
	"not_allowed_token_type": true,
}

const (
	codeRateLimited    = "ratelimited"
	codeAlreadyReacted = "already_reacted"
)

// errNotBot is a bot token that belongs to a user.
var errNotBot = errors.New("not a bot")

// fatal reports errors that retrying cannot fix: a bad or revoked token, a
// user's token in place of the bot's, or Socket Mode turned off.
func fatal(err error) bool {
	ae, ok := errors.AsType[*APIError](err)

	return ok && fatalCodes[ae.Code] || errors.Is(err, errNotBot) || errors.Is(err, errLinkDisabled)
}

// call invokes a Web API method with form params, authenticated by token,
// and decodes the answer into out (if not nil). A rate limit is honoured once.
func (c *Channel) call(ctx context.Context, token, method string, params url.Values, out any) error {
	return transport.RetryOnce(ctx, rateLimit, func() error { return c.callOnce(ctx, token, method, params, out) })
}

// rateLimit is how long a rate-limited answer asks to wait.
func rateLimit(err error) (time.Duration, bool) {
	if ae, ok := errors.AsType[*APIError](err); ok && ae.Code == codeRateLimited {
		return ae.RetryAfter, true
	}

	return 0, false
}

func (c *Channel) callOnce(ctx context.Context, token, method string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}

	req.Header.Set("Content-Type", formType)
	req.Header.Set("Authorization", transport.Bearer+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	if resp.StatusCode == http.StatusTooManyRequests {
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))

		return &APIError{Method: method, Code: codeRateLimited, RetryAfter: time.Duration(secs) * time.Second}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, transport.MaxResponse))
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}

	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("slack %s: %s: %w", method, resp.Status, err)
	}

	if !r.OK {
		return &APIError{Method: method, Code: r.Error}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("slack %s: decode answer: %w", method, err)
	}

	return nil
}
