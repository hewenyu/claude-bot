package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	repo = "o/r"
	me   = "maintainer"
)

var (
	alice   = user{Login: "alice", Type: userTypePerson}
	self    = user{Login: me, Type: userTypePerson}
	actions = user{Login: "github-actions[bot]", Type: "Bot"}
	start   = time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)
	before  = start.Add(-time.Hour)
	after   = start.Add(time.Minute)
)

type call struct {
	method, path, query, etag string
	body                      map[string]string
}

// fake is a GitHub API serving canned answers per "METHOD /path".
type fake struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	calls  []call
}

func newFake(t *testing.T) (*fake, *Channel) {
	t.Helper()

	f := &fake{routes: map[string]http.HandlerFunc{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	ch, err := New(Config{Token: "tok", Repo: repo}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	ch.api = srv.URL

	return f, ch
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	c := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, etag: r.Header.Get("If-None-Match")}

	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &c.body)
	}

	f.mu.Lock()
	f.calls = append(f.calls, c)
	h := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)

		return
	}

	if h == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))

		return
	}

	h(w, r)
}

func (f *fake) on(route string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.routes[route] = h
}

// json answers v on route.
func (f *fake) json(route string, v any) {
	f.on(route, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(v)
	})
}

func (f *fake) sent(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []call

	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}

	return out
}

// collect is a deliver func that keeps what it gets.
type collect struct{ msgs []channel.Message }

func (c *collect) deliver(m channel.Message) error {
	c.msgs = append(c.msgs, m)

	return nil
}

func (c *collect) ids() []string {
	var ids []string
	for _, m := range c.msgs {
		ids = append(ids, m.ID)
	}

	return ids
}

func TestRefs(t *testing.T) {
	t.Parallel()

	ch := &Channel{repo: repo}

	for _, r := range []ref{{number: 42}, {number: 7, kind: kindComment, id: 9}, {number: 7, kind: kindReviewComment, id: 10}, {number: 7, kind: kindReview, id: 11}} {
		id := ch.platformID(r)

		got, err := ch.parseRef(id)
		if err != nil || got != r {
			t.Errorf("parseRef(%q) = %+v, %v; want %+v", id, got, err, r)
		}
	}

	if got, err := ch.refOf("github:o/r/5#c6"); err != nil || got != (ref{number: 5, kind: kindComment, id: 6}) {
		t.Errorf("refOf(protocol id) = %+v, %v", got, err)
	}

	for _, bad := range []string{"x/y/1", "o/r/0", "o/r/abc", "o/r/1#", "o/r/1#x5", "o/r/1#c", "o/r/1#c-2", "o/r1"} {
		if _, err := ch.parseRef(bad); !errors.Is(err, channel.ErrInvalidID) {
			t.Errorf("parseRef(%q) err = %v, want ErrInvalidID", bad, err)
		}
	}
}

func TestRepoFromRemote(t *testing.T) {
	t.Parallel()

	for remote, want := range map[string]string{
		"https://github.com/hewenyu/claude-bot.git\n": "hewenyu/claude-bot",
		"https://github.com/hewenyu/claude-bot":       "hewenyu/claude-bot",
		"https://x-token@github.com/a/b.c.git":        "a/b.c",
		"git@github.com:a-b/c_d.git":                  "a-b/c_d",
		"ssh://git@github.com/a/b":                    "a/b",
		"git@gitlab.com:a/b.git":                      "",
		"https://github.com/a/b/c":                    "",
	} {
		got, ok := RepoFromRemote(remote)
		if got != want || ok != (want != "") {
			t.Errorf("RepoFromRemote(%q) = %q, %v; want %q", remote, got, ok, want)
		}
	}
}

func TestAttachments(t *testing.T) {
	t.Parallel()

	body := "截图：![shot](https://github.com/user-attachments/assets/3f2a-11) 日志 " +
		"[crash.log](https://github.com/user-attachments/files/123/crash.log) 旧图 " +
		`<img src="https://private-user-images.githubusercontent.com/1/2-abc.png?jwt=x"> ` +
		"再贴一次 https://github.com/user-attachments/assets/3f2a-11 外链 https://example.com/a.png"

	want := []channel.Attachment{
		{Kind: channel.AttachmentImage, Key: "https://github.com/user-attachments/assets/3f2a-11", Name: "3f2a-11"},
		{Kind: channel.AttachmentFile, Key: "https://github.com/user-attachments/files/123/crash.log", Name: "crash.log"},
		{Kind: channel.AttachmentImage, Key: "https://private-user-images.githubusercontent.com/1/2-abc.png?jwt=x", Name: "2-abc.png"},
	}

	if got := attachments(body); !reflect.DeepEqual(got, want) {
		t.Errorf("attachments()\n got  %+v\n want %+v", got, want)
	}
}

func TestImportOpen(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("GET /repos/o/r/issues", []issue{
		{Number: 1, Title: "崩了", Body: "复现步骤", User: alice, CreatedAt: before, HTMLURL: "https://github.com/o/r/issues/1"},
		{Number: 2, User: alice, Comments: 2, CreatedAt: before},                           // answered by the bot
		{Number: 3, User: alice, Comments: 1, CreatedAt: before, PullRequest: &struct{}{}}, // a person spoke last
		{Number: 4, User: actions, CreatedAt: before},                                      // opened by a bot
		{Number: 5, User: alice, Comments: 1, CreatedAt: before},                           // only CI commented
		{Number: 6, User: self, Comments: 1, CreatedAt: before},                            // the owner wrote by hand
	})
	f.json("GET /repos/o/r/issues/2/comments", []comment{{ID: 20, User: self, Body: "已修复" + "\n\n" + Marker}})
	f.json("GET /repos/o/r/issues/3/comments", []comment{{ID: 30, User: alice, Body: "还在吗", IssueURL: "https://api.github.com/repos/o/r/issues/3", HTMLURL: "https://github.com/o/r/pull/3#issuecomment-30"}})
	f.json("GET /repos/o/r/issues/5/comments", []comment{{ID: 50, User: actions, Body: "coverage 80%"}})
	f.json("GET /repos/o/r/issues/6/comments", []comment{{ID: 60, User: self, Body: "顺便看看这个", IssueURL: "https://api.github.com/repos/o/r/issues/6"}})

	var got collect

	p := newPoller(ch, me, got.deliver, start)
	if err := p.importOpen(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{"github:o/r/1", "github:o/r/3#c30", "github:o/r/5", "github:o/r/6#c60"}
	if !reflect.DeepEqual(got.ids(), want) {
		t.Fatalf("imported %v, want %v", got.ids(), want)
	}

	first := channel.Message{
		ID: "github:o/r/1", Channel: Name, ChatID: "o/r/1", ChatType: channel.ChatGroup,
		Sender:    channel.User{ID: "alice", Name: "alice", Mention: "@alice"},
		Text:      "Issue #1: 崩了\nhttps://github.com/o/r/issues/1\n\n复现步骤",
		CreatedAt: before,
	}
	if !reflect.DeepEqual(got.msgs[0], first) {
		t.Errorf("issue message\n got  %+v\n want %+v", got.msgs[0], first)
	}

	if m := got.msgs[1]; m.ReplyTo != "github:o/r/3" || !strings.HasPrefix(m.Text, "Comment on PR #3\n") {
		t.Errorf("comment message: reply_to %q, text %q", m.ReplyTo, m.Text)
	}
}

func TestPoll(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("GET /repos/o/r/issues", []issue{
		{Number: 8, User: alice, CreatedAt: before, UpdatedAt: after},                           // old, just updated
		{Number: 9, Title: "新需求", User: alice, CreatedAt: after, UpdatedAt: after},              // new
		{Number: 7, User: alice, CreatedAt: before, UpdatedAt: after, PullRequest: &struct{}{}}, // reviewed
	})
	f.json("GET /repos/o/r/pulls/7/reviews", []review{
		{ID: 70, User: alice, State: "CHANGES_REQUESTED", Body: "缺测试", SubmittedAt: after},
		{ID: 71, User: alice, State: "COMMENTED", SubmittedAt: after},                         // wrapper of inline comments
		{ID: 72, User: self, State: "COMMENTED", Body: "ok\n\n" + Marker, SubmittedAt: after}, // the bot's
		{ID: 73, User: alice, State: "APPROVED", SubmittedAt: before},                         // old
	})
	f.json("GET /repos/o/r/issues/comments", []comment{
		{ID: 80, User: alice, Body: "+1", IssueURL: "https://api.github.com/repos/o/r/issues/8", CreatedAt: after, UpdatedAt: after},
		{ID: 81, User: self, Body: "收到\n\n" + Marker, IssueURL: "https://api.github.com/repos/o/r/issues/8", CreatedAt: after, UpdatedAt: after},
		{ID: 82, User: alice, Body: "edited", IssueURL: "https://api.github.com/repos/o/r/issues/8", CreatedAt: before, UpdatedAt: after},
	})
	f.json("GET /repos/o/r/pulls/comments", []reviewComment{{
		ID: 501, User: alice, Body: "这里会 panic", CreatedAt: after, UpdatedAt: after,
		PullRequestURL: "https://api.github.com/repos/o/r/pulls/7", InReplyToID: 500, Path: "a.go", Line: 12,
		DiffHunk: "@@ -1,2 +1,2 @@\n-x\n+y",
	}})

	var got collect

	p := newPoller(ch, me, got.deliver, start)
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{"github:o/r/9", "github:o/r/7#v70", "github:o/r/8#c80", "github:o/r/7#r501"}
	if !reflect.DeepEqual(got.ids(), want) {
		t.Fatalf("delivered %v, want %v", got.ids(), want)
	}

	if rc := got.msgs[3]; rc.ThreadID != "500" || !strings.Contains(rc.Text, "a.go:12") || !strings.Contains(rc.Text, "+y") {
		t.Errorf("review comment: thread %q, text %q", rc.ThreadID, rc.Text)
	}

	if !p.issues.since.Equal(after) {
		t.Errorf("issues feed since = %v, want %v", p.issues.since, after)
	}
}

// A failure reading a PR's reviews must not lose them: the issues feed has
// already moved past the PR, so the next poll reads them from what is due.
func TestPoll_retriesReviewsAfterFailure(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)

	var issuesCalls atomic.Int32

	f.on("GET /repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
		var list []issue
		if issuesCalls.Add(1) == 1 {
			list = []issue{{Number: 7, User: alice, CreatedAt: before, UpdatedAt: after, PullRequest: &struct{}{}}}
		}

		_ = json.NewEncoder(w).Encode(list)
	})

	var reviewCalls atomic.Int32

	f.on("GET /repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, _ *http.Request) {
		if reviewCalls.Add(1) == 1 {
			http.Error(w, `{"message":"Server Error"}`, http.StatusInternalServerError)

			return
		}

		_ = json.NewEncoder(w).Encode([]review{{ID: 70, User: alice, State: "CHANGES_REQUESTED", Body: "缺测试", SubmittedAt: after}})
	})
	f.json("GET /repos/o/r/issues/comments", []comment{})
	f.json("GET /repos/o/r/pulls/comments", []reviewComment{})

	var got collect

	p := newPoller(ch, me, got.deliver, start)
	if err := p.poll(t.Context()); err == nil {
		t.Fatal("first poll: want the reviews error")
	}

	if err := p.poll(t.Context()); err != nil {
		t.Fatal(err)
	}

	if want := []string{"github:o/r/7#v70"}; !reflect.DeepEqual(got.ids(), want) {
		t.Fatalf("delivered %v, want %v", got.ids(), want)
	}

	if len(p.reviewsDue) != 0 {
		t.Errorf("reviews still due for %v", p.reviewsDue)
	}
}

func TestPollFeed_conditionalOnceIdle(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.on("GET /repos/o/r/pulls/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", `"v1"`)
		_ = json.NewEncoder(w).Encode([]reviewComment{{ID: 1, User: alice, CreatedAt: after, UpdatedAt: after}})
	})

	var got collect

	p := newPoller(ch, me, got.deliver, start)
	ctx := context.Background()

	for range 3 {
		if err := pollFeed(ctx, p, &p.reviewComments, func(rc reviewComment) time.Time { return rc.UpdatedAt }); err != nil {
			t.Fatal(err)
		}
	}

	var etags []string
	for _, c := range f.sent(http.MethodGet) {
		etags = append(etags, c.etag)
	}

	// 1: since moves, so the etag is dropped. 2: same answer, etag kept. 3: 304.
	if want := []string{"", "", `"v1"`}; !reflect.DeepEqual(etags, want) {
		t.Errorf("If-None-Match sent %q, want %q", etags, want)
	}
}

func TestReply(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("POST /repos/o/r/issues/42/comments", struct{}{})
	f.json("POST /repos/o/r/pulls/7/comments/500/replies", struct{}{})

	ctx := context.Background()
	issueMsg := channel.Message{ID: "github:o/r/42#c9", Sender: alice.toUser()}
	inline := channel.Message{ID: "github:o/r/7#r501", ThreadID: "500", Sender: alice.toUser()}

	if err := ch.Reply(ctx, issueMsg, "已修复"); err != nil {
		t.Fatal(err)
	}

	if err := ch.Reply(ctx, inline, "改了"); err != nil {
		t.Fatal(err)
	}

	if err := ch.Send(ctx, "o/r/42", "发版了"); err != nil {
		t.Fatal(err)
	}

	posts := f.sent(http.MethodPost)
	want := []call{
		{method: "POST", path: "/repos/o/r/issues/42/comments", body: map[string]string{"body": "@alice 已修复\n\n" + Marker}},
		{method: "POST", path: "/repos/o/r/pulls/7/comments/500/replies", body: map[string]string{"body": "@alice 改了\n\n" + Marker}},
		{method: "POST", path: "/repos/o/r/issues/42/comments", body: map[string]string{"body": "发版了\n\n" + Marker}},
	}

	if !reflect.DeepEqual(posts, want) {
		t.Errorf("posted\n got  %+v\n want %+v", posts, want)
	}
}

func TestAck(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	for _, route := range []string{"/repos/o/r/issues/4/reactions", "/repos/o/r/issues/comments/5/reactions", "/repos/o/r/pulls/comments/6/reactions"} {
		f.json("POST "+route, struct{}{})
	}

	ctx := context.Background()
	for _, id := range []string{"github:o/r/4", "github:o/r/4#c5", "github:o/r/4#r6", "github:o/r/4#v7"} {
		if err := ch.Ack(ctx, channel.Message{ID: id}); err != nil {
			t.Errorf("Ack(%s): %v", id, err)
		}
	}

	if posts := f.sent(http.MethodPost); len(posts) != 3 || posts[0].body["content"] != ackReaction {
		t.Errorf("reactions posted: %+v", posts)
	}
}

func TestGet_conversation(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("GET /repos/o/r/issues/3", issue{Number: 3, Title: "t", Body: "b", User: alice, Comments: 2, HTMLURL: "u", PullRequest: &struct{}{}})
	f.json("GET /repos/o/r/issues/3/comments", []comment{
		{ID: 1, User: self, Body: "看一下", CreatedAt: start},
		{ID: 2, User: alice, Body: "截图 https://github.com/user-attachments/assets/abc", CreatedAt: after},
	})

	msg, err := ch.Get(context.Background(), "o/r/3")
	if err != nil {
		t.Fatal(err)
	}

	want := "PR #3: t\nu\n\nb\n\n---\n2 comments:\n\n@maintainer (2026-10-28 06:00:00, #c1):\n看一下\n\n@alice (2026-10-28 06:01:00, #c2):\n截图 https://github.com/user-attachments/assets/abc"
	if msg.Text != want {
		t.Errorf("Text\n got  %q\n want %q", msg.Text, want)
	}

	if len(msg.Attachments) != 1 {
		t.Errorf("attachments of the conversation: %+v", msg.Attachments)
	}
}

func TestActions(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("GET /repos/o/r/issues/1", issue{Number: 1})
	f.json("GET /repos/o/r/issues/2", issue{Number: 2, PullRequest: &struct{}{}})
	f.json("PATCH /repos/o/r/issues/1", struct{}{})
	f.json("PATCH /repos/o/r/pulls/2", struct{}{})
	f.json("PUT /repos/o/r/pulls/2/merge", map[string]string{"sha": "abc123"})
	f.json("POST /repos/o/r/pulls/2/reviews", struct{}{})

	ctx := context.Background()

	if err := ch.Close(ctx, "github:o/r/1#c3", true); err != nil {
		t.Fatal(err)
	}

	if err := ch.Close(ctx, "o/r/2", false); err != nil {
		t.Fatal(err)
	}

	if sha, err := ch.Merge(ctx, "github:o/r/2", MergeSquash); err != nil || sha != "abc123" {
		t.Fatalf("Merge = %q, %v", sha, err)
	}

	if err := ch.Review(ctx, "o/r/2", ReviewRequestChanges, "缺测试"); err != nil {
		t.Fatal(err)
	}

	if _, err := ch.Merge(ctx, "o/r/2", "fast-forward"); err == nil {
		t.Error("Merge with an unknown method succeeded")
	}

	if err := ch.Review(ctx, "o/r/2", ReviewComment, ""); err == nil {
		t.Error("Review comment without a body succeeded")
	}

	var bodies []map[string]string

	for _, m := range []string{http.MethodPatch, http.MethodPut, http.MethodPost} {
		for _, c := range f.sent(m) {
			bodies = append(bodies, c.body)
		}
	}

	want := []map[string]string{
		{"state": "closed", "state_reason": "not_planned"},
		{"state": "closed"},
		{"merge_method": "squash"},
		{"event": "REQUEST_CHANGES", "body": "缺测试\n\n" + Marker},
	}
	if !reflect.DeepEqual(bodies, want) {
		t.Errorf("request bodies\n got  %v\n want %v", bodies, want)
	}
}

func TestListen_rejectedToken(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)
	ch.token = "wrong"

	err := ch.Listen(context.Background(), func(channel.Message) error { return nil }, func() {})
	if !fatal(err) {
		t.Errorf("Listen = %v, want a fatal 401", err)
	}
}

func TestDownload_onlyGitHubFiles(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)

	for _, key := range []string{"https://example.com/a.png", "https://github.com/user-attachments/assets/a https://evil"} {
		if _, err := ch.Download(context.Background(), channel.Message{}, channel.Attachment{Key: key}, t.TempDir()); err == nil {
			t.Errorf("Download(%q) succeeded", key)
		}
	}
}

func TestListen_pollsAgainAfterAFailure(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.json("GET /user", self)
	f.json("GET /repos/o/r", map[string]any{"full_name": repo})
	f.json("GET /repos/o/r/issues/comments", []comment{})
	f.json("GET /repos/o/r/pulls/comments", []reviewComment{})

	var calls atomic.Int32

	f.on("GET /repos/o/r/issues", func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1: // the import of open issues
			_ = json.NewEncoder(w).Encode([]issue{})
		case 2: // the first poll
			http.Error(w, `{"message":"Server Error"}`, http.StatusBadGateway)
		default:
			now := time.Now()
			_ = json.NewEncoder(w).Encode([]issue{{Number: 9, User: alice, CreatedAt: now, UpdatedAt: now}})
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan channel.Message, 1)

	go func() {
		_ = ch.Listen(ctx, func(m channel.Message) error {
			select {
			case got <- m:
			default:
			}

			return nil
		}, func() {})
	}()

	select {
	case m := <-got:
		if m.ID != "github:o/r/9" {
			t.Errorf("delivered %s, want github:o/r/9", m.ID)
		}
	case <-time.After(3 * backoffStart):
		t.Fatalf("nothing delivered after the failed poll; issues listed %d times", calls.Load())
	}
}
