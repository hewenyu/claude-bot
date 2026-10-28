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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "discord"

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
	inviteURL   = "https://discord.com/oauth2/authorize?client_id=%s&scope=bot&permissions=%d"
	tokenPrefix = "Bot " // Authorization scheme of bot tokens
	self        = "@me"  // the authenticated user or application in REST paths
	ackEmoji    = "👀"
	dirPerm     = 0o750
	filePerm    = 0o600
	maxDownload = 512 << 20 // well above Discord's upload limits
)

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
func (c Config) ProxyFunc() (transport.ProxyFunc, error) {
	mode := c.Proxy
	if mode == "" {
		mode = transport.ProxyEnv
	}

	proxy, err := transport.Proxy(mode)
	if err != nil {
		return nil, fmt.Errorf("discord: %w", err)
	}

	return proxy, nil
}

// Bot is the identity of the application's bot user.
type Bot struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	AppID string `json:"app_id"`
}

// InviteURL is the link that adds the bot to a server with Permissions.
func (b Bot) InviteURL() string {
	return fmt.Sprintf(inviteURL, url.QueryEscape(b.AppID), Permissions)
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

	s, err := discordgo.New(tokenPrefix + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("discord: %w", err)
	}

	s.Identify.Intents = intents
	s.Client = &http.Client{Transport: transport.HTTP(proxy)}
	s.Dialer = transport.WebSocket(proxy)

	return &Channel{s: s, log: log}, nil
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

	u, err := c.s.User(self, discordgo.WithContext(ctx))
	if err != nil {
		return Bot{}, fmt.Errorf("discord bot info: %w", err)
	}

	app, err := c.s.Application(self) // takes no context; the client has timeouts
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

	removers := []func(){
		c.s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Ready) {
			c.log.Info("discord connected", "bot", bot.Name)
			ready()
		}),
		c.s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Resumed) { ready() }),
		c.s.AddHandler(func(s *discordgo.Session, ev *discordgo.MessageCreate) {
			msg, ok := fromEvent(ev.Message, bot.ID, isThread(s, ev.ChannelID))
			if !ok {
				return
			}

			if err := deliver(msg); err != nil {
				c.log.Error("discord deliver", "id", msg.ID, "err", err)
			}
		}),
	}

	defer func() {
		for _, remove := range removers {
			remove()
		}
	}()

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

// Reply implements channel.Channel. The answer references the original
// message, in the same channel or thread; outside direct messages it starts
// by @-mentioning the sender (a message fetched with Get carries no chat type,
// so it counts as a server message).
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	channelID, messageID, err := locate(msg.ID)
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

// Get implements channel.Channel.
func (c *Channel) Get(ctx context.Context, platformID string) (channel.Message, error) {
	channelID, messageID, err := splitPlatformID(platformID)
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
	channelID, messageID, err := locate(msg.ID)
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

// fetch downloads a file, refusing one larger than maxDownload rather than
// saving it cut short.
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

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}

	if len(data) > maxDownload {
		return nil, fmt.Errorf("attachment larger than %d MiB", maxDownload>>20)
	}

	return data, nil
}

// fileName is the attachment's own name without any directories, or its id.
func fileName(a *discordgo.MessageAttachment) string {
	if name := channel.BaseName(a.Filename); name != "" {
		return name
	}

	return a.ID
}

// Ack implements channel.Channel with an emoji reaction.
func (c *Channel) Ack(ctx context.Context, msg channel.Message) error {
	channelID, messageID, err := locate(msg.ID)
	if err != nil {
		return err
	}

	if err := c.s.MessageReactionAdd(channelID, messageID, ackEmoji, discordgo.WithContext(ctx)); err != nil {
		return fmt.Errorf("discord ack: %w", err)
	}

	return nil
}
