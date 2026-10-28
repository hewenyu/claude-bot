package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPI    = "https://api.telegram.org"
	maxRetryAfter = 60 * time.Second // longest flood wait honoured before giving up
)

// APIError is an error answer of the Bot API.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration // set on 429 Too Many Requests
}

func (e *APIError) Error() string {
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
	var ae *APIError
	if !errors.As(err, &ae) {
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
	err := c.callOnce(ctx, method, params, out)

	var ae *APIError
	if errors.As(err, &ae) && ae.Code == codeTooMany && ae.RetryAfter <= maxRetryAfter {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ae.RetryAfter):
		}

		err = c.callOnce(ctx, method, params, out)
	}

	return err
}

func (c *Channel) callOnce(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, redact(err))
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, redact(err))
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: %s: %w", method, resp.Status, err)
	}

	if !r.OK {
		ae := &APIError{Method: method, Code: r.ErrorCode, Description: r.Description}
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
