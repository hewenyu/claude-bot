package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

const defaultAPI = "https://slack.com/api"

// apiError is an error answer of the Web API.
type apiError struct {
	Method     string
	Code       string // e.g. "invalid_auth", "ratelimited"
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
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
	ae, ok := errors.AsType[*apiError](err)

	return ok && fatalCodes[ae.Code] || errors.Is(err, errNotBot) || errors.Is(err, errLinkDisabled)
}

// call invokes a Web API method with form params, authenticated by token,
// and decodes the answer into out (if not nil). A rate limit is honoured once.
func (c *Channel) call(ctx context.Context, token, method string, params url.Values, out any) error {
	return transport.RetryOnce(ctx, rateLimit, func() error { return c.callOnce(ctx, token, method, params, out) })
}

// rateLimit is how long a rate-limited answer asks to wait.
func rateLimit(err error) (time.Duration, bool) {
	if ae, ok := errors.AsType[*apiError](err); ok && ae.Code == codeRateLimited {
		return ae.RetryAfter, true
	}

	return 0, false
}

func (c *Channel) callOnce(ctx context.Context, token, method string, params url.Values, out any) error {
	header := http.Header{"Content-Type": {transport.Form}, "Authorization": {transport.Bearer + token}}

	resp, err := transport.Do(ctx, c.http, http.MethodPost, c.api+"/"+method, header, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}

	if resp.Status == http.StatusTooManyRequests {
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))

		return &apiError{Method: method, Code: codeRateLimited, RetryAfter: time.Duration(secs) * time.Second}
	}

	raw := resp.Body

	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("slack %s: HTTP %d: %w", method, resp.Status, err)
	}

	if !r.OK {
		return &apiError{Method: method, Code: r.Error}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("slack %s: decode answer: %w", method, err)
	}

	return nil
}
