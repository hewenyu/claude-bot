package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hewenyu/claude-bot/internal/transport"
)

const (
	defaultAPI = "https://api.github.com"
	apiVersion = "2022-11-28"
	mediaType  = "application/vnd.github+json"
	userAgent  = "claude-bot"
	perPage    = 100 // the most GitHub returns per page
	maxPages   = 10  // pages read per listing, so a huge backlog cannot stall the listener
)

// APIError is an error answer of the REST API.
type APIError struct {
	Method  string
	Path    string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github %s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// fatal reports errors that retrying cannot fix: a rejected token.
func fatal(err error) bool {
	ae, ok := errors.AsType[*APIError](err)

	return ok && ae.Status == http.StatusUnauthorized
}

// request is one API call. etag, if set, makes it conditional.
type request struct {
	method string
	path   string // below the API root, e.g. /repos/o/r/issues
	query  url.Values
	body   any
	etag   string
}

// response is what a call yields besides the decoded body.
type response struct {
	etag        string
	notModified bool
}

// call performs r and decodes the answer into out (if not nil).
func (c *Channel) call(ctx context.Context, r request, out any) (response, error) {
	var body io.Reader

	if r.body != nil {
		raw, err := json.Marshal(r.body)
		if err != nil {
			return response{}, fmt.Errorf("github %s: %w", r.path, err)
		}

		body = bytes.NewReader(raw)
	}

	u := c.api + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return response{}, fmt.Errorf("github %s: %w", r.path, err)
	}

	req.Header.Set("Accept", mediaType)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if r.etag != "" {
		req.Header.Set("If-None-Match", r.etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("github %s %s: %w", r.method, r.path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	res := response{etag: resp.Header.Get("ETag")}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		res.notModified = true

		return res, nil
	case resp.StatusCode >= http.StatusBadRequest:
		return res, apiError(r, resp)
	case out == nil || resp.StatusCode == http.StatusNoContent:
		return res, nil
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, transport.MaxResponse)).Decode(out); err != nil {
		return res, fmt.Errorf("github %s %s: decode: %w", r.method, r.path, err)
	}

	return res, nil
}

func apiError(r request, resp *http.Response) error {
	var e struct {
		Message string `json:"message"`
	}

	_ = json.NewDecoder(io.LimitReader(resp.Body, transport.MaxResponse)).Decode(&e)

	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}

	return &APIError{Method: r.method, Path: r.path, Status: resp.StatusCode, Message: e.Message}
}

// repoPath is an API path below the repository, e.g. repoPath("/issues/%d", 42)
// is /repos/<owner>/<repo>/issues/42.
func (c *Channel) repoPath(format string, args ...any) string {
	return "/repos/" + c.repo + fmt.Sprintf(format, args...)
}

// get is a plain GET.
func (c *Channel) get(ctx context.Context, path string, query url.Values, out any) error {
	_, err := c.call(ctx, request{method: http.MethodGet, path: path, query: query}, out)

	return err
}

// send is a write (POST, PATCH, PUT) with a JSON body.
func (c *Channel) send(ctx context.Context, method, path string, body, out any) error {
	_, err := c.call(ctx, request{method: method, path: path, body: body}, out)

	return err
}

// listAll reads up to maxPages pages of a listing, calling each with every
// item. The first page is conditional on etag; notModified reports that it
// was unchanged (and nothing was read).
func listAll[T any](ctx context.Context, c *Channel, path string, query url.Values, etag string, each func(T)) (response, error) {
	q := url.Values{}
	maps.Copy(q, query)

	q.Set("per_page", strconv.Itoa(perPage))

	var first response

	for page := 1; page <= maxPages; page++ {
		q.Set("page", strconv.Itoa(page))

		r := request{method: http.MethodGet, path: path, query: q}
		if page == 1 {
			r.etag = etag
		}

		var items []T

		res, err := c.call(ctx, r, &items)
		if err != nil {
			return first, err
		}

		if page == 1 {
			first = res
		}

		if res.notModified {
			return first, nil
		}

		for _, it := range items {
			each(it)
		}

		if len(items) < perPage {
			break
		}
	}

	return first, nil
}
