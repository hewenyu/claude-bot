package github

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// Marker ends everything claude-bot posts. Comments by the token's own
// account that carry it are the bot's and are not delivered; the account's
// other comments, written by hand, are.
const Marker = "<!-- claude-bot -->"

const (
	userTypePerson = "User" // as opposed to "Bot" (Actions, Dependabot, apps)
	mentionMark    = "@"
	hunkLines      = 6 // diff lines quoted above a review comment
)

// What a platform id points at, after "#" (GitHub's own anchors are similar).
const (
	kindIssue         = 0   // the issue or PR itself: "<owner>/<repo>/<n>"
	kindComment       = 'c' // a conversation comment: "...#c<id>"
	kindReviewComment = 'r' // an inline review comment: "...#r<id>"
	kindReview        = 'v' // a review: "...#v<id>"
)

const kindSep = "#"

// ref locates an issue or PR, or something posted on it.
type ref struct {
	number int
	kind   byte
	id     int64 // of the comment or review
}

// thread is the platform id of the issue or PR itself, also the chat id.
func (c *Channel) thread(number int) string {
	return c.repo + "/" + strconv.Itoa(number)
}

func (c *Channel) platformID(r ref) string {
	if r.kind == kindIssue {
		return c.thread(r.number)
	}

	return c.thread(r.number) + kindSep + string(r.kind) + strconv.FormatInt(r.id, 10)
}

// parseRef reads a platform id of the configured repository.
func (c *Channel) parseRef(platformID string) (ref, error) {
	bad := fmt.Errorf("%w %q: want %s/<number>[#c|r|v<id>]", channel.ErrInvalidID, platformID, c.repo)

	rest, ok := strings.CutPrefix(platformID, c.repo+"/")
	if !ok {
		return ref{}, bad
	}

	num, anchor, hasAnchor := strings.Cut(rest, kindSep)

	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return ref{}, bad
	}

	r := ref{number: n}
	if !hasAnchor {
		return r, nil
	}

	if len(anchor) < 2 || !strings.ContainsRune("crv", rune(anchor[0])) {
		return ref{}, bad
	}

	r.kind = anchor[0]

	if r.id, err = strconv.ParseInt(anchor[1:], 10, 64); err != nil || r.id <= 0 {
		return ref{}, bad
	}

	return r, nil
}

// refOf parses a protocol id or a platform id.
func (c *Channel) refOf(id string) (ref, error) {
	if name, platformID, err := channel.SplitID(id); err == nil && name == Name {
		id = platformID
	}

	return c.parseRef(id)
}

type user struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

func (u user) person() bool { return u.Type == userTypePerson }

func (u user) toUser() channel.User {
	return channel.User{ID: u.Login, Name: u.Login, Mention: mentionMark + u.Login}
}

type issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	User        user      `json:"user"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	Comments    int       `json:"comments"`
	HTMLURL     string    `json:"html_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PullRequest *struct{} `json:"pull_request"`
}

func (i issue) isPR() bool { return i.PullRequest != nil }

// label names the issue or PR, e.g. "PR #43".
func (i issue) label() string {
	return label(i.isPR(), i.Number)
}

func label(pr bool, number int) string {
	if pr {
		return fmt.Sprintf("PR #%d", number)
	}

	return fmt.Sprintf("Issue #%d", number)
}

type comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      user      `json:"user"`
	HTMLURL   string    `json:"html_url"`
	IssueURL  string    `json:"issue_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type reviewComment struct {
	comment

	PullRequestURL string `json:"pull_request_url"`
	InReplyToID    int64  `json:"in_reply_to_id"`
	Path           string `json:"path"`
	Line           int    `json:"line"`
	OriginalLine   int    `json:"original_line"`
	DiffHunk       string `json:"diff_hunk"`
}

type review struct {
	ID          int64     `json:"id"`
	Body        string    `json:"body"`
	User        user      `json:"user"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// Review states with a verdict worth delivering even without a body.
var reviewVerdicts = map[string]string{
	"APPROVED":          "approved",
	"CHANGES_REQUESTED": "changes requested",
	"COMMENTED":         "commented",
	"DISMISSED":         "dismissed",
}

// numberOf reads the issue or PR number at the end of an API URL.
func numberOf(apiURL string) int {
	u, err := url.Parse(apiURL)
	if err != nil {
		return 0
	}

	n, _ := strconv.Atoi(path.Base(u.Path))

	return n
}

// fromBot reports whether a post is claude-bot's own.
func fromBot(u user, body, me string) bool {
	return strings.EqualFold(u.Login, me) && strings.Contains(body, Marker)
}

// message fills in what every delivered post has.
func (c *Channel) message(r ref, from user, text string, at time.Time) channel.Message {
	msg := channel.Message{
		ID:          channel.ID(Name, c.platformID(r)),
		Channel:     Name,
		ChatID:      c.thread(r.number),
		ChatType:    channel.ChatGroup, // public conversation: replies @-mention
		Sender:      from.toUser(),
		Text:        text,
		Attachments: attachments(text),
		CreatedAt:   at.UTC(),
	}

	if r.kind != kindIssue {
		msg.ReplyTo = channel.ID(Name, c.thread(r.number))
	}

	return msg
}

func (c *Channel) fromIssue(i issue) channel.Message {
	head := i.label()
	if i.Draft {
		head += " (draft)"
	}

	text := fmt.Sprintf("%s: %s\n%s\n\n%s", head, i.Title, i.HTMLURL, i.Body)

	return c.message(ref{number: i.Number}, i.User, strings.TrimSpace(text), i.CreatedAt)
}

func (c *Channel) fromComment(cm comment) channel.Message {
	n := numberOf(cm.IssueURL)
	pr := strings.Contains(cm.HTMLURL, "/pull/")
	text := fmt.Sprintf("Comment on %s\n%s\n\n%s", label(pr, n), cm.HTMLURL, cm.Body)

	return c.message(ref{number: n, kind: kindComment, id: cm.ID}, cm.User, strings.TrimSpace(text), cm.CreatedAt)
}

func (c *Channel) fromReviewComment(rc reviewComment) channel.Message {
	n := numberOf(rc.PullRequestURL)

	line := rc.Line
	if line == 0 {
		line = rc.OriginalLine // the line is gone from the latest diff
	}

	text := fmt.Sprintf("Review comment on %s, %s:%d\n%s\n\n```diff\n%s\n```\n\n%s",
		label(true, n), rc.Path, line, rc.HTMLURL, hunkTail(rc.DiffHunk), rc.Body)

	msg := c.message(ref{number: n, kind: kindReviewComment, id: rc.ID}, rc.User, strings.TrimSpace(text), rc.CreatedAt)

	// Replies go to the thread's first comment; GitHub has no nesting.
	root := rc.ID
	if rc.InReplyToID != 0 {
		root = rc.InReplyToID
	}

	msg.ThreadID = strconv.FormatInt(root, 10)

	return msg
}

func (c *Channel) fromReview(number int, rv review) channel.Message {
	verdict := reviewVerdicts[rv.State]
	if verdict == "" {
		verdict = strings.ToLower(rv.State)
	}

	text := fmt.Sprintf("Review on %s: %s\n%s\n\n%s", label(true, number), verdict, rv.HTMLURL, rv.Body)

	return c.message(ref{number: number, kind: kindReview, id: rv.ID}, rv.User, strings.TrimSpace(text), rv.SubmittedAt)
}

// hunkTail keeps the last lines of a diff hunk: the commented line and what
// leads up to it.
func hunkTail(hunk string) string {
	lines := strings.Split(strings.TrimRight(hunk, "\n"), "\n")
	if len(lines) > hunkLines {
		lines = lines[len(lines)-hunkLines:]
	}

	return strings.Join(lines, "\n")
}

// Files people attach to issues and comments are uploaded to these places.
var attachmentURL = regexp.MustCompile(
	`https://(?:github\.com/user-attachments/(?:assets|files)/|(?:private-)?user-images\.githubusercontent\.com/)[^\s)\]"'<>]+`)

// attachments lists the uploaded files a Markdown body links to.
func attachments(body string) []channel.Attachment {
	var out []channel.Attachment

	seen := map[string]bool{}

	for _, u := range attachmentURL.FindAllString(body, -1) {
		if seen[u] {
			continue
		}

		seen[u] = true

		kind := channel.AttachmentImage
		if strings.Contains(u, "/user-attachments/files/") {
			kind = channel.AttachmentFile
		}

		out = append(out, channel.Attachment{Kind: kind, Key: u, Name: channel.BaseName(path.Base(stripQuery(u)))})
	}

	return out
}

func stripQuery(u string) string {
	s, _, _ := strings.Cut(u, "?")

	return s
}

// remoteRepo matches the GitHub remotes git uses: https://github.com/o/r.git,
// git@github.com:o/r.git and ssh://git@github.com/o/r.
var remoteRepo = regexp.MustCompile(`^(?:https://(?:[^@/]+@)?github\.com/|git@github\.com:|ssh://git@github\.com/)([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)

// RepoFromRemote returns "<owner>/<name>" of a GitHub remote URL.
func RepoFromRemote(remote string) (string, bool) {
	m := remoteRepo.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", false
	}

	return m[1], true
}
