package discord

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
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

	c, err := New(Config{Token: "t", Proxy: transport.ProxyDirect}, slog.New(slog.DiscardHandler))
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

func TestFileName(t *testing.T) {
	t.Parallel()

	if got := fileName(&discordgo.MessageAttachment{ID: "a1", Filename: "../x/crash.log"}); got != "crash.log" {
		t.Errorf("fileName() = %q, want crash.log", got)
	}

	if got := fileName(&discordgo.MessageAttachment{ID: "a1", Filename: ".."}); got != "a1" {
		t.Errorf("fileName() = %q, want the id", got)
	}
}

func TestBot(t *testing.T) {
	t.Parallel()

	c, rec := newRecorded(t)

	bot, err := c.Bot(t.Context())
	if err != nil || bot.ID != "1" || bot.AppID != "1" {
		t.Fatalf("Bot() = %+v, %v", bot, err)
	}

	want := []string{"/api/v9/users/@me", "/api/v9/oauth2/applications/@me"}
	for i, r := range rec.reqs {
		if i >= len(want) || r.Method != http.MethodGet || r.Path != want[i] {
			t.Errorf("request %d = %s %s, want GET %s", i, r.Method, r.Path, want)
		}
	}
}

// stall answers the user and never the application, until the request ends.
type stall struct{ recorder }

func (s *stall) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/applications/") {
		<-req.Context().Done()

		return nil, req.Context().Err()
	}

	return s.recorder.RoundTrip(req)
}

func TestBot_applicationEndsWithContext(t *testing.T) {
	t.Parallel()

	c, _ := newRecorded(t)
	c.s.Client = &http.Client{Transport: &stall{}}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	if _, err := c.Bot(ctx); err == nil {
		t.Fatal("Bot() succeeded without the application")
	}
}
