// Package telegram implements the claude-bot channel protocol for Telegram,
// receiving messages by long polling the Bot API, so no public URL is needed.
//
// Telegram message ids are only unique within their chat, so the platform id
// of a message is "<chat id>/<message id>".
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/markdown"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "telegram"

const (
	// pollSeconds stays below the transport's 30 s response header timeout.
	pollSeconds = 25
	// maxMessageLen is Telegram's limit, in UTF-16 code units of the text
	// after entity parsing. Parts are measured as the Markdown they are cut
	// from: its HTML renders no longer, and the plain-text fallback is that
	// Markdown as is.
	maxMessageLen = 4096
	ackEmoji      = "👀"
	reactionEmoji = "emoji" // ReactionType of a plain emoji
	updateMessage = "message"
	parseHTML     = "HTML"
	botLink       = "https://t.me/"
)

// Bot API methods.
const (
	methodGetMe       = "getMe"
	methodGetUpdates  = "getUpdates"
	methodSendMessage = "sendMessage"
	methodGetFile     = "getFile"
	methodSetReaction = "setMessageReaction"
)

// Config holds the token of the project's bot, from @BotFather.
type Config struct {
	Token string `json:"token"`
	// Proxy is "direct", "env" (HTTPS_PROXY etc., the default) or a proxy URL.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	if c.Token == "" {
		return errors.New("telegram: token is required")
	}

	return nil
}

// ProxyFunc returns the proxy selector for API traffic.
func (c Config) ProxyFunc() (transport.ProxyFunc, error) {
	proxy, err := transport.ProxyOr(c.Proxy, transport.ProxyEnv)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}

	return proxy, nil
}

// Bot is the identity of the bot.
type Bot struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	// ReadsAllGroupMessages is false while privacy mode is on.
	ReadsAllGroupMessages bool `json:"reads_all_group_messages"`
}

// Link opens a direct chat with the bot.
func (b Bot) Link() string {
	return botLink + b.Username
}

// Channel is the Telegram adapter.
type Channel struct {
	http  *http.Client
	api   string // Bot API root, replaced in tests
	token string
	log   *slog.Logger

	bot channel.Memo[Bot]
}

var (
	_ channel.Channel    = (*Channel)(nil)
	_ channel.Identifier = (*Channel)(nil)
)

// New returns a Telegram channel for cfg.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := cfg.ProxyFunc()
	if err != nil {
		return nil, err
	}

	return &Channel{
		http:  transport.Client(proxy),
		api:   defaultAPI,
		token: cfg.Token,
		log:   log,
	}, nil
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return Name }

// Bot returns the bot's identity. It doubles as a token check; only a
// successful answer is cached, so a transient failure can be retried.
func (c *Channel) Bot(ctx context.Context) (Bot, error) {
	return c.bot.Get(ctx, func(ctx context.Context) (Bot, error) {
		var u user
		if err := c.call(ctx, methodGetMe, struct{}{}, &u); err != nil {
			return Bot{}, err
		}

		return Bot{ID: u.ID, Name: u.name(), Username: u.Username, ReadsAllGroupMessages: u.CanReadAllGroupMessages}, nil
	})
}

// Identity implements channel.Identifier.
func (c *Channel) Identity(ctx context.Context) (string, error) {
	bot, err := c.Bot(ctx)
	if err != nil {
		return "", err
	}

	return "Telegram @" + bot.Username, nil
}

type getUpdates struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// Listen implements channel.Channel. It long-polls for new messages and
// delivers direct messages, and group messages that @-mention the bot or
// reply to it, from people only. Network errors, and updates that could not
// be delivered, are retried with backoff; only a rejected token (or a
// webhook in the way) ends it.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	backoff := transport.ReconnectBackoff()

	bot, err := transport.Retry(ctx, backoff, c.log, "telegram getMe", fatal, c.Bot)
	if err != nil || ctx.Err() != nil {
		return err
	}

	req := getUpdates{Timeout: pollSeconds, AllowedUpdates: []string{updateMessage}}

	return transport.Poll(ctx, c.log, Name, 0, backoff, fatal, func(ctx context.Context) error {
		var updates []update
		if err := c.call(ctx, methodGetUpdates, req, &updates); err != nil {
			return err
		}

		return deliverAll(updates, bot, &req.Offset, deliver)
	}, func() {
		c.log.Info("telegram connected", "bot", bot.Username)
		ready()
	})
}

// deliverAll delivers the messages of updates addressed to the bot, moving
// offset past each update handled: the next poll confirms them. It stops at
// the first update that could not be delivered, so the next poll gets it
// again.
func deliverAll(updates []update, bot Bot, offset *int64, deliver func(channel.Message) error) error {
	for _, u := range updates {
		if msg, ok := fromMessage(u.Message, bot); ok {
			if err := deliver(msg); err != nil {
				return fmt.Errorf("telegram deliver %s: %w", msg.ID, err)
			}
		}

		*offset = u.UpdateID + 1
	}

	return nil
}

// Reply implements channel.Channel. The answer replies to the original
// message, in the same topic; in groups it starts by @-mentioning the sender.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	chatID, messageID, err := locate(msg.ID)
	if err != nil {
		return err
	}

	if channel.MentionsSender(msg) && msg.Sender.Mention != "" {
		text = msg.Sender.Mention + " " + text
	}

	return c.post(ctx, chatID, msg.ThreadID, messageID, text)
}

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	return c.post(ctx, chatID, "", 0, text)
}

type replyParameters struct {
	MessageID                int64 `json:"message_id"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply"`
}

type sendMessage struct {
	ChatID             string           `json:"chat_id"`
	MessageThreadID    int64            `json:"message_thread_id,omitempty"`
	Text               string           `json:"text"`
	ParseMode          string           `json:"parse_mode,omitempty"`
	ReplyParameters    *replyParameters `json:"reply_parameters,omitempty"`
	LinkPreviewOptions struct {
		IsDisabled bool `json:"is_disabled"`
	} `json:"link_preview_options"`
}

// post sends Markdown as HTML, split into as many messages as Telegram's
// length limit needs; only the first one replies to replyTo (if not 0).
// A part Telegram cannot parse as HTML is sent again as plain text.
func (c *Channel) post(ctx context.Context, chatID, threadID string, replyTo int64, text string) error {
	var thread int64

	if threadID != "" {
		var err error
		if thread, err = strconv.ParseInt(threadID, 10, 64); err != nil {
			return fmt.Errorf("telegram: invalid thread id %q", threadID)
		}
	}

	parts := markdown.SplitFunc(text, maxMessageLen, utf16Len)

	err := markdown.SendParts(parts, func(i int, part string) error {
		send := sendMessage{ChatID: chatID, MessageThreadID: thread, Text: toHTML(part), ParseMode: parseHTML}
		send.LinkPreviewOptions.IsDisabled = true

		if i == 0 && replyTo != 0 {
			send.ReplyParameters = &replyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
		}

		err := c.call(ctx, methodSendMessage, send, nil)

		if ae, ok := errors.AsType[*apiError](err); ok && ae.Code == codeBadRequest && strings.Contains(ae.Description, "parse") {
			send.Text, send.ParseMode = part, ""
			err = c.call(ctx, methodSendMessage, send, nil)
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}

	return nil
}

// utf16Len is the length of s in UTF-16 code units, as Telegram counts.
func utf16Len(s string) int {
	n := 0

	for _, r := range s {
		n += utf16.RuneLen(r)
	}

	return n
}

// Get implements channel.Channel. The Bot API has no way to fetch a message,
// so it always fails with channel.ErrNoGet; messages carry what they quote
// instead.
func (c *Channel) Get(context.Context, string) (channel.Message, error) {
	return channel.Message{}, fmt.Errorf("telegram: the Bot API %w; quoted messages are included in the text", channel.ErrNoGet)
}

type fileInfo struct {
	FilePath string `json:"file_path"`
}

// Download implements channel.Channel.
func (c *Channel) Download(ctx context.Context, _ channel.Message, att channel.Attachment, dir string) (string, error) {
	var f fileInfo
	if err := c.call(ctx, methodGetFile, map[string]string{"file_id": att.Key}, &f); err != nil {
		return "", fmt.Errorf("telegram download: %w", err)
	}

	if f.FilePath == "" {
		return "", errors.New("telegram download: no file path (larger than the Bot API's 20 MB?)")
	}

	// The file URL carries the token, so errors must not. Photos have no
	// name of their own; the file path has one.
	saved, err := channel.SaveURL(ctx, c.http, c.api+"/file/bot"+c.token+"/"+f.FilePath, nil,
		dir, att.Key, att.Name, path.Base(f.FilePath))
	if err != nil {
		return "", fmt.Errorf("telegram download: %w", redact(err))
	}

	return saved, nil
}

type reaction struct {
	Type  string `json:"type"`
	Emoji string `json:"emoji"`
}

type setReaction struct {
	ChatID    string     `json:"chat_id"`
	MessageID int64      `json:"message_id"`
	Reaction  []reaction `json:"reaction"`
}

// Ack implements channel.Channel with an emoji reaction.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	chatID, messageID, err := locate(msg.ID)
	if err != nil {
		return err
	}

	r := setReaction{ChatID: chatID, MessageID: messageID, Reaction: []reaction{{Type: reactionEmoji, Emoji: ackEmoji}}}
	if err := c.call(ctx, methodSetReaction, r, nil); err != nil {
		return fmt.Errorf("telegram ack: %w", err)
	}

	return nil
}
