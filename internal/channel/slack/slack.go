// Package slack implements the claude-bot channel protocol for Slack,
// receiving events over Socket Mode (WebSocket) so no public URL is needed.
//
// Slack messages are addressed by channel and timestamp, so the platform id
// of a message is "<channel id>/<ts>".
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/markdown"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "slack"

const (
	appTokenPrefix = "xapp-"
	botTokenPrefix = "xoxb-"
	maxMarkdownLen = 12000 // Slack's limit for markdown blocks per message
	maxFallbackLen = 4000  // notification text Slack recommends at most
	ackEmoji       = "eyes"
)

// Web API methods.
const (
	methodAuthTest    = "auth.test"
	methodOpenSocket  = "apps.connections.open"
	methodPostMessage = "chat.postMessage"
	methodReplies     = "conversations.replies"
	methodFileInfo    = "files.info"
	methodAddReaction = "reactions.add"
	methodUserInfo    = "users.info"
)

// Block Kit types used in replies.
const (
	blockSection  = "section"
	blockMarkdown = "markdown"
	textMrkdwn    = "mrkdwn"
)

// Config holds the two tokens of the project's Slack app: the app-level
// token (xapp-…, scope connections:write) opens the Socket Mode connection,
// the bot token (xoxb-…) calls the Web API.
type Config struct {
	AppToken string `json:"app_token"`
	BotToken string `json:"bot_token"`
	// Proxy is "direct", "env" (HTTPS_PROXY etc., the default) or a proxy URL.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	switch {
	case !strings.HasPrefix(c.AppToken, appTokenPrefix):
		return fmt.Errorf("slack: app token must start with %s (Basic Information → App-Level Tokens)", appTokenPrefix)
	case !strings.HasPrefix(c.BotToken, botTokenPrefix):
		return fmt.Errorf("slack: bot token must start with %s (OAuth & Permissions → Bot User OAuth Token)", botTokenPrefix)
	}

	return nil
}

// ProxyFunc returns the proxy selector used for both API and socket traffic.
func (c Config) ProxyFunc() (transport.ProxyFunc, error) {
	proxy, err := transport.ProxyOr(c.Proxy, transport.ProxyEnv)
	if err != nil {
		return nil, fmt.Errorf("slack: %w", err)
	}

	return proxy, nil
}

// Bot is the identity of the app's bot user.
type Bot struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	BotID  string `json:"bot_id"`
	Team   string `json:"team"`
	URL    string `json:"url"` // the workspace
}

// Channel is the Slack adapter.
type Channel struct {
	http   *http.Client
	dialer *websocket.Dialer
	api    string // Web API root, replaced in tests
	cfg    Config
	log    *slog.Logger

	botMu sync.Mutex
	bot   *Bot // cached once fetched successfully

	names channel.Names // display names of senders; events carry user ids only
}

var _ channel.Channel = (*Channel)(nil)

// New returns a Slack channel for cfg.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := cfg.ProxyFunc()
	if err != nil {
		return nil, err
	}

	c := &Channel{
		http:   transport.Client(proxy),
		dialer: transport.WebSocket(proxy),
		api:    defaultAPI,
		cfg:    cfg,
		log:    log,
	}
	c.names.Lookup = c.userName

	return c, nil
}

// nameSender fills in the sender's display name. Best effort: without the
// users:read scope the sender stays a user id.
func (c *Channel) nameSender(ctx context.Context, msg *channel.Message) {
	if msg.Sender.Name != "" || msg.Sender.ID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, channel.NameTimeout)
	defer cancel()

	name, err := c.names.Name(ctx, msg.Sender.ID)
	if err != nil {
		c.log.Warn("slack sender name (add the users:read scope to show names)", "user", msg.Sender.ID, "err", err)
	}

	msg.Sender.Name = name
}

// userName looks up a user's display name (users.info, scope users:read):
// the profile's display name, else the real name, else the handle.
func (c *Channel) userName(ctx context.Context, id string) (string, error) {
	var r struct {
		User struct {
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			Profile  struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := c.call(ctx, c.cfg.BotToken, methodUserInfo, url.Values{"user": {id}}, &r); err != nil {
		return "", err
	}

	for _, name := range []string{r.User.Profile.DisplayName, r.User.Profile.RealName, r.User.RealName, r.User.Name} {
		if name != "" {
			return name, nil
		}
	}

	return "", nil
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return Name }

// Bot returns the bot's identity. It doubles as a bot token check; only a
// successful answer is cached, so a transient failure can be retried.
func (c *Channel) Bot(ctx context.Context) (Bot, error) {
	c.botMu.Lock()
	defer c.botMu.Unlock()

	if c.bot != nil {
		return *c.bot, nil
	}

	var r struct {
		UserID string `json:"user_id"`
		User   string `json:"user"`
		BotID  string `json:"bot_id"`
		Team   string `json:"team"`
		URL    string `json:"url"`
	}
	if err := c.call(ctx, c.cfg.BotToken, methodAuthTest, nil, &r); err != nil {
		return Bot{}, err
	}

	if r.BotID == "" {
		return Bot{}, fmt.Errorf("slack: the bot token belongs to a user (%s), not a bot", r.User)
	}

	c.bot = &Bot{UserID: r.UserID, Name: r.User, BotID: r.BotID, Team: r.Team, URL: r.URL}

	return *c.bot, nil
}

// CheckAppToken proves the app-level token can open a Socket Mode connection.
func (c *Channel) CheckAppToken(ctx context.Context) error {
	_, err := c.socketURL(ctx)

	return err
}

// Reply implements channel.Channel. Outside direct messages the answer goes
// into the message's thread (starting one if needed) and @-mentions the
// sender; in a direct message it stays in the conversation or its thread.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	channelID, ts, err := channel.LocateScoped(msg.ID)
	if err != nil {
		return err
	}

	thread, mention := msg.ThreadID, ""

	if msg.ChatType != channel.ChatDirect {
		if thread == "" {
			thread = ts
		}

		if msg.Sender.ID != "" {
			mention = mentionSyntax(msg.Sender.ID)
		}
	}

	return c.post(ctx, channelID, thread, mention, text)
}

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	return c.post(ctx, chatID, "", "", text)
}

// post sends Markdown as markdown blocks, split into as many messages as the
// length limit needs; the first one starts with mention (if any). Mentions
// of people notify them; <!channel>, <!here> and <!everyone> never do: the
// text may quote what anyone wrote.
func (c *Channel) post(ctx context.Context, channelID, thread, mention, text string) error {
	text = defuseBroadcasts.Replace(text)

	for i, part := range markdown.Split(text, maxMarkdownLen) {
		var blocks []block

		fallback := part

		if i == 0 && mention != "" {
			blocks = append(blocks, block{Type: blockSection, Text: textObject{Type: textMrkdwn, Text: mention}})
			fallback = mention + " " + part
		}

		blocks = append(blocks, block{Type: blockMarkdown, Text: part})

		raw, err := json.Marshal(blocks)
		if err != nil {
			return fmt.Errorf("slack send: %w", err)
		}

		params := url.Values{
			"channel":      {channelID},
			"blocks":       {string(raw)},
			"text":         {truncate(fallback, maxFallbackLen)},
			"unfurl_links": {"false"},
			"unfurl_media": {"false"},
		}
		if thread != "" {
			params.Set("thread_ts", thread)
		}

		if err := c.call(ctx, c.cfg.BotToken, methodPostMessage, params, nil); err != nil {
			return fmt.Errorf("slack send (part %d): %w", i+1, err)
		}
	}

	return nil
}

// block is a Block Kit block; Text is a string for markdown blocks and a
// textObject for section blocks.
type block struct {
	Type string `json:"type"`
	Text any    `json:"text"`
}

type textObject struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// defuseBroadcasts breaks Slack's <!…> special mentions with a zero-width
// space, which leaves the text looking the same.
var defuseBroadcasts = strings.NewReplacer("<!", "<\u200b!")

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

// Get implements channel.Channel, for messages in channels and threads alike.
func (c *Channel) Get(ctx context.Context, platformID string) (channel.Message, error) {
	channelID, ts, err := channel.SplitScopedID(platformID)
	if err != nil {
		return channel.Message{}, err
	}

	// conversations.replies finds a message whether or not it is in a thread;
	// the window around ts keeps the rest of the thread out.
	params := url.Values{"channel": {channelID}, "ts": {ts}, "oldest": {ts}, "latest": {ts}, "inclusive": {"true"}}

	var r struct {
		Messages []event `json:"messages"`
	}
	if err := c.call(ctx, c.cfg.BotToken, methodReplies, params, &r); err != nil {
		return channel.Message{}, fmt.Errorf("slack get message: %w", err)
	}

	bot, _ := c.Bot(ctx) // best effort: only used to hide the bot's own mention

	for _, m := range r.Messages {
		if m.TS == ts {
			m.Channel = channelID

			msg := toMessage(m, bot.UserID)
			if m.BotID == "" {
				c.nameSender(ctx, &msg)
			}

			return msg, nil
		}
	}

	return channel.Message{}, fmt.Errorf("slack get message: %s not found", platformID)
}

// Download implements channel.Channel.
func (c *Channel) Download(ctx context.Context, _ channel.Message, att channel.Attachment, dir string) (string, error) {
	var r struct {
		File file `json:"file"`
	}
	if err := c.call(ctx, c.cfg.BotToken, methodFileInfo, url.Values{"file": {att.Key}}, &r); err != nil {
		return "", fmt.Errorf("slack download: %w", err)
	}

	if r.File.URLPrivateDownload == "" {
		return "", fmt.Errorf("slack download: file %s has no download link", att.Key)
	}

	auth := http.Header{"Authorization": {bearer + c.cfg.BotToken}}

	data, err := transport.Download(ctx, c.http, r.File.URLPrivateDownload, auth)
	if err != nil {
		return "", fmt.Errorf("slack download: %w", err)
	}

	name := channel.BaseName(r.File.Name)
	if name == "" {
		name = channel.PathSafe(att.Key)
	}

	path, err := channel.SaveFile(dir, name, data)
	if err != nil {
		return "", fmt.Errorf("slack download: %w", err)
	}

	return path, nil
}

// Ack implements channel.Channel with an emoji reaction.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	channelID, ts, err := channel.LocateScoped(msg.ID)
	if err != nil {
		return err
	}

	params := url.Values{"channel": {channelID}, "timestamp": {ts}, "name": {ackEmoji}}

	err = c.call(ctx, c.cfg.BotToken, methodAddReaction, params, nil)
	if ae, ok := errors.AsType[*APIError](err); ok && ae.Code == codeAlreadyReacted {
		return nil
	}

	if err != nil {
		return fmt.Errorf("slack ack: %w", err)
	}

	return nil
}
