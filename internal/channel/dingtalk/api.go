package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultAPI  = "https://api.dingtalk.com"
	maxResponse = 16 << 20                      // bound on an API answer
	tokenHeader = "x-acs-dingtalk-access-token" //nolint:gosec // the header name, not a token
	tokenMargin = 5 * time.Minute               // renew the access token this long before it expires
)

// OpenAPI paths.
const (
	pathAccessToken = "/v1.0/oauth2/accessToken" //nolint:gosec // the path, not a token
	pathOpenStream  = "/v1.0/gateway/connections/open"
	pathGroupSend   = "/v1.0/robot/groupMessages/send"
	pathDirectSend  = "/v1.0/robot/oToMessages/batchSend"
	pathDownload    = "/v1.0/robot/messageFiles/download"
)

// APIError is an error answer of the OpenAPI.
type APIError struct {
	Path    string
	Status  int
	Code    string // e.g. "InvalidAuthentication"
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("dingtalk %s: %d %s: %s", e.Path, e.Status, e.Code, e.Message)
}

// fatal reports errors that retrying cannot fix: the credentials or the
// request are refused. Rate limits and server errors are retried.
func fatal(err error) bool {
	ae, ok := errors.AsType[*APIError](err)

	return ok && ae.Status >= http.StatusBadRequest && ae.Status < http.StatusInternalServerError &&
		ae.Status != http.StatusTooManyRequests
}

// call POSTs body as JSON to an OpenAPI path and decodes the answer into out
// (if not nil). Every path but the token's own is authenticated with the
// app's access token.
func (c *Channel) call(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}

	req.Header.Set("Content-Type", "application/json")

	if path != pathAccessToken {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}

		req.Header.Set(tokenHeader, token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}

		_ = json.Unmarshal(answer, &e)

		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}

		return &APIError{Path: path, Status: resp.StatusCode, Code: e.Code, Message: e.Message}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("dingtalk %s: decode answer: %w", path, err)
	}

	return nil
}

// accessToken returns the app's access token, fetching a new one when the
// cached one is about to expire. It doubles as the credentials check.
func (c *Channel) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	var r struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"` // seconds
	}

	body := map[string]string{"appKey": c.cfg.ClientID, "appSecret": c.cfg.ClientSecret}
	if err := c.call(ctx, pathAccessToken, body, &r); err != nil {
		return "", err
	}

	if r.AccessToken == "" {
		return "", fmt.Errorf("dingtalk %s: no access token in the answer", pathAccessToken)
	}

	c.token = r.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(r.ExpireIn)*time.Second - tokenMargin)

	return c.token, nil
}
