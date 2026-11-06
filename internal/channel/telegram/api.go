package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

const defaultAPI = "https://api.telegram.org"

// apiError is an error answer of the Bot API.
type apiError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration // set on 429 Too Many Requests
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Status codes with a meaning of their own.
const (
	codeBadRequest   = http.StatusBadRequest
	codeUnauthorized = http.StatusUnauthorized
	codeNotFound     = http.StatusNotFound // what a malformed token gets
	codeConflict     = http.StatusConflict
	codeTooMany      = http.StatusTooManyRequests
)

// fatal reports errors that retrying cannot fix: a bad token, or a webhook
// that keeps getUpdates from working.
func fatal(err error) bool {
	ae, ok := errors.AsType[*apiError](err)
	if !ok {
		return false
	}

	switch ae.Code {
	case codeUnauthorized, codeNotFound:
		return true
	case codeConflict:
		return strings.Contains(ae.Description, "webhook")
	}

	return false
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call invokes a Bot API method with params as JSON and decodes the result
// into out (if not nil). A flood wait is honoured once.
func (c *Channel) call(ctx context.Context, method string, params, out any) error {
	return transport.RetryOnce(ctx, floodWait, func() error { return c.callOnce(ctx, method, params, out) })
}

// floodWait is how long a 429 answer asks to wait.
func floodWait(err error) (time.Duration, bool) {
	if ae, ok := errors.AsType[*apiError](err); ok && ae.Code == codeTooMany {
		return ae.RetryAfter, true
	}

	return 0, false
}

func (c *Channel) callOnce(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}

	resp, err := transport.Do(ctx, c.http, http.MethodPost, c.api+"/bot"+c.token+"/"+method,
		http.Header{"Content-Type": {transport.JSON}}, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, redact(err))
	}

	var r response
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d: %w", method, resp.Status, err)
	}

	if !r.OK {
		ae := &apiError{Method: method, Code: r.ErrorCode, Description: r.Description}
		if r.Parameters != nil {
			ae.RetryAfter = time.Duration(r.Parameters.RetryAfter) * time.Second
		}

		return ae
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(r.Result, out); err != nil {
		return fmt.Errorf("telegram %s: decode result: %w", method, err)
	}

	return nil
}

// redact drops the request URL from a transport error: it carries the token.
func redact(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}

	return err
}
