// Package github implements the claude-bot channel protocol for one GitHub
// repository: new issues, pull requests, comments and reviews arrive as
// messages, and replies are posted as comments. It polls the REST API with a
// token, so no webhook or public URL is needed.
//
// A platform id is "<owner>/<repo>/<number>" for an issue or PR, with
// "#c<id>", "#r<id>" or "#v<id>" appended for a comment, an inline review
// comment or a review on it.
package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/markdown"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "github"

const (
	pollEvery    = 30 * time.Second
	clockSkew    = time.Minute // activity this much before the start still counts as new
	backoffStart = 5 * time.Second
	backoffCap   = 5 * time.Minute
	maxBodyLen   = 60000 // GitHub allows 65536 characters per comment
	ackReaction  = "eyes"
)

// Pull request merge methods.
const (
	MergeSquash = "squash"
	MergeCommit = "merge"
	MergeRebase = "rebase"
)

// Review events.
const (
	ReviewApprove        = "APPROVE"
	ReviewRequestChanges = "REQUEST_CHANGES"
	ReviewComment        = "COMMENT"
)

// Config holds a token with access to Repo.
type Config struct {
	Token string `json:"token"`
	Repo  string `json:"repo"` // "<owner>/<name>"
	// Proxy is "direct", "env" (HTTPS_PROXY etc., the default) or a proxy URL.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	if c.Token == "" {
		return errors.New("github: token is required")
	}

	owner, name, ok := strings.Cut(c.Repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("github: repo %q: want <owner>/<name>", c.Repo)
	}

	return nil
}

// Account is who the token acts as, and what it may do in the repository.
type Account struct {
	Login   string `json:"login"`
	Repo    string `json:"repo"`
	Private bool   `json:"private"`
	CanPush bool   `json:"can_push"` // needed to merge, close and label
}

// Channel is the GitHub adapter.
type Channel struct {
	http  *http.Client
	api   string // REST API root, replaced in tests
	token string
	repo  string
	log   *slog.Logger

	accountMu sync.Mutex
	account   *Account // cached once fetched successfully
}

var _ channel.Channel = (*Channel)(nil)

// New returns a GitHub channel for cfg.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := transport.ProxyOr(cfg.Proxy, transport.ProxyEnv)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	return &Channel{
		http:  transport.Client(proxy),
		api:   defaultAPI,
		token: cfg.Token,
		repo:  cfg.Repo,
		log:   log,
	}, nil
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return Name }

// Account checks the token and its access to the repository. Only a
// successful answer is cached.
func (c *Channel) Account(ctx context.Context) (Account, error) {
	c.accountMu.Lock()
	defer c.accountMu.Unlock()

	if c.account != nil {
		return *c.account, nil
	}

	var u user
	if err := c.get(ctx, "/user", nil, &u); err != nil {
		return Account{}, err
	}

	var r struct {
		FullName    string `json:"full_name"`
		Private     bool   `json:"private"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := c.get(ctx, c.repoPath(""), nil, &r); err != nil {
		return Account{}, fmt.Errorf("github: no access to %s with this token: %w", c.repo, err)
	}

	c.account = &Account{Login: u.Login, Repo: r.FullName, Private: r.Private, CanPush: r.Permissions.Push}

	return *c.account, nil
}

// Listen implements channel.Channel. On start it delivers the open issues and
// PRs still waiting for an answer, then polls for new ones, comments and
// reviews, from people only. Errors are retried with backoff; only a
// rejected token, or no access to the repository, ends it.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	acct, err := c.Account(ctx)
	if err != nil {
		return err
	}

	c.log.Info("github connected", "user", acct.Login, "repo", c.repo)
	ready()

	p := newPoller(c, acct.Login, deliver, time.Now().Add(-clockSkew))
	backoff := transport.Backoff{Start: backoffStart, Cap: backoffCap}
	imported := false

	for {
		// Every round sets err afresh: a failure must not stick and skip
		// the rounds after it.
		if imported {
			err = p.poll(ctx)
		} else if err = p.importOpen(ctx); err == nil {
			imported = true
			err = p.poll(ctx)
		}

		wait := pollEvery

		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // cancelled: the poll error is just the shutdown
		case fatal(err):
			return err
		case err != nil:
			wait = backoff.Next()
			c.log.Warn("github poll", "err", err, "retry_in", wait)
		default:
			backoff.Reset()
		}

		if !transport.Sleep(ctx, wait) {
			return nil
		}
	}
}

// Reply implements channel.Channel: a comment on the issue or PR, or a reply
// in the thread of an inline review comment, @-mentioning the sender.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	r, err := c.refOf(msg.ID)
	if err != nil {
		return err
	}

	if msg.Sender.Mention != "" {
		text = msg.Sender.Mention + " " + text
	}

	if r.kind == kindReviewComment && msg.ThreadID != "" {
		return c.post(ctx, c.repoPath("/pulls/%d/comments/%s/replies", r.number, msg.ThreadID), text)
	}

	return c.post(ctx, c.commentsPath(r.number), text)
}

// Send implements channel.Channel. chatID is "<owner>/<repo>/<number>".
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	r, err := c.parseRef(chatID)
	if err != nil {
		return err
	}

	return c.post(ctx, c.commentsPath(r.number), text)
}

func (c *Channel) commentsPath(number int) string {
	return c.repoPath("/issues/%d/comments", number)
}

// post comments text, split to fit GitHub's limit. Every part carries the
// Marker, so the poller knows it for the bot's own.
func (c *Channel) post(ctx context.Context, path, text string) error {
	for i, part := range markdown.Split(text, maxBodyLen) {
		if err := c.send(ctx, http.MethodPost, path, map[string]string{"body": withMarker(part)}, nil); err != nil {
			return fmt.Errorf("github comment (part %d): %w", i+1, err)
		}
	}

	return nil
}

func withMarker(body string) string {
	return body + "\n\n" + Marker
}

// Get implements channel.Channel. For an issue or PR the text includes the
// conversation so far, so one call gives the whole context.
func (c *Channel) Get(ctx context.Context, platformID string) (channel.Message, error) {
	r, err := c.parseRef(platformID)
	if err != nil {
		return channel.Message{}, err
	}

	switch r.kind {
	case kindComment:
		var cm comment

		err := c.get(ctx, c.repoPath("/issues/comments/%d", r.id), nil, &cm)

		return c.fromComment(cm), err
	case kindReviewComment:
		var rc reviewComment

		err := c.get(ctx, c.repoPath("/pulls/comments/%d", r.id), nil, &rc)

		return c.fromReviewComment(rc), err
	case kindReview:
		var rv review

		err := c.get(ctx, c.repoPath("/pulls/%d/reviews/%d", r.number, r.id), nil, &rv)

		return c.fromReview(r.number, rv), err
	}

	return c.conversation(ctx, r.number)
}

// conversation is an issue or PR with its latest comments appended.
func (c *Channel) conversation(ctx context.Context, number int) (channel.Message, error) {
	var i issue
	if err := c.get(ctx, c.repoPath("/issues/%d", number), nil, &i); err != nil {
		return channel.Message{}, err
	}

	msg := c.fromIssue(i)
	if i.Comments == 0 {
		return msg, nil
	}

	comments, err := c.latestComments(ctx, i, perPage)
	if err != nil {
		return channel.Message{}, err
	}

	var b strings.Builder

	b.WriteString(msg.Text)
	fmt.Fprintf(&b, "\n\n---\n%d comments", i.Comments)

	if len(comments) < i.Comments {
		fmt.Fprintf(&b, ", the latest %d", len(comments))
	}

	b.WriteString(":")

	for _, cm := range comments {
		fmt.Fprintf(&b, "\n\n%s (%s, %s):\n%s",
			cm.User.toUser().Mention, cm.CreatedAt.UTC().Format(time.DateTime), anchor(kindComment, cm.ID), cm.Body)
	}

	msg.Text = b.String()
	msg.Attachments = attachments(msg.Text)

	return msg, nil
}

// latestComments returns the last page of i's comments, at most n of them:
// the latest ones.
func (c *Channel) latestComments(ctx context.Context, i issue, n int) ([]comment, error) {
	if i.Comments == 0 {
		return nil, nil
	}

	lastPage := (i.Comments + n - 1) / n
	q := url.Values{"per_page": {strconv.Itoa(n)}, "page": {strconv.Itoa(lastPage)}}

	var comments []comment
	if err := c.get(ctx, c.commentsPath(i.Number), q, &comments); err != nil {
		return nil, err
	}

	return comments, nil
}

// Download implements channel.Channel. The token goes only to github.com:
// files of private repositories need it there, and Go drops it on a redirect
// to another host.
func (c *Channel) Download(ctx context.Context, _ channel.Message, att channel.Attachment, dir string) (string, error) {
	if att.Key == "" || attachmentURL.FindString(att.Key) != att.Key {
		return "", fmt.Errorf("github download: not a GitHub attachment URL: %q", att.Key)
	}

	var header http.Header
	if strings.HasPrefix(att.Key, "https://github.com/") {
		header = http.Header{"Authorization": {"Bearer " + c.token}}
	}

	data, err := transport.Download(ctx, c.http, att.Key, header)
	if err != nil {
		return "", fmt.Errorf("github download: %w", err)
	}

	name := channel.BaseName(att.Name)
	if name == "" {
		name = channel.PathSafe(att.Key)
	}

	path, err := channel.SaveFile(dir, name, data)
	if err != nil {
		return "", fmt.Errorf("github download: %w", err)
	}

	return path, nil
}

// Ack implements channel.Channel with an 👀 reaction. Reviews cannot carry
// reactions, so they are not acknowledged.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	r, err := c.refOf(msg.ID)
	if err != nil {
		return err
	}

	var path string

	switch r.kind {
	case kindIssue:
		path = c.repoPath("/issues/%d/reactions", r.number)
	case kindComment:
		path = c.repoPath("/issues/comments/%d/reactions", r.id)
	case kindReviewComment:
		path = c.repoPath("/pulls/comments/%d/reactions", r.id)
	default:
		return nil
	}

	if err := c.send(ctx, http.MethodPost, path, map[string]string{"content": ackReaction}, nil); err != nil {
		return fmt.Errorf("github ack: %w", err)
	}

	return nil
}
