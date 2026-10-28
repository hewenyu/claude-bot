package github

import (
	"context"
	"fmt"
	"net/http"
	"slices"
)

// Close closes the issue or PR that id (a protocol or platform id) belongs
// to. notPlanned marks an issue as not planned rather than completed.
func (c *Channel) Close(ctx context.Context, id string, notPlanned bool) error {
	r, err := c.refOf(id)
	if err != nil {
		return err
	}

	var i issue
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/issues/%d", c.repo, r.number), nil, &i); err != nil {
		return err
	}

	if i.isPR() {
		return c.send(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", c.repo, r.number), map[string]string{"state": "closed"}, nil)
	}

	reason := "completed"
	if notPlanned {
		reason = "not_planned"
	}

	body := map[string]string{"state": "closed", "state_reason": reason}

	return c.send(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", c.repo, r.number), body, nil)
}

// Merge merges the PR that id belongs to with method (MergeSquash,
// MergeCommit or MergeRebase) and returns the resulting commit.
func (c *Channel) Merge(ctx context.Context, id, method string) (string, error) {
	if !slices.Contains([]string{MergeSquash, MergeCommit, MergeRebase}, method) {
		return "", fmt.Errorf("github merge: method %q: want %s, %s or %s", method, MergeSquash, MergeCommit, MergeRebase)
	}

	r, err := c.refOf(id)
	if err != nil {
		return "", err
	}

	var res struct {
		SHA string `json:"sha"`
	}

	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", c.repo, r.number)
	if err := c.send(ctx, http.MethodPut, path, map[string]string{"merge_method": method}, &res); err != nil {
		return "", err
	}

	return res.SHA, nil
}

// Review submits a review of the PR that id belongs to. event is
// ReviewApprove, ReviewRequestChanges or ReviewComment; the last two need a
// body.
func (c *Channel) Review(ctx context.Context, id, event, body string) error {
	if !slices.Contains([]string{ReviewApprove, ReviewRequestChanges, ReviewComment}, event) {
		return fmt.Errorf("github review: event %q: want %s, %s or %s", event, ReviewApprove, ReviewRequestChanges, ReviewComment)
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

	return c.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", c.repo, r.number), req, nil)
}
