// Package discord implements the claude-bot channel protocol for Discord,
// receiving messages over the Gateway (WebSocket) so no public URL is needed.
//
// Discord message ids are only addressable together with their channel, so
// the platform id of a message is "<channel id>/<message id>".
package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// Name is the channel id prefix.
const Name = "discord"

// Proxy modes besides a proxy URL; see Config.Proxy.
const (
	ProxyDirect = "direct"
	ProxyEnv    = "env"
)

// Intents the bot needs. Message content is a privileged intent, but Discord
// sends it anyway for direct messages and messages that mention the bot,
// which are the only ones delivered.
const intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages

// Permissions asks for in the invite link: read and answer messages, in
// threads too, and react with the ack emoji.
const Permissions = discordgo.PermissionViewChannel |
	discordgo.PermissionSendMessages |
	discordgo.PermissionSendMessagesInThreads |
	discordgo.PermissionReadMessageHistory |
	discordgo.PermissionAddReactions

const (
	maxMessageLen = 2000 // Discord's limit per message, in characters
	ackEmoji      = "👀"
	fence         = "```"
	maxFenceLen   = 100 // longest fence line repeated when a code block is cut
	dirPerm       = 0o750
	filePerm      = 0o600
	maxDownload   = 512 << 20 // well above Discord's upload limits
)

// userMention matches <@id> and the legacy nickname form <@!id>.
var userMention = regexp.MustCompile(`<@!?(\d+)>`)

// Config holds the bot token of the project's Discord application.
type Config struct {
	Token string `json:"token"`
	// Proxy is "direct", "env" (HTTPS_PROXY etc., the default) or a proxy URL.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	if c.Token == "" {
		return errors.New("discord: token is required")
	}

	return nil
}

// ProxyFunc returns the proxy selector used for both API and Gateway traffic.
func (c Config) ProxyFunc() (func(*http.Request) (*url.URL, error), error) {
	switch c.Proxy {
	case "", ProxyEnv:
		return http.ProxyFromEnvironment, nil
	case ProxyDirect:
		return nil, nil
	}

	u, err := url.Parse(c.Proxy)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("discord: invalid proxy %q: want %s, %s or a URL", c.Proxy, ProxyDirect, ProxyEnv)
	}

	return http.ProxyURL(u), nil
}

// Bot is the identity of the application's bot user.
type Bot struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	AppID string `json:"app_id"`
}

// InviteURL is the link that adds the bot to a server with Permissions.
func (b Bot) InviteURL() string {
	return fmt.Sprintf("https://discord.com/oauth2/authorize?client_id=%s&scope=bot&permissions=%d", url.QueryEscape(b.AppID), Permissions)
}

// Channel is the Discord adapter.
type Channel struct {
	s   *discordgo.Session
	log *slog.Logger

	botMu sync.Mutex
	bot   *Bot // cached once fetched successfully
}

var _ channel.Channel = (*Channel)(nil)

// New returns a Discord channel for cfg.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := cfg.ProxyFunc()
	if err != nil {
		return nil, err
	}

	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("discord: %w", err)
	}

	s.Identify.Intents = intents
	s.Client = &http.Client{Transport: newTransport(proxy)}
	s.Dialer = &websocket.Dialer{Proxy: proxy, HandshakeTimeout: 45 * time.Second}

	return &Channel{s: s, log: log}, nil
}

// newTransport bounds every phase of a request that can hang: dial, TLS and
// waiting for the response headers.
func newTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
	}
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

	u, err := c.s.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return Bot{}, fmt.Errorf("discord bot info: %w", err)
	}

	app, err := c.s.Application("@me") // takes no context; the client has timeouts
	if err != nil {
		return Bot{}, fmt.Errorf("discord application info: %w", err)
	}

	c.bot = &Bot{Name: u.Username, ID: u.ID, AppID: app.ID}

	return *c.bot, nil
}

// Listen implements channel.Channel. It delivers direct messages and server
// messages that @-mention the bot, from people only (never bots or webhooks).
// The session reconnects by itself; ready is called on every (re)connect.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	bot, err := c.Bot(ctx)
	if err != nil {
		return err
	}

	defer c.s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Ready) {
		c.log.Info("discord connected", "bot", bot.Name)
		ready()
	})()
	defer c.s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Resumed) { ready() })()
	defer c.s.AddHandler(func(s *discordgo.Session, ev *discordgo.MessageCreate) {
		msg, ok := fromEvent(ev.Message, bot.ID, isThread(s, ev.ChannelID))
		if !ok {
			return
		}

		if err := deliver(msg); err != nil {
			c.log.Error("discord deliver", "id", msg.ID, "err", err)
		}
	})()

	if err := c.s.Open(); err != nil {
		return fmt.Errorf("discord gateway: %w", err)
	}

	<-ctx.Done()

	if err := c.s.Close(); err != nil {
		c.log.Warn("discord close", "err", err)
	}

	return nil
}

// isThread reports whether a channel is known to be a thread. The state is
// filled from Gateway events, so an unknown channel counts as not a thread.
func isThread(s *discordgo.Session, channelID string) bool {
	ch, err := s.State.Channel(channelID)

	return err == nil && ch.IsThread()
}

// fromEvent converts a received message; ok is false for messages that are
// not addressed to the bot.
func fromEvent(m *discordgo.Message, botID string, inThread bool) (channel.Message, bool) {
	if m == nil || m.Author == nil || m.Author.Bot || m.WebhookID != "" {
		return channel.Message{}, false
	}

	if m.Type != discordgo.MessageTypeDefault && m.Type != discordgo.MessageTypeReply {
		return channel.Message{}, false // joins, pins and other system messages
	}

	chatType := channel.ChatDirect
	if m.GuildID != "" {
		chatType = channel.ChatGroup

		if !mentions(m, botID) {
			return channel.Message{}, false // server chatter not addressed to the bot
		}
	}

	msg := fromMessage(m, botID)
	msg.ChatType = chatType

	if inThread {
		msg.ThreadID = m.ChannelID
	}

	return msg, true
}

func mentions(m *discordgo.Message, botID string) bool {
	for _, u := range m.Mentions {
		if u != nil && u.ID == botID {
			return true
		}
	}

	return false
}

// fromMessage converts the fields every message has. Chat and thread type are
// only known from Gateway events, so they are left to fromEvent.
func fromMessage(m *discordgo.Message, botID string) channel.Message {
	var others []channel.User

	names := map[string]string{}

	for _, u := range m.Mentions {
		if u == nil || u.ID == botID {
			continue
		}

		names[u.ID] = displayName(u)
		others = append(others, user(u))
	}

	text := userMention.ReplaceAllStringFunc(m.Content, func(s string) string {
		id := userMention.FindStringSubmatch(s)[1]
		if id == botID {
			return ""
		}

		if name, ok := names[id]; ok {
			return "@" + name
		}

		return s
	})

	var sender channel.User
	if m.Author != nil {
		sender = user(m.Author)
	}

	return channel.Message{
		ID:          channel.ID(Name, platformID(m.ChannelID, m.ID)),
		Channel:     Name,
		ChatID:      m.ChannelID,
		ReplyTo:     replyTo(m),
		Sender:      sender,
		Text:        strings.TrimSpace(text),
		Mentions:    others,
		Attachments: attachments(m.Attachments),
		CreatedAt:   m.Timestamp.UTC(),
	}
}

func attachments(in []*discordgo.MessageAttachment) []channel.Attachment {
	var out []channel.Attachment

	for _, a := range in {
		if a == nil {
			continue
		}

		kind := channel.AttachmentFile
		if strings.HasPrefix(a.ContentType, "image/") {
			kind = channel.AttachmentImage
		}

		out = append(out, channel.Attachment{Kind: kind, Key: a.ID, Name: a.Filename})
	}

	return out
}

// replyTo is the id of the message m answers, if it is a reply.
func replyTo(m *discordgo.Message) string {
	ref := m.MessageReference
	if ref == nil || ref.MessageID == "" || m.Type != discordgo.MessageTypeReply {
		return ""
	}

	ch := ref.ChannelID
	if ch == "" {
		ch = m.ChannelID
	}

	return channel.ID(Name, platformID(ch, ref.MessageID))
}

func user(u *discordgo.User) channel.User {
	return channel.User{ID: u.ID, Name: displayName(u), Mention: mentionSyntax(u.ID)}
}

func displayName(u *discordgo.User) string {
	if u.GlobalName != "" {
		return u.GlobalName
	}

	return u.Username
}

func mentionSyntax(userID string) string {
	return "<@" + userID + ">"
}

func platformID(channelID, messageID string) string {
	return channelID + "/" + messageID
}

// splitPlatformID returns the channel and message id of a platform id.
func splitPlatformID(id string) (channelID, messageID string, err error) {
	channelID, messageID, ok := strings.Cut(id, "/")
	if !ok || channelID == "" || messageID == "" {
		return "", "", fmt.Errorf("%w %q: want <channel id>/<message id>", channel.ErrInvalidID, id)
	}

	return channelID, messageID, nil
}

// Reply implements channel.Channel. The answer references the original
// message, in the same channel or thread; outside direct messages it starts
// by @-mentioning the sender (a message fetched with Get carries no chat type,
// so it counts as a server message).
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	_, platform, err := channel.SplitID(msg.ID)
	if err != nil {
		return err
	}

	channelID, messageID, err := splitPlatformID(platform)
	if err != nil {
		return err
	}

	if msg.ChatType != channel.ChatDirect && msg.Sender.ID != "" {
		text = mentionSyntax(msg.Sender.ID) + " " + text
	}

	failIfMissing := false
	ref := &discordgo.MessageReference{MessageID: messageID, ChannelID: channelID, FailIfNotExists: &failIfMissing}

	return c.post(ctx, channelID, text, ref)
}

// Send implements channel.Channel.
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	return c.post(ctx, chatID, text, nil)
}

// post sends text, split into as many messages as Discord's length limit
// needs; only the first one references ref. Mentions of users notify them,
// @everyone, @here and roles never do: the text may quote what anyone wrote.
func (c *Channel) post(ctx context.Context, channelID, text string, ref *discordgo.MessageReference) error {
	for i, part := range split(text, maxMessageLen) {
		send := &discordgo.MessageSend{
			Content:         part,
			AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers}},
		}
		if i == 0 {
			send.Reference = ref
		}

		if _, err := c.s.ChannelMessageSendComplex(channelID, send, discordgo.WithContext(ctx)); err != nil {
			return fmt.Errorf("discord send (part %d): %w", i+1, err)
		}
	}

	return nil
}

// split cuts Markdown into messages of at most limit characters, preferring
// line breaks. A code block cut in two is closed at the end of one message
// and reopened at the start of the next, so both still render as code.
func split(text string, limit int) []string {
	budget := limit - len("\n"+fence) // room kept for closing a cut code block

	var (
		parts []string
		cur   strings.Builder
		n     int    // characters in cur
		base  int    // characters in cur that only reopen a code block
		open  string // fence line of the code block cur ends inside, if any
	)

	flush := func() {
		part := strings.TrimSuffix(cur.String(), "\n")
		if open != "" {
			part += "\n" + fence
		}

		parts = append(parts, part)

		cur.Reset()

		n, base = 0, 0

		if open != "" {
			cur.WriteString(open + "\n")
			n = utf8.RuneCountInString(open) + 1
			base = n
		}
	}

	for line := range strings.SplitAfterSeq(text, "\n") {
		fenceLine := strings.TrimSpace(line) // taken before a long line is cut

		for n+utf8.RuneCountInString(line) > budget {
			if n > base {
				flush()

				continue
			}

			// A single line longer than a message: cut it.
			head, tail := cutRunes(line, budget-n)
			cur.WriteString(head)
			n += utf8.RuneCountInString(head)
			line = tail

			flush()
		}

		cur.WriteString(line)
		n += utf8.RuneCountInString(line)

		if strings.HasPrefix(fenceLine, fence) {
			switch {
			case open != "":
				open = ""
			case utf8.RuneCountInString(fenceLine) > maxFenceLen:
				open = fence // reopen without the info string rather than fill the message
			default:
				open = fenceLine
			}
		}
	}

	if n > base || len(parts) == 0 {
		open = "" // a block left open by the author stays as written

		flush()
	}

	return parts
}

// cutRunes splits s after its first n runes.
func cutRunes(s string, n int) (head, tail string) {
	for i := range s {
		if n == 0 {
			return s[:i], s[i:]
		}

		n--
	}

	return s, ""
}

// Get implements channel.Channel.
func (c *Channel) Get(ctx context.Context, platform string) (channel.Message, error) {
	channelID, messageID, err := splitPlatformID(platform)
	if err != nil {
		return channel.Message{}, err
	}

	m, err := c.s.ChannelMessage(channelID, messageID, discordgo.WithContext(ctx))
	if err != nil {
		return channel.Message{}, fmt.Errorf("discord get message: %w", err)
	}

	bot, _ := c.Bot(ctx) // best effort: only used to hide the bot's own mention

	return fromMessage(m, bot.ID), nil
}

// Download implements channel.Channel. Attachment URLs expire, so the message
// is fetched again for a fresh one.
func (c *Channel) Download(ctx context.Context, msg channel.Message, att channel.Attachment, dir string) (string, error) {
	_, platform, err := channel.SplitID(msg.ID)
	if err != nil {
		return "", err
	}

	channelID, messageID, err := splitPlatformID(platform)
	if err != nil {
		return "", err
	}

	m, err := c.s.ChannelMessage(channelID, messageID, discordgo.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("discord download: %w", err)
	}

	var found *discordgo.MessageAttachment

	for _, a := range m.Attachments {
		if a != nil && a.ID == att.Key {
			found = a

			break
		}
	}

	if found == nil {
		return "", fmt.Errorf("discord download: attachment %s not found on %s", att.Key, msg.ID)
	}

	data, err := c.fetch(ctx, found.URL)
	if err != nil {
		return "", fmt.Errorf("discord download: %w", err)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("discord download: %w", err)
	}

	path := filepath.Join(dir, fileName(found))
	if err := os.WriteFile(path, data, filePerm); err != nil {
		return "", fmt.Errorf("discord download: %w", err)
	}

	return path, nil
}

func (c *Channel) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET attachment: %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}

	return data, nil
}

// fileName is the attachment's own name without any directories, or its id.
func fileName(a *discordgo.MessageAttachment) string {
	switch base := filepath.Base(a.Filename); base {
	case "", ".", "..", string(filepath.Separator):
		return a.ID
	default:
		return base
	}
}

// Ack implements channel.Channel with an emoji reaction.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	_, platform, err := channel.SplitID(msg.ID)
	if err != nil {
		return err
	}

	channelID, messageID, err := splitPlatformID(platform)
	if err != nil {
		return err
	}

	if err := c.s.MessageReactionAdd(channelID, messageID, ackEmoji, discordgo.WithContext(ctx)); err != nil {
		return fmt.Errorf("discord ack: %w", err)
	}

	return nil
}
