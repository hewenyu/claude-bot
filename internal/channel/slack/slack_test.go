package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	botUser  = "UBOT"
	appToken = "xapp-1-test"
	botToken = "xoxb-test"
)

func TestFromEvent_channelMention(t *testing.T) {
	t.Parallel()

	ev := event{
		Type: eventMention, Channel: "C42", User: "UALICE",
		Text:     "<@UBOT> 登录页报 500 &amp; 白屏，<@UBOB|bob> 也遇到了 &lt;b&gt; <@UBOB>",
		TS:       "1792813575.123456",
		ThreadTS: "1792813000.000100",
		Files: []file{
			{ID: "F1", Name: "shot.png", Mimetype: "image/png"},
			{ID: "F2", Name: "crash.log", Mimetype: "text/plain"},
		},
	}

	got, ok := fromEvent(ev, botUser)
	if !ok {
		t.Fatal("mention of the bot was dropped")
	}

	want := channel.Message{
		ID:       "slack:C42/1792813575.123456",
		Channel:  "slack",
		ChatID:   "C42",
		ChatType: channel.ChatGroup,
		ThreadID: "1792813000.000100",
		ReplyTo:  "slack:C42/1792813000.000100",
		Sender:   channel.User{ID: "UALICE", Mention: "<@UALICE>"},
		Text:     "登录页报 500 & 白屏，<@UBOB> 也遇到了 <b> <@UBOB>",
		Mentions: []channel.User{{ID: "UBOB", Mention: "<@UBOB>"}},
		Attachments: []channel.Attachment{
			{Kind: channel.AttachmentImage, Key: "F1", Name: "shot.png"},
			{Kind: channel.AttachmentFile, Key: "F2", Name: "crash.log"},
		},
		CreatedAt: time.Date(2026, 10, 24, 3, 46, 15, 123456000, time.UTC),
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromEvent()\n got  %+v\n want %+v", got, want)
	}
}

func TestFromEvent_filters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ev   event
		want bool
	}{
		{name: "direct message is delivered", ev: event{Type: eventMessage, ChannelType: channelTypeIM, Channel: "D1", User: "U1", TS: "1.1"}, want: true},
		{name: "direct message with files is delivered", ev: event{Type: eventMessage, Subtype: "file_share", ChannelType: channelTypeIM, User: "U1"}, want: true},
		{name: "channel message without mention is ignored", ev: event{Type: eventMessage, ChannelType: "channel", User: "U1"}},
		{name: "edits are ignored", ev: event{Type: eventMessage, Subtype: "message_changed", ChannelType: channelTypeIM, User: "U1"}},
		{name: "bot messages are ignored", ev: event{Type: eventMention, User: "U1", BotID: "B1"}},
		{name: "the bot's own messages are ignored", ev: event{Type: eventMessage, ChannelType: channelTypeIM, User: botUser}},
		{name: "events without a user are ignored", ev: event{Type: eventMention}},
		{name: "other events are ignored", ev: event{Type: "reaction_added", User: "U1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := fromEvent(tt.ev, botUser)
			if ok != tt.want {
				t.Fatalf("fromEvent() delivered = %v, want %v", ok, tt.want)
			}

			if ok && got.ChatType != channel.ChatDirect {
				t.Errorf("chat type = %q, want %q", got.ChatType, channel.ChatDirect)
			}
		})
	}
}

func TestParseTS(t *testing.T) {
	t.Parallel()

	if got := parseTS("1792813575.123456"); !got.Equal(time.Unix(1792813575, 123456000)) {
		t.Errorf("parseTS() = %v", got)
	}

	if got := parseTS("nope"); !got.IsZero() {
		t.Errorf("parseTS(nope) = %v, want zero", got)
	}
}

func TestConfig_validate(t *testing.T) {
	t.Parallel()

	for _, cfg := range []Config{
		{AppToken: botToken, BotToken: botToken},
		{AppToken: appToken, BotToken: appToken},
		{},
	} {
		if _, err := New(cfg, slog.New(slog.DiscardHandler)); err == nil {
			t.Errorf("New(%+v) accepted swapped or missing tokens", cfg)
		}
	}
}

func TestFatal(t *testing.T) {
	t.Parallel()

	if !fatal(&APIError{Code: "invalid_auth"}) || fatal(&APIError{Code: "internal_error"}) || fatal(errors.New("eof")) {
		t.Error("fatal() misclassifies errors")
	}
}

// fakeSlack serves the Web API and a Socket Mode endpoint.
type fakeSlack struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	calls  []call
	frames []string          // sent on each socket connection after hello
	acks   chan string       // envelope ids acknowledged
	answer map[string]string // method -> raw JSON answer
}

type call struct {
	Method string
	Token  string
	Params url.Values
}

func newFake(t *testing.T) (*fakeSlack, *Channel) {
	t.Helper()

	f := &fakeSlack{t: t, acks: make(chan string, 10), answer: map[string]string{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)

	ch, err := New(Config{AppToken: appToken, BotToken: botToken, Proxy: "direct"}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	ch.api = f.srv.URL + "/api"

	return f, ch
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/socket":
		f.socket(w, r)

		return
	case "/files/F1":
		if r.Header.Get("Authorization") != "Bearer "+botToken {
			http.Error(w, "no", http.StatusForbidden)

			return
		}

		_, _ = io.WriteString(w, "png bytes")

		return
	}

	method := strings.TrimPrefix(r.URL.Path, "/api/")
	_ = r.ParseForm()

	f.mu.Lock()
	f.calls = append(f.calls, call{Method: method, Token: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), Params: r.PostForm})
	answer, ok := f.answer[method]
	f.mu.Unlock()

	if ok {
		_, _ = io.WriteString(w, answer)

		return
	}

	switch method {
	case "auth.test":
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"UBOT","user":"proj_bot","bot_id":"B1","team":"Acme","url":"https://acme.slack.com/"}`)
	case "apps.connections.open":
		_, _ = io.WriteString(w, `{"ok":true,"url":"ws`+strings.TrimPrefix(f.srv.URL, "http")+`/socket"}`)
	case "files.info":
		_, _ = io.WriteString(w, `{"ok":true,"file":{"id":"F1","name":"../shot.png","url_private_download":"`+f.srv.URL+`/files/F1"}}`)
	default:
		_, _ = io.WriteString(w, `{"ok":true}`)
	}
}

func (f *fakeSlack) socket(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close() //nolint:errcheck // test server

	f.mu.Lock()
	frames := f.frames
	f.frames = nil
	f.mu.Unlock()

	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","num_connections":1}`))

	for _, fr := range frames {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fr))
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}

		_ = json.Unmarshal(data, &ack)
		f.acks <- ack.EnvelopeID
	}
}

func (f *fakeSlack) called(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []call

	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}

	return out
}

func TestListen_deliversAndAcks(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.frames = []string{
		`{"envelope_id":"e1","type":"events_api","payload":{"event":{"type":"message","channel_type":"channel","channel":"C1","user":"U1","text":"chatter","ts":"1.1"}}}`,
		`{"envelope_id":"e2","type":"events_api","payload":{"event":{"type":"app_mention","channel":"C1","user":"U1","text":"<@UBOT> hi","ts":"1.2"}}}`,
		`{"type":"disconnect","reason":"refresh_requested"}`,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan channel.Message, 2)
	readies := make(chan struct{}, 4)
	done := make(chan error, 1)

	go func() {
		done <- ch.Listen(ctx, func(m channel.Message) error { got <- m; return nil }, func() { readies <- struct{}{} })
	}()

	if m := receive(t, got, "a delivered message"); m.ID != "slack:C1/1.2" || m.Text != "hi" || m.ChatType != channel.ChatGroup {
		t.Errorf("delivered %+v", m)
	}

	for _, want := range []string{"e1", "e2"} {
		if id := receive(t, f.acks, "an acknowledgement"); id != want {
			t.Errorf("acked %q, want %q", id, want)
		}
	}

	// The refresh makes it connect again, and say ready again.
	receive(t, readies, "ready")
	receive(t, readies, "ready after the requested reconnect")

	cancel()

	if err := <-done; err != nil {
		t.Errorf("Listen() = %v after cancel, want nil", err)
	}

	for _, c := range f.called("apps.connections.open") {
		if c.Token != appToken {
			t.Errorf("apps.connections.open used token %q, want the app token", c.Token)
		}
	}
}

// receive waits for a value on ch.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s within 5s", what)

		var zero T

		return zero
	}
}

func TestListen_linkDisabledEnds(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.frames = []string{`{"type":"disconnect","reason":"link_disabled"}`}

	err := ch.Listen(t.Context(), func(channel.Message) error { return nil }, func() {})
	if !errors.Is(err, errLinkDisabled) {
		t.Errorf("Listen() = %v, want errLinkDisabled", err)
	}
}

func TestListen_badTokenEnds(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.answer["auth.test"] = `{"ok":false,"error":"invalid_auth"}`

	if err := ch.Listen(t.Context(), func(channel.Message) error { return nil }, func() {}); !fatal(err) {
		t.Errorf("Listen() = %v, want invalid_auth", err)
	}
}

func TestReply_channelGoesToThreadWithMention(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	msg := channel.Message{ID: "slack:C1/1.2", ChatType: channel.ChatGroup, Sender: channel.User{ID: "U1"}}

	if err := ch.Reply(t.Context(), msg, "**done** <!channel>"); err != nil {
		t.Fatal(err)
	}

	posts := f.called("chat.postMessage")
	if len(posts) != 1 {
		t.Fatalf("chat.postMessage calls = %d, want 1", len(posts))
	}

	p := posts[0].Params
	if p.Get("channel") != "C1" || p.Get("thread_ts") != "1.2" || posts[0].Token != botToken {
		t.Errorf("chat.postMessage params = %v", p)
	}

	var blocks []map[string]any
	if err := json.Unmarshal([]byte(p.Get("blocks")), &blocks); err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 2 || blocks[0]["type"] != "section" || blocks[1]["type"] != "markdown" {
		t.Fatalf("blocks = %v", blocks)
	}

	if mention, _ := blocks[0]["text"].(map[string]any); mention["text"] != "<@U1>" {
		t.Errorf("mention block = %v", blocks[0])
	}

	if body := blocks[1]["text"]; body != "**done** <\u200b!channel>" {
		t.Errorf("markdown block = %q: broadcasts must be defused", body)
	}
}

func TestReply_directMessageStaysInConversation(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	msg := channel.Message{ID: "slack:D1/1.2", ChatType: channel.ChatDirect, Sender: channel.User{ID: "U1"}}

	if err := ch.Reply(t.Context(), msg, "ok"); err != nil {
		t.Fatal(err)
	}

	p := f.called("chat.postMessage")[0].Params
	if p.Has("thread_ts") || strings.Contains(p.Get("blocks"), "section") {
		t.Errorf("direct reply threaded or mentioned: %v", p)
	}
}

func TestSend_splitsLongText(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)

	if err := ch.Send(t.Context(), "C1", strings.Repeat("line of text\n", 2000)); err != nil {
		t.Fatal(err)
	}

	if n := len(f.called("chat.postMessage")); n != 3 {
		t.Errorf("chat.postMessage calls = %d, want 3", n)
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.answer["conversations.replies"] = `{"ok":true,"messages":[
		{"type":"message","user":"U9","text":"thread start","ts":"1.0"},
		{"type":"message","user":"U1","text":"<@UBOT> the reply","ts":"1.5","thread_ts":"1.0"}]}`

	got, err := ch.Get(t.Context(), "C1/1.5")
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != "slack:C1/1.5" || got.Text != "the reply" || got.ReplyTo != "slack:C1/1.0" || got.ChatType != channel.ChatGroup {
		t.Errorf("Get() = %+v", got)
	}

	if _, err := ch.Get(t.Context(), "C1/2.0"); err == nil {
		t.Error("Get() of a missing message succeeded")
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)
	dir := t.TempDir()

	got, err := ch.Download(t.Context(), channel.Message{}, channel.Attachment{Key: "F1"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, "shot.png"); got != want {
		t.Errorf("Download() = %s, want %s", got, want)
	}

	data, err := os.ReadFile(got)
	if err != nil || string(data) != "png bytes" {
		t.Errorf("downloaded %q, %v", data, err)
	}
}

func TestAck_alreadyReactedIsFine(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.answer["reactions.add"] = `{"ok":false,"error":"already_reacted"}`

	if err := ch.Ack(t.Context(), channel.Message{ID: "slack:C1/1.2"}); err != nil {
		t.Errorf("Ack() = %v", err)
	}

	if p := f.called("reactions.add")[0].Params; p.Get("timestamp") != "1.2" || p.Get("name") != ackEmoji {
		t.Errorf("reactions.add params = %v", p)
	}
}
