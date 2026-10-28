package github

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Issue and PR states, and why an issue was closed.
const (
	stateOpen        = "open"
	stateClosed      = "closed"
	reasonCompleted  = "completed"
	reasonNotPlanned = "not_planned"
)

// Accepted values of Merge's method and Review's event.
var (
	mergeMethods = []string{MergeSquash, MergeCommit, MergeRebase}
	reviewEvents = []string{ReviewApprove, ReviewRequestChanges, ReviewComment}
)

// Close closes the issue or PR that id (a protocol or platform id) belongs
// to. notPlanned marks an issue as not planned rather than completed.
func (c *Channel) Close(ctx context.Context, id string, notPlanned bool) error {
	r, err := c.refOf(id)
	if err != nil {
		return err
	}

	var i issue
	if err := c.get(ctx, c.repoPath("/issues/%d", r.number), nil, &i); err != nil {
		return err
	}

	if i.isPR() {
		return c.send(ctx, http.MethodPatch, c.repoPath("/pulls/%d", r.number), map[string]string{"state": stateClosed}, nil)
	}

	reason := reasonCompleted
	if notPlanned {
		reason = reasonNotPlanned
	}

	body := map[string]string{"state": stateClosed, "state_reason": reason}

	return c.send(ctx, http.MethodPatch, c.repoPath("/issues/%d", r.number), body, nil)
}

// Merge merges the PR that id belongs to with method (MergeSquash,
// MergeCommit or MergeRebase) and returns the resulting commit.
func (c *Channel) Merge(ctx context.Context, id, method string) (string, error) {
	if !slices.Contains(mergeMethods, method) {
		return "", fmt.Errorf("github merge: method %q: want one of %s", method, strings.Join(mergeMethods, ", "))
	}

	r, err := c.refOf(id)
	if err != nil {
		return "", err
	}

	var res struct {
		SHA string `json:"sha"`
	}

	path := c.repoPath("/pulls/%d/merge", r.number)
	if err := c.send(ctx, http.MethodPut, path, map[string]string{"merge_method": method}, &res); err != nil {
		return "", err
	}

	return res.SHA, nil
}

// Review submits a review of the PR that id belongs to. event is
// ReviewApprove, ReviewRequestChanges or ReviewComment; the last two need a
// body.
func (c *Channel) Review(ctx context.Context, id, event, body string) error {
	if !slices.Contains(reviewEvents, event) {
		return fmt.Errorf("github review: event %q: want one of %s", event, strings.Join(reviewEvents, ", "))
	}

	if body == "" && event != ReviewApprove {
		return fmt.Errorf("github review: %s needs a body", event)
	}

	r, err := c.refOf(id)
	if err != nil {
		return err
	}

	req := map[string]string{"event": event}
	if body != "" {
		req["body"] = withMarker(body)
	}

	return c.send(ctx, http.MethodPost, c.repoPath("/pulls/%d/reviews", r.number), req, nil)
}
