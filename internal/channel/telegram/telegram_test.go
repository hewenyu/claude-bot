package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

	"github.com/hewenyu/claude-bot/internal/channel"
)

const token = "123:secret"

var (
	bot   = Bot{ID: 100, Name: "Proj Bot", Username: "proj_bot"}
	alice = &user{ID: 200, FirstName: "Alice", Username: "alice"}
	nobu  = &user{ID: 300, FirstName: "Nobu", LastName: "Sato"} // no username
	sent  = time.Date(2026, 10, 28, 3, 46, 15, 0, time.UTC)
)

func group(text string, entities ...entity) *message {
	return &message{
		MessageID: 9001, From: alice, Chat: chat{ID: -1001, Type: chatSuper},
		Date: sent.Unix(), Text: text, Entities: entities,
	}
}

func TestFromMessage_groupMentioningBot(t *testing.T) {
	t.Parallel()

	// "🐛" is two UTF-16 code units: entity offsets count those, not runes.
	m := group("🐛 @proj_bot 登录页报 500，@bob 和 Nobu 也遇到了",
		entity{Type: entityMention, Offset: 3, Length: 9},
		entity{Type: entityMention, Offset: 22, Length: 4},
		entity{Type: entityTextRef, Offset: 29, Length: 4, User: nobu},
	)
	m.IsTopicMessage, m.MessageThreadID = true, 77
	m.ReplyToMessage = &message{MessageID: 77, From: alice, Text: "topic start"} // not a real reply
	m.Photo = []photoSize{{FileID: "small"}, {FileID: "large"}}
	m.Caption = "ignored: the message has text"

	got, ok := fromMessage(m, bot)
	if !ok {
		t.Fatal("group message that @s the bot was dropped")
	}

	want := channel.Message{
		ID:       "telegram:-1001/9001",
		Channel:  "telegram",
		ChatID:   "-1001",
		ChatType: channel.ChatGroup,
		ThreadID: "77",
		Sender:   channel.User{ID: "200", Name: "Alice", Mention: "@alice"},
		Text:     "🐛  登录页报 500，@bob 和 Nobu 也遇到了",
		Mentions: []channel.User{
			{Name: "bob", Mention: "@bob"},
			{ID: "300", Name: "Nobu Sato", Mention: "[Nobu Sato](tg://user?id=300)"},
		},
		Attachments: []channel.Attachment{{Kind: channel.AttachmentImage, Key: "large"}},
		CreatedAt:   sent,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromMessage()\n got  %+v\n want %+v", got, want)
	}
}

func TestFromMessage_replyToBotQuotesIt(t *testing.T) {
	t.Parallel()

	m := group("还是不行")
	m.ReplyToMessage = &message{MessageID: 9000, From: &user{ID: bot.ID, IsBot: true, FirstName: "Proj Bot"}, Text: "已修复\n请重试"}

	got, ok := fromMessage(m, bot)
	if !ok {
		t.Fatal("reply to the bot was dropped")
	}

	if want := "> Proj Bot: 已修复\n> 请重试\n\n还是不行"; got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}

	if got.ReplyTo != "" {
		t.Errorf("ReplyTo = %q: Get cannot fetch it, so it must stay empty", got.ReplyTo)
	}
}

func TestFromMessage_filters(t *testing.T) {
	t.Parallel()

	mention := entity{Type: entityMention, Offset: 0, Length: 9}

	tests := []struct {
		name string
		msg  *message
		want bool
	}{
		{name: "direct message is delivered", msg: &message{From: alice, Chat: chat{ID: 200, Type: chatPrivate}, Text: "hi"}, want: true},
		{name: "group chatter is ignored", msg: group("hi all")},
		{name: "mentioning someone else is not mentioning the bot", msg: group("@alice_x hi", mention)},
		{name: "a mention in any case counts", msg: group("@PROJ_BOT hi", mention), want: true},
		{name: "a command for the bot counts", msg: group("/fix@proj_bot now", entity{Type: entityCommand, Length: 13}), want: true},
		{name: "a command for another bot does not", msg: group("/fix@other_bo now", entity{Type: entityCommand, Length: 13})},
		{name: "messages from bots are ignored", msg: &message{From: &user{ID: 5, IsBot: true}, Chat: chat{Type: chatPrivate}}},
		{name: "a message without sender is ignored", msg: &message{Chat: chat{Type: chatPrivate}}},
		{name: "channel posts are ignored", msg: &message{From: alice, Chat: chat{Type: "channel"}}},
		{name: "an entity out of range is ignored", msg: group("@proj_bot", entity{Type: entityMention, Offset: 5, Length: 9})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := fromMessage(tt.msg, bot); ok != tt.want {
				t.Errorf("fromMessage() delivered = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestPlainText_commandKeepsItsName(t *testing.T) {
	t.Parallel()

	got, _ := plainText("/fix@proj_bot the login", []entity{{Type: entityCommand, Length: 13}}, bot)
	if got != "/fix the login" {
		t.Errorf("plainText() = %q", got)
	}
}

func TestToHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, md, want string
	}{
		{name: "escapes HTML", md: "a < b && c > d", want: "a &lt; b &amp;&amp; c &gt; d"},
		{name: "bold, strike and links", md: "**修好了** ~~旧~~ [commit](https://x.io/c?a=1&b=2)", want: `<b>修好了</b> <s>旧</s> <a href="https://x.io/c?a=1&amp;b=2">commit</a>`},
		{name: "inline code is verbatim", md: "run `a **b** <c>`", want: "run <code>a **b** &lt;c&gt;</code>"},
		{name: "unmatched backtick stays", md: "it`s **ok**", want: "it`s <b>ok</b>"},
		{name: "single stars and underscores stay", md: "snake_case_name * item", want: "snake_case_name * item"},
		{name: "heading becomes bold", md: "## 结论", want: "<b>结论</b>"},
		{name: "quotes are grouped", md: "> a\n> b\nc", want: "<blockquote>a\nb</blockquote>\nc"},
		{name: "code block with language", md: "```go\nx := a < b\n```", want: `<pre><code class="language-go">x := a &lt; b</code></pre>`},
		{name: "code block left open", md: "```\nx", want: "<pre>x</pre>"},
		{name: "text mention link", md: "[Nobu](tg://user?id=300) hi", want: `<a href="tg://user?id=300">Nobu</a> hi`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := toHTML(tt.md); got != tt.want {
				t.Errorf("toHTML(%q)\n got  %q\n want %q", tt.md, got, tt.want)
			}
		})
	}
}

func TestLocate(t *testing.T) {
	t.Parallel()

	if chatID, msg, err := locate("telegram:-1001/9001"); err != nil || chatID != "-1001" || msg != 9001 {
		t.Errorf("locate() = %q, %d, %v", chatID, msg, err)
	}

	for _, bad := range []string{"telegram:9001", "telegram:/9001", "telegram:-1/x", "nope"} {
		if _, _, err := locate(bad); !errors.Is(err, channel.ErrInvalidID) {
			t.Errorf("locate(%q) error = %v, want ErrInvalidID", bad, err)
		}
	}
}

func TestFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want bool
	}{
		{err: &APIError{Code: codeUnauthorized}, want: true},
		{err: &APIError{Code: codeNotFound}, want: true},
		{err: &APIError{Code: codeConflict, Description: "Conflict: can't use getUpdates method while webhook is active"}, want: true},
		{err: &APIError{Code: codeConflict, Description: "Conflict: terminated by other getUpdates request"}},
		{err: &APIError{Code: http.StatusBadGateway}},
		{err: errors.New("connection reset")},
	}

	for _, tt := range tests {
		if got := fatal(tt.err); got != tt.want {
			t.Errorf("fatal(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// fakeAPI is a Bot API server that records the calls it gets.
type fakeAPI struct {
	t *testing.T

	mu      sync.Mutex
	calls   []call
	updates []update // served once by getUpdates
	answer  map[string]func(params map[string]any) (int, string)
}

type call struct {
	Method string
	Params map[string]any
}

func newFake(t *testing.T) (*fakeAPI, *Channel) {
	t.Helper()

	f := &fakeAPI{t: t, answer: map[string]func(map[string]any) (int, string){}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	ch, err := New(Config{Token: token, Proxy: "direct"}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	ch.api = srv.URL

	return f, ch
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if path, ok := strings.CutPrefix(r.URL.Path, "/file/bot"+token+"/"); ok {
		_, _ = io.WriteString(w, "file:"+path)

		return
	}

	method, ok := strings.CutPrefix(r.URL.Path, "/bot"+token+"/")
	if !ok {
		http.Error(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, http.StatusUnauthorized)

		return
	}

	var params map[string]any

	_ = json.NewDecoder(r.Body).Decode(&params)

	f.mu.Lock()
	f.calls = append(f.calls, call{Method: method, Params: params})
	answer := f.answer[method]

	var updates []update
	if method == "getUpdates" {
		updates, f.updates = f.updates, nil
	}
	f.mu.Unlock()

	if answer != nil {
		code, body := answer(params)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)

		return
	}

	var result any = true

	switch method {
	case "getMe":
		result = user{ID: bot.ID, IsBot: true, FirstName: bot.Name, Username: bot.Username}
	case "getUpdates":
		if updates == nil {
			select { // a long poll with nothing new
			case <-r.Context().Done():
			case <-time.After(50 * time.Millisecond):
			}
		}

		result = updates
	case "getFile":
		result = fileInfo{FilePath: "photos/file_7.jpg"}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeAPI) called(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []map[string]any

	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c.Params)
		}
	}

	return out
}

func TestListen_deliversAndConfirms(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.updates = []update{
		{UpdateID: 10, Message: group("not for the bot")},
		{UpdateID: 11, Message: &message{MessageID: 5, From: alice, Chat: chat{ID: 200, Type: chatPrivate}, Text: "hi"}},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan channel.Message, 2)
	readies := 0
	done := make(chan error, 1)

	go func() {
		done <- ch.Listen(ctx, func(m channel.Message) error { got <- m; return nil }, func() { readies++ })
	}()

	select {
	case m := <-got:
		if m.ID != "telegram:200/5" || m.ChatType != channel.ChatDirect {
			t.Errorf("delivered %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing delivered")
	}

	// The next poll confirms both updates.
	deadline := time.Now().Add(5 * time.Second)
	for len(f.called("getUpdates")) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("Listen() = %v after cancel, want nil", err)
	}

	if off := f.called("getUpdates")[1]["offset"]; off != float64(12) {
		t.Errorf("second poll offset = %v, want 12", off)
	}

	if readies != 1 {
		t.Errorf("ready called %d times, want once", readies)
	}

	if len(got) != 0 {
		t.Errorf("group chatter was delivered: %+v", <-got)
	}
}

func TestListen_badTokenEnds(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.answer["getMe"] = func(map[string]any) (int, string) {
		return http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`
	}

	err := ch.Listen(t.Context(), func(channel.Message) error { return nil }, func() {})

	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != codeUnauthorized {
		t.Fatalf("Listen() = %v, want the 401", err)
	}

	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestReply_groupMentionsAndReplies(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	msg := channel.Message{
		ID: "telegram:-1001/9001", ChatType: channel.ChatGroup, ThreadID: "77",
		Sender: channel.User{ID: "300", Mention: "[Nobu](tg://user?id=300)"},
	}

	if err := ch.Reply(t.Context(), msg, "**done** a<b"); err != nil {
		t.Fatal(err)
	}

	sends := f.called("sendMessage")
	if len(sends) != 1 {
		t.Fatalf("sendMessage calls = %d, want 1", len(sends))
	}

	p := sends[0]
	if p["chat_id"] != "-1001" || p["message_thread_id"] != float64(77) || p["parse_mode"] != parseHTML {
		t.Errorf("sendMessage params = %v", p)
	}

	if want := `<a href="tg://user?id=300">Nobu</a> <b>done</b> a&lt;b`; p["text"] != want {
		t.Errorf("text = %q, want %q", p["text"], want)
	}

	if rp, _ := p["reply_parameters"].(map[string]any); rp["message_id"] != float64(9001) || rp["allow_sending_without_reply"] != true {
		t.Errorf("reply_parameters = %v", p["reply_parameters"])
	}
}

func TestSend_fallsBackToPlainText(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	f.answer["sendMessage"] = func(p map[string]any) (int, string) {
		if p["parse_mode"] == parseHTML {
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`
		}

		return http.StatusOK, `{"ok":true,"result":{}}`
	}

	if err := ch.Send(t.Context(), "200", "[x](javascript:alert)"); err != nil {
		t.Fatal(err)
	}

	sends := f.called("sendMessage")
	if len(sends) != 2 || sends[1]["text"] != "[x](javascript:alert)" || sends[1]["parse_mode"] != nil {
		t.Errorf("sendMessage calls = %v, want an HTML try then plain text", sends)
	}
}

func TestSend_splitsLongText(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)

	text := strings.Repeat("line of text\n", 700) // ~9100 characters
	if err := ch.Send(t.Context(), "200", text); err != nil {
		t.Fatal(err)
	}

	sends := f.called("sendMessage")
	if len(sends) != 3 {
		t.Fatalf("sendMessage calls = %d, want 3", len(sends))
	}

	for _, p := range sends {
		if p["reply_parameters"] != nil {
			t.Errorf("Send replied to something: %v", p["reply_parameters"])
		}
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)
	dir := t.TempDir()

	got, err := ch.Download(t.Context(), channel.Message{}, channel.Attachment{Kind: channel.AttachmentImage, Key: "large"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, "file_7.jpg"); got != want {
		t.Errorf("Download() = %s, want %s", got, want)
	}

	data, err := os.ReadFile(got)
	if err != nil || string(data) != "file:photos/file_7.jpg" {
		t.Errorf("downloaded %q, %v", data, err)
	}
}

func TestDownload_keepsOnlyTheBaseName(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)
	dir := t.TempDir()

	got, err := ch.Download(t.Context(), channel.Message{}, channel.Attachment{Key: "doc", Name: "../../evil.sh"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Dir(got) != dir {
		t.Errorf("Download() wrote %s, outside %s", got, dir)
	}
}

func TestAck(t *testing.T) {
	t.Parallel()

	f, ch := newFake(t)
	if err := ch.Ack(t.Context(), channel.Message{ID: "telegram:200/5"}); err != nil {
		t.Fatal(err)
	}

	r := f.called("setMessageReaction")
	if len(r) != 1 || r[0]["message_id"] != float64(5) {
		t.Errorf("setMessageReaction calls = %v", r)
	}
}

func TestGet_unsupported(t *testing.T) {
	t.Parallel()

	_, ch := newFake(t)
	if _, err := ch.Get(t.Context(), "200/5"); !errors.Is(err, ErrNoGet) {
		t.Errorf("Get() error = %v, want ErrNoGet", err)
	}
}
