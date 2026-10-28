package github

import (
	"context"
	"net/url"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// feed is one listing polled for changes. since only moves forward when
// something changed, so an idle feed asks the same question every time and
// GitHub answers 304, which does not count against the rate limit.
type feed struct {
	path  string
	since time.Time
	etag  string
}

func (f *feed) query() url.Values {
	return url.Values{
		"sort":      {"updated"},
		"direction": {"asc"},
		"since":     {f.since.UTC().Format(time.RFC3339)},
	}
}

// advance moves since to the latest update seen.
func (f *feed) advance(t time.Time) {
	if t.After(f.since) {
		f.since = t
	}
}

// poller finds what is new in the repository since start. The inbox drops
// anything delivered twice, so overlapping windows are harmless.
type poller struct {
	c       *Channel
	me      string // login of the token's account
	deliver func(channel.Message) error
	start   time.Time

	issues, comments, reviewComments feed
}

func newPoller(c *Channel, me string, deliver func(channel.Message) error, start time.Time) *poller {
	return &poller{
		c: c, me: me, deliver: deliver, start: start,
		issues:         feed{path: c.repoPath("/issues"), since: start},
		comments:       feed{path: c.repoPath("/issues/comments"), since: start},
		reviewComments: feed{path: c.repoPath("/pulls/comments"), since: start},
	}
}

func (p *poller) send(msg channel.Message) {
	if err := p.deliver(msg); err != nil {
		p.c.log.Error("github deliver", "id", msg.ID, "err", err)
	}
}

// fresh reports whether a post is new and from someone other than the bot.
func (p *poller) fresh(u user, body string, at time.Time) bool {
	return !at.Before(p.start) && u.person() && !fromBot(u, body, p.me)
}

// poll delivers new issues and PRs, comments, inline review comments and
// reviews.
func (p *poller) poll(ctx context.Context) error {
	var updatedPRs []int

	err := pollFeed(ctx, p, &p.issues, func(i issue) time.Time {
		if p.fresh(i.User, i.Body, i.CreatedAt) {
			p.send(p.c.fromIssue(i))
		}

		if i.isPR() {
			updatedPRs = append(updatedPRs, i.Number)
		}

		return i.UpdatedAt
	})
	if err != nil {
		return err
	}

	// Reviews have no repository-wide listing; a review updates its PR.
	for _, n := range updatedPRs {
		if err := p.pollReviews(ctx, n); err != nil {
			return err
		}
	}

	err = pollFeed(ctx, p, &p.comments, func(cm comment) time.Time {
		if p.fresh(cm.User, cm.Body, cm.CreatedAt) {
			p.send(p.c.fromComment(cm))
		}

		return cm.UpdatedAt
	})
	if err != nil {
		return err
	}

	return pollFeed(ctx, p, &p.reviewComments, func(rc reviewComment) time.Time {
		if p.fresh(rc.User, rc.Body, rc.CreatedAt) {
			p.send(p.c.fromReviewComment(rc))
		}

		return rc.UpdatedAt
	})
}

// pollFeed lists what changed in f and hands each item to handle, which
// returns the item's update time.
func pollFeed[T any](ctx context.Context, p *poller, f *feed, handle func(T) time.Time) error {
	latest := f.since

	res, err := listAll(ctx, p.c, f.path, f.query(), f.etag, func(it T) {
		if t := handle(it); t.After(latest) {
			latest = t
		}
	})
	if err != nil {
		return err
	}

	if !res.notModified {
		// The etag answers the old query; a new since needs a new one.
		f.etag = ""
		if !latest.After(f.since) {
			f.etag = res.etag
		}

		f.advance(latest)
	}

	return nil
}

// pollReviews delivers the reviews of PR n submitted since start. Inline
// comments arrive on their own; a review is delivered for its summary or
// its verdict.
func (p *poller) pollReviews(ctx context.Context, n int) error {
	path := p.c.repoPath("/pulls/%d/reviews", n)

	_, err := listAll(ctx, p.c, path, nil, "", func(rv review) {
		if rv.Body == "" && rv.State == reviewCommented {
			return // just the wrapper of inline comments
		}

		if p.fresh(rv.User, rv.Body, rv.SubmittedAt) {
			p.send(p.c.fromReview(n, rv))
		}
	})

	return err
}

// importOpen delivers the open issues and PRs still waiting for an answer:
// those whose latest comment is not the bot's. The latest comment is
// delivered (it replies to its issue), or the issue itself when nobody has
// commented yet. Issues answered already, or marked done, are dropped by the
// inbox.
func (p *poller) importOpen(ctx context.Context) error {
	var open []issue

	_, err := listAll(ctx, p.c, p.c.repoPath("/issues"), url.Values{"state": {stateOpen}}, "", func(i issue) {
		open = append(open, i)
	})
	if err != nil {
		return err
	}

	for _, i := range open {
		if !i.User.person() {
			continue
		}

		last, err := p.lastComment(ctx, i)
		if err != nil {
			return err
		}

		switch {
		case last == nil || !last.User.person():
			if !fromBot(i.User, i.Body, p.me) {
				p.send(p.c.fromIssue(i))
			}
		case !fromBot(last.User, last.Body, p.me):
			p.send(p.c.fromComment(*last))
		}
	}

	return nil
}

// lastComment is the latest comment on i, nil if there is none.
func (p *poller) lastComment(ctx context.Context, i issue) (*comment, error) {
	cs, err := p.c.latestComments(ctx, i, 1)
	if err != nil || len(cs) == 0 {
		return nil, err
	}

	return &cs[0], nil
}
