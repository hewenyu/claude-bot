package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

const (
	defaultAPI  = "https://api.dingtalk.com"
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

// apiError is an error answer of the OpenAPI.
type apiError struct {
	Path    string
	Status  int
	Code    string // e.g. "InvalidAuthentication"
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("dingtalk %s: %d %s: %s", e.Path, e.Status, e.Code, e.Message)
}

// fatal reports errors that retrying cannot fix: the credentials or the
// request are refused. Rate limits and server errors are retried.
func fatal(err error) bool {
	ae, ok := errors.AsType[*apiError](err)

	return ok && ae.Status >= http.StatusBadRequest && ae.Status < http.StatusInternalServerError &&
		ae.Status != http.StatusTooManyRequests
}

// call POSTs body as JSON to an OpenAPI path, authenticated with the app's
// access token, and decodes the answer into out (if not nil).
func (c *Channel) call(ctx context.Context, path string, body, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	return c.post(ctx, path, http.Header{tokenHeader: {token}}, body, out)
}

// post POSTs body as JSON to an OpenAPI path with header, and decodes the
// answer into out (if not nil). On its own it is unauthenticated, for the
// access token's own path.
func (c *Channel) post(ctx context.Context, path string, header http.Header, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}

	h := http.Header{"Content-Type": {transport.JSON}}
	maps.Copy(h, header)

	resp, err := transport.Do(ctx, c.http, http.MethodPost, c.api+path, h, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("dingtalk %s: %w", path, err)
	}

	if resp.Status >= http.StatusBadRequest {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}

		_ = json.Unmarshal(resp.Body, &e)

		if e.Message == "" {
			e.Message = http.StatusText(resp.Status)
		}

		return &apiError{Path: path, Status: resp.Status, Code: e.Code, Message: e.Message}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(resp.Body, out); err != nil {
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
	if err := c.post(ctx, pathAccessToken, nil, body, &r); err != nil {
		return "", err
	}

	if r.AccessToken == "" {
		return "", fmt.Errorf("dingtalk %s: no access token in the answer", pathAccessToken)
	}

	c.token = r.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(r.ExpireIn)*time.Second - tokenMargin)

	return c.token, nil
}
