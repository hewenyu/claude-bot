package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	clientID     = "dingtestapp"
	clientSecret = "secret"
	accessToken  = "token-1"
	botUser      = "$:LWCP_v1:$bot"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestFromRobotMessage_groupMention(t *testing.T) {
	t.Parallel()

	m := robotMessage{
		MsgID: "msgA", MsgType: msgText, ConversationID: "cidG", ConversationType: conversationGroup,
		SenderID: "$:LWCP_v1:$alice", SenderStaffID: "staff-alice", SenderNick: "Alice",
		ChatbotUserID: botUser, IsInAtList: true,
		AtUsers:  []atUser{{DingtalkID: botUser}, {DingtalkID: "$:LWCP_v1:$bob", StaffID: "staff-bob"}},
		CreateAt: 1792813575123,
		Text:     textContent{Content: "  登录页报 500  "},
	}

	got, ok := fromRobotMessage(m)
	if !ok {
		t.Fatal("mention of the bot was dropped")
	}

	want := channel.Message{
		ID:        "dingtalk:cidG/msgA",
		Channel:   Name,
		ChatID:    "cidG",
		ChatType:  channel.ChatGroup,
		Sender:    channel.User{ID: "staff-alice", Name: "Alice", Mention: "@Alice"},
		Text:      "登录页报 500",
		Mentions:  []channel.User{{ID: "staff-bob"}},
		CreatedAt: time.UnixMilli(1792813575123).UTC(),
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromRobotMessage()\n got  %+v\n want %+v", got, want)
	}
}

func TestFromRobotMessage_kinds(t *testing.T) {
	t.Parallel()

	direct := robotMessage{MsgID: "m", ConversationID: "cid", ConversationType: "1", SenderID: "outsider"}

	with := func(msgType, contentJSON string) robotMessage {
		m := direct
		m.MsgType, m.Content = msgType, json.RawMessage(contentJSON)

		return m
	}

	tests := []struct {
		name     string
		m        robotMessage
		wantText string
		wantAtts []channel.Attachment
	}{
		{
			name:     "rich text keeps text and images",
			m:        with(msgRichText, `{"richText":[{"text":"看这个"},{"type":"picture","downloadCode":"pic1"},{"text":" 报错"},{"type":"picture","downloadCode":"pic3"}]}`),
			wantText: "看这个 报错",
			wantAtts: []channel.Attachment{
				{Kind: channel.AttachmentImage, Key: "pic1", Name: "image-1"},
				{Kind: channel.AttachmentImage, Key: "pic3", Name: "image-2"},
			},
		},
		{
			name:     "picture",
			m:        with(msgPicture, `{"downloadCode":"pic2"}`),
			wantAtts: []channel.Attachment{{Kind: channel.AttachmentImage, Key: "pic2", Name: "image-1"}},
		},
		{
			name:     "file keeps a safe name",
			m:        with(msgFile, `{"downloadCode":"f1","fileName":"../crash.log"}`),
			wantAtts: []channel.Attachment{{Kind: channel.AttachmentFile, Key: "f1", Name: "crash.log"}},
		},
		{
			name:     "voice is its transcript",
			m:        with(msgAudio, `{"downloadCode":"a1","recognition":"帮我看下日志"}`),
			wantText: "帮我看下日志",
			wantAtts: []channel.Attachment{{Kind: channel.AttachmentFile, Key: "a1", Name: "file-1"}},
		},
		{
			name:     "unknown types are named",
			m:        with("interactiveCard", `{}`),
			wantText: "[interactiveCard message]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := fromRobotMessage(tt.m)
			if !ok {
				t.Fatal("direct message was dropped")
			}

			if got.ChatType != channel.ChatDirect || got.Sender.ID != "outsider" {
				t.Errorf("chat type %q, sender %q: want direct, the sender id when there is no staff id", got.ChatType, got.Sender.ID)
			}

			if got.Text != tt.wantText || !reflect.DeepEqual(got.Attachments, tt.wantAtts) {
				t.Errorf("text %q, attachments %+v; want %q, %+v", got.Text, got.Attachments, tt.wantText, tt.wantAtts)
			}
		})
	}
}

func TestFromRobotMessage_filters(t *testing.T) {
	t.Parallel()

	tests := map[string]robotMessage{
		"group message without @bot": {MsgID: "m", ConversationID: "c", ConversationType: conversationGroup},
		"no message id":              {ConversationID: "c", ConversationType: "1"},
		"no conversation":            {MsgID: "m", ConversationType: "1"},
	}

	for name, m := range tests {
		if _, ok := fromRobotMessage(m); ok {
			t.Errorf("%s: delivered", name)
		}
	}
}

func TestTitle(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"## 修好了\n\n细节":          "修好了",
		"":                      "claude-bot",
		"```\ncode":             "claude-bot",
		strings.Repeat("长", 40): strings.Repeat("长", maxTitleLen-1) + "…",
	}

	for in, want := range tests {
		if got := title(in); got != want {
			t.Errorf("title(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeDingTalk serves the OpenAPI and a Stream endpoint.
type fakeDingTalk struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	tokenCalls int
	sent       []sentMessage
	acks       []ack
	frames     []frame // pushed on every Stream connection
	authStatus int     // answer of the token endpoint, if not 200
}

type sentMessage struct {
	Path string
	Body map[string]any
}

func newFake(t *testing.T) *fakeDingTalk {
	t.Helper()

	f := &fakeDingTalk{t: t}

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pathAccessToken, f.token)
	mux.HandleFunc("POST "+pathOpenStream, f.openStream)
	mux.HandleFunc("GET /stream", f.stream)
	mux.HandleFunc("POST "+pathDownload, f.authed(func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, map[string]string{"downloadUrl": f.srv.URL + "/file"})
	}))
	mux.HandleFunc("GET /file", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngHeader) })

	for _, path := range []string{pathGroupSend, pathDirectSend} {
		mux.HandleFunc("POST "+path, f.authed(func(w http.ResponseWriter, body map[string]any) {
			f.mu.Lock()
			f.sent = append(f.sent, sentMessage{Path: path, Body: body})
			f.mu.Unlock()

			writeJSON(w, map[string]string{})
		}))
	}

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeDingTalk) channel() *Channel {
	f.t.Helper()

	c, err := New(Config{ClientID: clientID, ClientSecret: clientSecret}, slog.New(slog.DiscardHandler))
	if err != nil {
		f.t.Fatal(err)
	}

	c.api = f.srv.URL

	return c
}

func decodeBody(r *http.Request) map[string]any {
	var body map[string]any

	_ = json.NewDecoder(r.Body).Decode(&body)

	return body
}

// authed serves an OpenAPI path that needs the access token.
func (f *fakeDingTalk) authed(h func(http.ResponseWriter, map[string]any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tokenHeader) != accessToken {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		h(w, decodeBody(r))
	}
}

func (f *fakeDingTalk) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.tokenCalls++

	if f.authStatus != 0 {
		w.WriteHeader(f.authStatus)
		_, _ = w.Write([]byte(`{"code":"invalidClientIdOrSecret","message":"bad credentials"}`))

		return
	}

	if body := decodeBody(r); body["appKey"] != clientID || body["appSecret"] != clientSecret {
		f.t.Errorf("token request %v", body)
	}

	writeJSON(w, map[string]any{"accessToken": accessToken, "expireIn": 7200})
}

func (f *fakeDingTalk) openStream(w http.ResponseWriter, r *http.Request) {
	if body := decodeBody(r); body["clientId"] != clientID || body["clientSecret"] != clientSecret {
		f.t.Errorf("stream request %v", body)
	}

	writeJSON(w, map[string]string{"endpoint": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/stream", "ticket": "t1"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// stream pushes the frames, records the acks, and then holds the connection
// until the client closes it.
func (f *fakeDingTalk) stream(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("ticket") != "t1" {
		f.t.Errorf("stream opened without the ticket: %s", r.URL)
	}

	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		f.t.Error(err)

		return
	}
	defer conn.Close() //nolint:errcheck // test server

	f.mu.Lock()
	frames := f.frames
	f.mu.Unlock()

	for _, fr := range frames {
		if err := conn.WriteJSON(fr); err != nil {
			return
		}

		var a ack
		if err := conn.ReadJSON(&a); err != nil {
			return
		}

		f.mu.Lock()
		f.acks = append(f.acks, a)
		f.mu.Unlock()
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func TestListen(t *testing.T) {
	t.Parallel()

	f := newFake(t)

	data, _ := json.Marshal(robotMessage{
		MsgID: "msgA", MsgType: msgText, ConversationID: "cidD", ConversationType: "1",
		SenderStaffID: "staff-alice", SenderNick: "Alice", Text: textContent{Content: "你好"},
	})
	f.frames = []frame{
		{Type: frameSystem, Headers: frameHeaders{MessageID: "p1", Topic: topicPing}, Data: `{"opaque":"x"}`},
		{Type: frameCallback, Headers: frameHeaders{MessageID: "c1", Topic: topicBotMessage}, Data: string(data)},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan channel.Message, 1)
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)

	go func() {
		done <- f.channel().Listen(ctx, func(m channel.Message) error {
			got <- m

			return nil
		}, func() { ready <- struct{}{} })
	}()

	select {
	case m := <-got:
		if m.ID != "dingtalk:cidD/msgA" || m.Text != "你好" || m.Sender.ID != "staff-alice" {
			t.Errorf("delivered %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message delivered")
	}

	select {
	case <-ready:
	default:
		t.Error("ready was not called")
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("Listen() = %v after cancel", err)
	}

	// The acks went out before the delivery; the fake records them on its own time.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		f.mu.Lock()
		n := len(f.acks)
		f.mu.Unlock()

		if n >= 2 {
			break
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	want := []ack{
		{Code: ackOK, Headers: frameHeaders{MessageID: "p1", ContentType: "application/json"}, Message: ackMessage, Data: `{"opaque":"x"}`},
		{Code: ackOK, Headers: frameHeaders{MessageID: "c1", ContentType: "application/json"}, Message: ackMessage, Data: ackEmptyResponse},
	}
	if !reflect.DeepEqual(f.acks, want) {
		t.Errorf("acks\n got  %+v\n want %+v", f.acks, want)
	}
}

func TestListen_refusedCredentials(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.authStatus = http.StatusBadRequest

	err := f.channel().Listen(context.Background(), func(channel.Message) error { return nil }, func() {})

	ae, ok := errors.AsType[*APIError](err)
	if !ok || !fatal(err) || ae.Code != "invalidClientIdOrSecret" {
		t.Errorf("Listen() = %v, want the refusal", err)
	}
}

func TestReplyAndSend(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	c := f.channel()
	ctx := context.Background()

	direct := channel.Message{ID: "dingtalk:cidD/m1", ChatType: channel.ChatDirect, Sender: channel.User{ID: "staff-alice", Mention: "@Alice"}}
	group := channel.Message{ID: "dingtalk:cidG/m2", ChatType: channel.ChatGroup, Sender: channel.User{ID: "staff-bob", Mention: "@Bob"}}

	for _, err := range []error{
		c.Reply(ctx, direct, "## 已修复\n见 v0.2.0"),
		c.Reply(ctx, group, "收到"),
		c.Send(ctx, "cidG", strings.Repeat("x\n", maxMarkdownLen/2+10)), // just over the limit: two parts
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.tokenCalls != 1 {
		t.Errorf("access token fetched %d times, want once", f.tokenCalls)
	}

	if len(f.sent) != 4 {
		t.Fatalf("sent %d messages, want 4", len(f.sent))
	}

	want := []struct {
		path, to    string // the path, and the field naming the recipient
		recipient   any
		title, text string
	}{
		{pathDirectSend, "userIds", []any{"staff-alice"}, "已修复", "## 已修复\n见 v0.2.0"},
		{pathGroupSend, "openConversationId", "cidG", "@Bob 收到", "@Bob 收到"},
		{pathGroupSend, "openConversationId", "cidG", "x", ""},
		{pathGroupSend, "openConversationId", "cidG", "x", ""},
	}

	for i, w := range want {
		checkSent(t, f.sent[i], w.path, w.to, w.recipient, w.title, w.text)
	}
}

// checkSent compares a robot message with what was expected; an empty text
// is not compared.
func checkSent(t *testing.T, s sentMessage, path, to string, recipient any, title, text string) {
	t.Helper()

	if s.Path != path || !reflect.DeepEqual(s.Body[to], recipient) || s.Body["robotCode"] != clientID || s.Body["msgKey"] != msgKeyMarkdown {
		t.Errorf("sent to %s: %v, want %s with %s %v", s.Path, s.Body, path, to, recipient)
	}

	raw, _ := s.Body["msgParam"].(string)

	var p map[string]string
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("msgParam %q: %v", raw, err)
	}

	if p["title"] != title || text != "" && p["text"] != text {
		t.Errorf("msgParam title %q text %q, want %q %q", p["title"], p["text"], title, text)
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	dir := t.TempDir()
	msg := channel.Message{ID: "dingtalk:cidD/msgA"}

	path, err := f.channel().Download(context.Background(), msg, channel.Attachment{Kind: channel.AttachmentImage, Key: "pic1", Name: "image-1"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, "image-1.png"); path != want {
		t.Errorf("saved to %s, want %s", path, want)
	}

	if data, _ := os.ReadFile(path); string(data) != string(pngHeader) {
		t.Errorf("content %q", data)
	}
}

func TestGet_unsupported(t *testing.T) {
	t.Parallel()

	if _, err := newFake(t).channel().Get(context.Background(), "cid/m"); !errors.Is(err, ErrNoGet) {
		t.Errorf("Get() = %v, want ErrNoGet", err)
	}
}
