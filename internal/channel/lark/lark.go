// Package lark implements the claude-bot channel protocol for Lark / Feishu,
// receiving events over the long-connection (WebSocket) API so no public
// callback URL is needed.
package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"sync"

	"github.com/gorilla/websocket"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "lark"

// Deployments.
const (
	DomainFeishu = "feishu"
	DomainLark   = "lark"
)

// Base URLs per deployment.
const (
	FeishuURL = "https://open.feishu.cn"
	LarkURL   = "https://open.larksuite.com"
)

// Open platform endpoints that have no typed SDK method.
const (
	botInfoPath = "/open-apis/bot/v3/info"
	scopesPath  = "/open-apis/application/v6/scopes"
)

// Field values in Lark events and API responses.
const (
	chatP2P       = "p2p"    // chat_type of a direct chat
	resourceImage = "image"  // message resource type of an image
	resourceFile  = "file"   // message resource type of any other file
	senderUser    = "user"   // sender_type of a person
	mentionedBot  = "bot"    // mentioned_type of an app
	scopeTenant   = "tenant" // scope_type of permissions granted to the app
	scopeGranted  = 1        // grant_status of an active permission
)

// ackEmojis are tried in order; see the emoji_type list in the Lark docs.
var ackEmojis = []string{"OnIt", "OK"}

// Config holds the app credentials of the project's bot.
type Config struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	Domain    string `json:"domain,omitempty"` // "feishu" (default) or "lark"
	// Proxy is "direct", "env" (HTTPS_PROXY etc.) or a proxy URL. Default:
	// direct for feishu (a domestic endpoint that local proxies often break),
	// env for lark.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	if c.AppID == "" || c.AppSecret == "" {
		return errors.New("lark: app_id and app_secret are required")
	}

	switch c.Domain {
	case "", DomainFeishu, DomainLark:
		return nil
	default:
		return fmt.Errorf("lark: unknown domain %q: want %s or %s", c.Domain, DomainFeishu, DomainLark)
	}
}

// BaseURL returns the open platform URL for the configured domain.
func (c Config) BaseURL() string {
	if c.Domain == DomainLark {
		return LarkURL
	}

	return FeishuURL
}

// ProxyFunc returns the proxy selector used for both API and WebSocket traffic.
func (c Config) ProxyFunc() (transport.ProxyFunc, error) {
	fallback := transport.ProxyDirect
	if c.Domain == DomainLark {
		fallback = transport.ProxyEnv
	}

	proxy, err := transport.ProxyOr(c.Proxy, fallback)
	if err != nil {
		return nil, fmt.Errorf("lark: %w", err)
	}

	return proxy, nil
}

// Bot is the identity of the app's bot.
type Bot struct {
	AppName string `json:"app_name"`
	OpenID  string `json:"open_id"`
}

// Channel is the Lark adapter.
type Channel struct {
	cfg    Config
	client *lark.Client
	http   *http.Client
	dialer *websocket.Dialer
	log    *slog.Logger

	botMu sync.Mutex
	bot   *Bot // cached once fetched successfully
}

var _ channel.Channel = (*Channel)(nil)

// New returns a Lark channel for cfg.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := cfg.ProxyFunc()
	if err != nil {
		return nil, err
	}

	hc := &http.Client{Transport: transport.HTTP(proxy)}

	return &Channel{
		cfg: cfg,
		client: lark.NewClient(cfg.AppID, cfg.AppSecret,
			lark.WithOpenBaseUrl(cfg.BaseURL()), lark.WithLogLevel(larkcore.LogLevelError), lark.WithHttpClient(hc)),
		http:   hc,
		dialer: transport.WebSocket(proxy),
		log:    log,
	}, nil
}

// apiError is a call the platform received and refused with a non-zero code,
// as opposed to a transport failure where the outcome is unknown.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("code %d: %s", e.Code, e.Msg) }

// checkCode turns the platform's verdict into an error, nil on success.
func checkCode(code int, msg string) error {
	if code == 0 {
		return nil
	}

	return &apiError{Code: code, Msg: msg}
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return Name }

// Bot returns the bot's identity. It doubles as a credentials check; only a
// successful answer is cached, so a transient failure can be retried.
func (c *Channel) Bot(ctx context.Context) (Bot, error) {
	c.botMu.Lock()
	defer c.botMu.Unlock()

	if c.bot != nil {
		return *c.bot, nil
	}

	resp, err := c.client.Get(ctx, botInfoPath, nil, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return Bot{}, fmt.Errorf("lark bot info: %w", err)
	}

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  Bot    `json:"bot"`
	}

	if err := json.Unmarshal(resp.RawBody, &body); err != nil {
		return Bot{}, fmt.Errorf("lark bot info: parse: %w", err)
	}

	if err := checkCode(body.Code, body.Msg); err != nil {
		return Bot{}, fmt.Errorf("lark bot info: %w", err)
	}

	c.bot = &body.Bot

	return body.Bot, nil
}

// Listen implements channel.Channel. It delivers direct messages and group
// messages that @-mention the bot, from users only (never from bots).
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	bot, err := c.Bot(ctx)
	if err != nil {
		return err
	}

	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, ev *larkim.P2MessageReceiveV1) error {
			msg, ok := fromEvent(ev, bot.OpenID)
			if !ok {
				return nil
			}

			return deliver(msg)
		}).
		// Reactions, the bot's own acks included, are not messages; handling
		// them keeps the SDK from logging each one as an error.
		OnP2MessageReactionCreatedV1(func(context.Context, *larkim.P2MessageReactionCreatedV1) error { return nil }).
		OnP2MessageReactionDeletedV1(func(context.Context, *larkim.P2MessageReactionDeletedV1) error { return nil })

	ws := larkws.NewClient(c.cfg.AppID, c.cfg.AppSecret,
		larkws.WithEventHandler(handler),
		larkws.WithDomain(c.cfg.BaseURL()),
		larkws.WithHttpClient(c.http),
		larkws.WithWebSocketDialer(c.dialer),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithOnReady(func() {
			c.log.Info("lark connected", "bot", bot.AppName)
			ready()
		}),
		larkws.WithOnReconnecting(func() { c.log.Warn("lark reconnecting") }),
	)

	if err := ws.Start(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("lark long connection: %w", err)
	}

	return nil
}

func fromEvent(ev *larkim.P2MessageReceiveV1, botOpenID string) (channel.Message, bool) {
	if ev == nil || ev.Event == nil || ev.Event.Message == nil || ev.Event.Sender == nil {
		return channel.Message{}, false
	}

	m, s := ev.Event.Message, ev.Event.Sender
	if larkcore.StringValue(s.SenderType) != senderUser || s.SenderId == nil {
		return channel.Message{}, false
	}

	mentions := eventMentions(m.Mentions, botOpenID)

	chatType := toChatType(larkcore.StringValue(m.ChatType))
	if chatType != channel.ChatDirect && !slices.ContainsFunc(mentions, func(x mention) bool { return x.IsBot }) {
		return channel.Message{}, false // group chatter not addressed to the bot
	}

	senderID := larkcore.StringValue(s.SenderId.OpenId)
	text, atts := parseContent(larkcore.StringValue(m.MessageType), larkcore.StringValue(m.Content), mentions)

	return channel.Message{
		ID:          channel.ID(Name, larkcore.StringValue(m.MessageId)),
		Channel:     Name,
		ChatID:      larkcore.StringValue(m.ChatId),
		ChatType:    chatType,
		ThreadID:    larkcore.StringValue(m.ThreadId),
		ReplyTo:     prefixed(larkcore.StringValue(m.ParentId)),
		Sender:      channel.User{ID: senderID, Mention: mentionSyntax(senderID)},
		Text:        text,
		Mentions:    otherMentions(mentions),
		Attachments: atts,
		CreatedAt:   millis(larkcore.StringValue(m.CreateTime)),
	}, true
}

func toChatType(larkType string) channel.ChatType {
	if larkType == chatP2P {
		return channel.ChatDirect
	}

	return channel.ChatGroup
}

// eventMentions converts event mentions, flagging the ones that are our bot.
// Without a known bot open_id, any mentioned bot is assumed to be us (the
// platform only delivers group messages that @ this app anyway).
func eventMentions(in []*larkim.MentionEvent, botOpenID string) []mention {
	out := make([]mention, 0, len(in))

	for _, mm := range in {
		if mm == nil {
			continue
		}

		openID := ""
		if mm.Id != nil {
			openID = larkcore.StringValue(mm.Id.OpenId)
		}

		isBot := openID != "" && openID == botOpenID ||
			botOpenID == "" && larkcore.StringValue(mm.MentionedType) == mentionedBot
		out = append(out, mention{Key: larkcore.StringValue(mm.Key), OpenID: openID, Name: larkcore.StringValue(mm.Name), IsBot: isBot})
	}

	return out
}

func prefixed(id string) string {
	if id == "" {
		return ""
	}

	return channel.ID(Name, id)
}

// Reply implements channel.Channel. Outside direct chats the sender is
// @-mentioned so they are notified (a message fetched with Get carries no chat
// type, so it counts as a group); thread messages are answered in the thread.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	_, platformID, err := channel.SplitID(msg.ID)
	if err != nil {
		return err
	}

	mentionID := ""
	if msg.ChatType != channel.ChatDirect {
		mentionID = msg.Sender.ID
	}

	return c.sendWithFallback(text, mentionID, func(msgType, content string) error {
		resp, err := c.client.Im.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(platformID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType(msgType).
				Content(content).
				ReplyInThread(msg.ThreadID != "").
				Uuid(dedupeKey(msg.ID, msgType, content)).
				Build()).
			Build())
		if err != nil {
			return err
		}

		return checkCode(resp.Code, resp.Msg)
	})
}

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	return c.sendWithFallback(text, "", func(msgType, content string) error {
		resp, err := c.client.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType(msgType).
				Content(content).
				Uuid(dedupeKey(chatID, msgType, content)).
				Build()).
			Build())
		if err != nil {
			return err
		}

		return checkCode(resp.Code, resp.Msg)
	})
}

// sendFunc posts one message of the given type.
type sendFunc func(msgType, content string) error

// sendWithFallback posts Markdown as a rich-text post. Only if the platform
// rejects the post does it retry as plain text: after a transport error the
// post may have gone through, and a second message would be a duplicate.
func (c *Channel) sendWithFallback(text, mentionID string, send sendFunc) error {
	content, err := buildPost(text, mentionID)
	if err != nil {
		return fmt.Errorf("build post: %w", err)
	}

	postErr := send(larkim.MsgTypePost, content)

	if _, rejected := errors.AsType[*apiError](postErr); !rejected {
		if postErr != nil {
			return fmt.Errorf("lark send post: %w", postErr)
		}

		return nil
	}

	c.log.Warn("lark post rejected, retrying as text", "err", postErr)

	content, err = buildText(text, mentionID)
	if err != nil {
		return fmt.Errorf("build text: %w", err)
	}

	if err := send(larkim.MsgTypeText, content); err != nil {
		return errors.Join(fmt.Errorf("lark send post: %w", postErr), fmt.Errorf("lark send text: %w", err))
	}

	return nil
}

// dedupeKey makes retries of the same reply idempotent (Lark dedupes a uuid for 1h).
func dedupeKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Get implements channel.Channel.
func (c *Channel) Get(ctx context.Context, platformID string) (channel.Message, error) {
	resp, err := c.client.Im.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(platformID).Build())
	if err != nil {
		return channel.Message{}, fmt.Errorf("lark get message: %w", err)
	}

	if err := checkCode(resp.Code, resp.Msg); err != nil {
		return channel.Message{}, fmt.Errorf("lark get message: %w", err)
	}

	if resp.Data == nil || len(resp.Data.Items) == 0 {
		return channel.Message{}, fmt.Errorf("lark get message %s: not found", platformID)
	}

	bot, _ := c.Bot(ctx) // best effort: only used to hide the bot's own mention

	return fromItem(resp.Data.Items[0], bot.OpenID), nil
}

func fromItem(it *larkim.Message, botOpenID string) channel.Message {
	mentions := make([]mention, 0, len(it.Mentions))
	for _, mm := range it.Mentions {
		if mm == nil {
			continue
		}

		id := larkcore.StringValue(mm.Id)
		mentions = append(mentions, mention{Key: larkcore.StringValue(mm.Key), OpenID: id, Name: larkcore.StringValue(mm.Name), IsBot: id != "" && id == botOpenID})
	}

	content := ""
	if it.Body != nil {
		content = larkcore.StringValue(it.Body.Content)
	}

	text, atts := parseContent(larkcore.StringValue(it.MsgType), content, mentions)

	var sender channel.User

	if it.Sender != nil {
		id := larkcore.StringValue(it.Sender.Id)
		sender = channel.User{ID: id, Name: larkcore.StringValue(it.Sender.SenderName), Mention: mentionSyntax(id)}
	}

	return channel.Message{
		ID:          channel.ID(Name, larkcore.StringValue(it.MessageId)),
		Channel:     Name,
		ChatID:      larkcore.StringValue(it.ChatId),
		ThreadID:    larkcore.StringValue(it.ThreadId),
		ReplyTo:     prefixed(larkcore.StringValue(it.ParentId)),
		Sender:      sender,
		Text:        text,
		Mentions:    otherMentions(mentions),
		Attachments: atts,
		CreatedAt:   millis(larkcore.StringValue(it.CreateTime)),
	}
}

// Download implements channel.Channel.
func (c *Channel) Download(ctx context.Context, msg channel.Message, att channel.Attachment, dir string) (string, error) {
	_, platformID, err := channel.SplitID(msg.ID)
	if err != nil {
		return "", err
	}

	resp, err := c.client.Im.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().
		MessageId(platformID).FileKey(att.Key).Type(resourceType(att.Kind)).Build())
	if err != nil {
		return "", fmt.Errorf("lark download: %w", err)
	}

	if err := checkCode(resp.Code, resp.Msg); err != nil {
		return "", fmt.Errorf("lark download: %w", err)
	}

	data, err := transport.ReadAll(resp.File)
	if err != nil {
		return "", fmt.Errorf("lark download: %w", err)
	}

	path, err := channel.SaveFile(dir, fileName(att, resp.FileName, data), data)
	if err != nil {
		return "", fmt.Errorf("lark download: %w", err)
	}

	return path, nil
}

func resourceType(kind channel.AttachmentKind) string {
	if kind == channel.AttachmentImage {
		return resourceImage
	}

	return resourceFile
}

// fileName picks a local name for a download: the attachment's own name, the
// name the server sent, or the key, plus an extension sniffed from the data.
func fileName(att channel.Attachment, served string, data []byte) string {
	name := channel.BaseName(att.Name)
	if name == "" {
		name = channel.BaseName(served)
	}

	if name == "" {
		name = channel.PathSafe(att.Key)
	}

	if filepath.Ext(name) == "" {
		name += channel.Extension(data)
	}

	return name
}

// Ack implements channel.Channel with an emoji reaction.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	_, platformID, err := channel.SplitID(msg.ID)
	if err != nil {
		return err
	}

	var errs []error

	for _, emoji := range ackEmojis {
		resp, err := c.client.Im.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
			MessageId(platformID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().EmojiType(emoji).Build()).
				Build()).
			Build())
		if err == nil {
			err = checkCode(resp.Code, resp.Msg)
		}

		if err == nil {
			return nil
		}

		errs = append(errs, fmt.Errorf("lark ack %s: %w", emoji, err))
	}

	return errors.Join(errs...)
}

// MissingScopes returns the permissions the bot needs that are not granted yet.
// Newly granted permissions can take a moment to show up after approval.
func (c *Channel) MissingScopes(ctx context.Context) ([]string, error) {
	resp, err := c.client.Get(ctx, scopesPath, nil, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return nil, fmt.Errorf("lark scopes: %w", err)
	}

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Scopes []struct {
				Name        string `json:"scope_name"`
				Type        string `json:"scope_type"`
				GrantStatus int    `json:"grant_status"`
			} `json:"scopes"`
		} `json:"data"`
	}

	if err := json.Unmarshal(resp.RawBody, &body); err != nil {
		return nil, fmt.Errorf("lark scopes: parse: %w", err)
	}

	if err := checkCode(body.Code, body.Msg); err != nil {
		return nil, fmt.Errorf("lark scopes: %w", err)
	}

	granted := map[string]bool{}

	for _, s := range body.Data.Scopes {
		if s.Type == scopeTenant && s.GrantStatus == scopeGranted {
			granted[s.Name] = true
		}
	}

	var missing []string

	for _, s := range scopes {
		if !granted[s] {
			missing = append(missing, s)
		}
	}

	return missing, nil
}
