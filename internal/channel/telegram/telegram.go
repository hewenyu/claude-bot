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
	"sync"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/markdown"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "telegram"

const (
	// pollSeconds stays below the transport's 30 s response header timeout.
	pollSeconds   = 25
	backoffStart  = 2 * time.Second // first wait after a failed poll
	backoffCap    = time.Minute
	maxMessageLen = 4096 // Telegram's limit per message, in characters
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

// ErrNoGet is returned by Get: the Bot API cannot fetch a message by id.
var ErrNoGet = errors.New("telegram: the Bot API cannot fetch messages by id; quoted messages are included in the text")

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

	botMu sync.Mutex
	bot   *Bot // cached once fetched successfully
}

var _ channel.Channel = (*Channel)(nil)

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
	c.botMu.Lock()
	defer c.botMu.Unlock()

	if c.bot != nil {
		return *c.bot, nil
	}

	var u user
	if err := c.call(ctx, methodGetMe, struct{}{}, &u); err != nil {
		return Bot{}, err
	}

	c.bot = &Bot{ID: u.ID, Name: u.name(), Username: u.Username, ReadsAllGroupMessages: u.CanReadAllGroupMessages}

	return *c.bot, nil
}

type getUpdates struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// Listen implements channel.Channel. It long-polls for new messages and
// delivers direct messages, and group messages that @-mention the bot or
// reply to it, from people only. Network errors are retried with backoff;
// only a rejected token (or a webhook in the way) ends it.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	bot, err := c.Bot(ctx)
	if err != nil {
		return err
	}

	req := getUpdates{Timeout: pollSeconds, AllowedUpdates: []string{updateMessage}}
	backoff := transport.Backoff{Start: backoffStart, Cap: backoffCap}
	connected := false

	for {
		var updates []update

		err := c.call(ctx, methodGetUpdates, req, &updates)

		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // cancelled: the poll error is just the shutdown
		case fatal(err):
			return err
		case err != nil:
			connected = false
			wait := backoff.Next()
			c.log.Warn("telegram poll", "err", err, "retry_in", wait)

			if !transport.Sleep(ctx, wait) {
				return nil
			}

			continue
		}

		if !connected {
			c.log.Info("telegram connected", "bot", bot.Username)
			ready()
		}

		connected = true

		backoff.Reset()

		for _, u := range updates {
			req.Offset = u.UpdateID + 1 // confirms it on the next poll

			msg, ok := fromMessage(u.Message, bot)
			if !ok {
				continue
			}

			if err := deliver(msg); err != nil {
				c.log.Error("telegram deliver", "id", msg.ID, "err", err)
			}
		}
	}
}

// Reply implements channel.Channel. The answer replies to the original
// message, in the same topic; in groups it starts by @-mentioning the sender.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	chatID, messageID, err := locate(msg.ID)
	if err != nil {
		return err
	}

	if msg.ChatType == channel.ChatGroup && msg.Sender.Mention != "" {
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

	for i, part := range markdown.Split(text, maxMessageLen) {
		send := sendMessage{ChatID: chatID, MessageThreadID: thread, Text: toHTML(part), ParseMode: parseHTML}
		send.LinkPreviewOptions.IsDisabled = true

		if i == 0 && replyTo != 0 {
			send.ReplyParameters = &replyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
		}

		err := c.call(ctx, methodSendMessage, send, nil)

		if ae, ok := errors.AsType[*APIError](err); ok && ae.Code == codeBadRequest && strings.Contains(ae.Description, "parse") {
			send.Text, send.ParseMode = part, ""
			err = c.call(ctx, methodSendMessage, send, nil)
		}

		if err != nil {
			return fmt.Errorf("telegram send (part %d): %w", i+1, err)
		}
	}

	return nil
}

// Get implements channel.Channel. The Bot API has no way to fetch a message,
// so it always fails with ErrNoGet; messages carry what they quote instead.
func (c *Channel) Get(context.Context, string) (channel.Message, error) {
	return channel.Message{}, ErrNoGet
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

	// The file URL carries the token, so errors must not.
	data, err := transport.Download(ctx, c.http, c.api+"/file/bot"+c.token+"/"+f.FilePath, nil)
	if err != nil {
		return "", fmt.Errorf("telegram download: %w", redact(err))
	}

	name := channel.BaseName(att.Name)
	if name == "" {
		name = channel.BaseName(path.Base(f.FilePath)) // photos have no name of their own
	}

	if name == "" {
		name = channel.PathSafe(att.Key)
	}

	path, err := channel.SaveFile(dir, name, data)
	if err != nil {
		return "", fmt.Errorf("telegram download: %w", err)
	}

	return path, nil
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
