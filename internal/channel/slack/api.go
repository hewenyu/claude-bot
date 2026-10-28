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
	defaultAPI    = "https://slack.com/api"
	maxRetryAfter = 60 * time.Second // longest rate-limit wait honoured before giving up
	formType      = "application/x-www-form-urlencoded"
	bearer        = "Bearer " // Authorization scheme of every token
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

// fatal reports errors that retrying cannot fix: a bad or revoked token.
func fatal(err error) bool {
	var ae *APIError

	return errors.As(err, &ae) && fatalCodes[ae.Code]
}

// call invokes a Web API method with form params, authenticated by token,
// and decodes the answer into out (if not nil). A rate limit is honoured once.
func (c *Channel) call(ctx context.Context, token, method string, params url.Values, out any) error {
	err := c.callOnce(ctx, token, method, params, out)

	var ae *APIError
	if errors.As(err, &ae) && ae.Code == codeRateLimited && ae.RetryAfter <= maxRetryAfter {
		if !transport.Sleep(ctx, ae.RetryAfter) {
			return ctx.Err()
		}

		err = c.callOnce(ctx, token, method, params, out)
	}

	return err
}

func (c *Channel) callOnce(ctx context.Context, token, method string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}

	req.Header.Set("Content-Type", formType)
	req.Header.Set("Authorization", bearer+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	if resp.StatusCode == http.StatusTooManyRequests {
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))

		return &APIError{Method: method, Code: codeRateLimited, RetryAfter: time.Duration(secs) * time.Second}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
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
