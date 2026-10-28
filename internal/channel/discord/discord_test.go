package discord

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const botID = "100"

var (
	alice = &discordgo.User{ID: "200", Username: "alice", GlobalName: "Alice"}
	bob   = &discordgo.User{ID: "300", Username: "bob"}
	sent  = time.Date(2026, 10, 28, 3, 46, 15, 0, time.UTC)
)

func TestFromEvent_serverMentioningBot(t *testing.T) {
	t.Parallel()

	m := &discordgo.Message{
		ID:               "9001",
		ChannelID:        "42",
		GuildID:          "7",
		Type:             discordgo.MessageTypeReply,
		Author:           alice,
		Content:          "<@100> 登录页报 500 了，<@!300> 也遇到了 <@555>",
		Mentions:         []*discordgo.User{{ID: botID, Username: "bot", Bot: true}, bob},
		MessageReference: &discordgo.MessageReference{MessageID: "8000", ChannelID: "42"},
		Attachments: []*discordgo.MessageAttachment{
			{ID: "a1", Filename: "shot.png", ContentType: "image/png"},
			{ID: "a2", Filename: "crash.log", ContentType: "text/plain"},
		},
		Timestamp: sent,
	}

	got, ok := fromEvent(m, botID, true)
	if !ok {
		t.Fatal("server message that @s the bot was dropped")
	}

	want := channel.Message{
		ID:       "discord:42/9001",
		Channel:  "discord",
		ChatID:   "42",
		ChatType: channel.ChatGroup,
		ThreadID: "42",
		ReplyTo:  "discord:42/8000",
		Sender:   channel.User{ID: "200", Name: "Alice", Mention: "<@200>"},
		Text:     "登录页报 500 了，@bob 也遇到了 <@555>",
		Mentions: []channel.User{{ID: "300", Name: "bob", Mention: "<@300>"}},
		Attachments: []channel.Attachment{
			{Kind: channel.AttachmentImage, Key: "a1", Name: "shot.png"},
			{Kind: channel.AttachmentFile, Key: "a2", Name: "crash.log"},
		},
		CreatedAt: sent,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromEvent()\n got  %+v\n want %+v", got, want)
	}
}

func TestFromEvent_filters(t *testing.T) {
	t.Parallel()

	toBot := []*discordgo.User{{ID: botID}}

	tests := []struct {
		name string
		msg  *discordgo.Message
		want bool
	}{
		{name: "direct message is delivered", msg: &discordgo.Message{Author: alice, Content: "hi"}, want: true},
		{name: "server chatter without mention is ignored", msg: &discordgo.Message{Author: alice, GuildID: "7", Content: "hi"}},
		{name: "mentioning someone else is not mentioning the bot", msg: &discordgo.Message{Author: alice, GuildID: "7", Mentions: []*discordgo.User{bob}}},
		{name: "messages from bots are ignored", msg: &discordgo.Message{Author: &discordgo.User{ID: "1", Bot: true}, Mentions: toBot}},
		{name: "webhook messages are ignored", msg: &discordgo.Message{Author: alice, WebhookID: "5", Mentions: toBot}},
		{name: "system messages are ignored", msg: &discordgo.Message{Author: alice, Type: discordgo.MessageTypeChannelPinnedMessage, Mentions: toBot}},
		{name: "a message without author is ignored", msg: &discordgo.Message{Mentions: toBot}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := fromEvent(tt.msg, botID, false)
			if ok != tt.want {
				t.Fatalf("fromEvent() delivered = %v, want %v", ok, tt.want)
			}

			if ok && got.ChatType != channel.ChatDirect {
				t.Errorf("chat type = %q, want %q", got.ChatType, channel.ChatDirect)
			}
		})
	}
}

func TestSplitPlatformID(t *testing.T) {
	t.Parallel()

	if ch, msg, err := splitPlatformID("42/9001"); err != nil || ch != "42" || msg != "9001" {
		t.Errorf("splitPlatformID(42/9001) = %q, %q, %v", ch, msg, err)
	}

	for _, bad := range []string{"9001", "/9001", "42/"} {
		if _, _, err := splitPlatformID(bad); !errors.Is(err, channel.ErrInvalidID) {
			t.Errorf("splitPlatformID(%q) error = %v, want ErrInvalidID", bad, err)
		}
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{name: "short text is one message", text: "hello\nworld", limit: 20, want: []string{"hello\nworld"}},
		{name: "cuts at line breaks", text: "aaaa\nbbbb\ncccc", limit: 14, want: []string{"aaaa\nbbbb", "cccc"}},
		{name: "cuts a long line", text: "abcdefghij", limit: 8, want: []string{"abcd", "efgh", "ij"}},
		{name: "counts characters, not bytes", text: "你好世界你好", limit: 7, want: []string{"你好世", "界你好"}},
		{
			name:  "a cut code block is closed and reopened",
			text:  "intro\n```go\nline1\nline2\n```\nend",
			limit: 23,
			want:  []string{"intro\n```go\nline1\n```", "```go\nline2\n```\nend"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := split(tt.text, tt.limit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("split(%q, %d)\n got  %q\n want %q", tt.text, tt.limit, got, tt.want)
			}

			for _, part := range got {
				if n := utf8.RuneCountInString(part); n > tt.limit {
					t.Errorf("part %q has %d characters, limit %d", part, n, tt.limit)
				}
			}
		})
	}
}

func TestSplit_longFenceLineTerminates(t *testing.T) {
	t.Parallel()

	text := "```" + strings.Repeat("x", 300) + "\n" + strings.Repeat("code\n", 100) + "```"
	for _, part := range split(text, 120) {
		if n := utf8.RuneCountInString(part); n > 120 {
			t.Fatalf("part has %d characters, limit 120", n)
		}
	}
}

func TestInviteURL(t *testing.T) {
	t.Parallel()

	got := Bot{AppID: "123"}.InviteURL()
	want := "https://discord.com/oauth2/authorize?client_id=123&scope=bot&permissions=274877975616"

	if got != want {
		t.Errorf("InviteURL() = %s, want %s", got, want)
	}
}

func TestConfig_ProxyFunc(t *testing.T) {
	t.Parallel()

	if p, err := (Config{}).ProxyFunc(); err != nil || p == nil {
		t.Errorf("default proxy set = %v, %v; want the environment's", p != nil, err)
	}

	if p, err := (Config{Proxy: ProxyDirect}).ProxyFunc(); err != nil || p != nil {
		t.Errorf("direct proxy set = %v, %v; want none", p != nil, err)
	}

	if _, err := (Config{Proxy: "not a url"}).ProxyFunc(); err == nil {
		t.Error("invalid proxy accepted")
	}
}

// recorder captures the REST calls a channel makes.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	Method, Path string
	Body         map[string]any
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := recorded{Method: req.Method, Path: req.URL.Path}

	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}

		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.Body); err != nil {
				return nil, err
			}
		}
	}

	r.mu.Lock()
	r.reqs = append(r.reqs, rec)
	r.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"1"}`)),
		Request:    req,
	}, nil
}

func newRecorded(t *testing.T) (*Channel, *recorder) {
	t.Helper()

	c, err := New(Config{Token: "t", Proxy: ProxyDirect}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	c.s.Client = &http.Client{Transport: rec}

	return c, rec
}

func TestReply_server(t *testing.T) {
	t.Parallel()

	c, rec := newRecorded(t)
	msg := channel.Message{ID: "discord:42/9001", ChatType: channel.ChatGroup, Sender: channel.User{ID: "200"}}

	if err := c.Reply(t.Context(), msg, "fixed in `abc123`\n"+strings.Repeat("x", 2500)); err != nil {
		t.Fatal(err)
	}

	// The short first line goes alone; the long line is cut in two.
	if len(rec.reqs) != 3 {
		t.Fatalf("sent %d messages, want 3", len(rec.reqs))
	}

	first := rec.reqs[0]
	if first.Method != http.MethodPost || first.Path != "/api/v9/channels/42/messages" {
		t.Errorf("first request = %s %s", first.Method, first.Path)
	}

	if content, _ := first.Body["content"].(string); !strings.HasPrefix(content, "<@200> fixed in `abc123`") {
		t.Errorf("reply does not start by mentioning the sender: %.40q", content)
	}

	ref, _ := first.Body["message_reference"].(map[string]any)
	if ref["message_id"] != "9001" || ref["fail_if_not_exists"] != false {
		t.Errorf("message_reference = %v, want the original message", ref)
	}

	for _, r := range rec.reqs[1:] {
		if _, ok := r.Body["message_reference"]; ok {
			t.Error("continuation message references the original too")
		}
	}

	for i, r := range rec.reqs {
		allowed, _ := r.Body["allowed_mentions"].(map[string]any)
		if !reflect.DeepEqual(allowed["parse"], []any{"users"}) {
			t.Errorf("message %d allowed_mentions = %v, want users only (no @everyone, no roles)", i+1, allowed)
		}
	}
}

func TestReply_directHasNoMention(t *testing.T) {
	t.Parallel()

	c, rec := newRecorded(t)
	msg := channel.Message{ID: "discord:42/9001", ChatType: channel.ChatDirect, Sender: channel.User{ID: "200"}}

	if err := c.Reply(t.Context(), msg, "done"); err != nil {
		t.Fatal(err)
	}

	if len(rec.reqs) != 1 || rec.reqs[0].Body["content"] != "done" {
		t.Errorf("requests = %+v, want one message \"done\"", rec.reqs)
	}
}
