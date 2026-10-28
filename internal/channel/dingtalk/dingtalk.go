// Package dingtalk implements the claude-bot channel protocol for DingTalk
// (钉钉): the robot of an internal app receives messages over Stream mode
// (WebSocket), so no public URL is needed, and answers through the OpenAPI.
//
// A DingTalk message is addressed within its conversation, so the platform
// id of a message is "<conversation id>/<message id>".
package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/markdown"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// Name is the channel id prefix.
const Name = "dingtalk"

const (
	clientTimeout  = 2 * time.Minute
	maxMarkdownLen = 4000 // characters per message, below DingTalk's limit
	maxTitleLen    = 30   // characters of the title shown in notifications
	msgKeyMarkdown = "sampleMarkdown"
)

// ErrNoGet is returned by Get: the OpenAPI cannot fetch a message by id.
var ErrNoGet = errors.New("dingtalk: the OpenAPI cannot fetch messages by id")

// Config holds the credentials of the project's DingTalk app (Client ID and
// Client Secret, formerly AppKey and AppSecret). The app's robot code is its
// Client ID.
type Config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// Proxy is "direct" (the default), "env" (HTTPS_PROXY etc.) or a proxy URL.
	Proxy string `json:"proxy,omitempty"`
}

func (c Config) validate() error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("dingtalk: client id and client secret are required")
	}

	return nil
}

// App identifies the DingTalk app whose robot the channel speaks for.
type App struct {
	ClientID string `json:"client_id"`
}

// Channel is the DingTalk adapter.
type Channel struct {
	http   *http.Client
	dialer *websocket.Dialer
	api    string // OpenAPI root, replaced in tests
	cfg    Config
	log    *slog.Logger

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time
}

var _ channel.Channel = (*Channel)(nil)

// New returns a DingTalk channel for cfg. DingTalk is reached directly by
// default, like Feishu: local proxies often break TLS to Chinese services.
func New(cfg Config, log *slog.Logger) (*Channel, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxy, err := transport.ProxyOr(cfg.Proxy, transport.ProxyDirect)
	if err != nil {
		return nil, fmt.Errorf("dingtalk: %w", err)
	}

	return &Channel{
		http:   &http.Client{Transport: transport.HTTP(proxy), Timeout: clientTimeout},
		dialer: transport.WebSocket(proxy),
		api:    defaultAPI,
		cfg:    cfg,
		log:    log,
	}, nil
}

// Name implements channel.Channel.
func (c *Channel) Name() string { return Name }

// App checks the credentials and returns the app they belong to.
func (c *Channel) App(ctx context.Context) (App, error) {
	if _, err := c.accessToken(ctx); err != nil {
		return App{}, err
	}

	return App{ClientID: c.cfg.ClientID}, nil
}

// Reply implements channel.Channel: in a direct chat a message to the
// sender, in a group a message to the group that names the sender. The
// OpenAPI cannot @-mention, so the name does not notify.
func (c *Channel) Reply(ctx context.Context, msg channel.Message, text string) error {
	if msg.ChatType == channel.ChatDirect {
		return c.post(ctx, pathDirectSend, map[string]any{"userIds": []string{msg.Sender.ID}}, text)
	}

	conversation, _, err := channel.LocateScoped(msg.ID)
	if err != nil {
		return err
	}

	if msg.Sender.Mention != "" {
		text = msg.Sender.Mention + " " + text
	}

	return c.Send(ctx, conversation, text)
}

// Send implements channel.Channel. chatID is a group's conversation id.
func (c *Channel) Send(ctx context.Context, chatID, text string) error {
	return c.post(ctx, pathGroupSend, map[string]any{"openConversationId": chatID}, text)
}

// post sends Markdown through a robot send path, split into as many messages
// as the length limit needs. to says who receives it.
func (c *Channel) post(ctx context.Context, path string, to map[string]any, text string) error {
	for i, part := range markdown.Split(text, maxMarkdownLen) {
		param, err := json.Marshal(map[string]string{"title": title(part), "text": part})
		if err != nil {
			return fmt.Errorf("dingtalk send: %w", err)
		}

		body := map[string]any{"robotCode": c.cfg.ClientID, "msgKey": msgKeyMarkdown, "msgParam": string(param)}
		maps.Copy(body, to)

		if err := c.call(ctx, path, body, nil); err != nil {
			return fmt.Errorf("dingtalk send (part %d): %w", i+1, err)
		}
	}

	return nil
}

// title is what notifications and the chat list show for a message: its
// first line without Markdown marks, shortened.
func title(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(strings.TrimLeft(line, "#>*-` "))

	runes := []rune(line)
	if len(runes) > maxTitleLen {
		return string(runes[:maxTitleLen-1]) + "…"
	}

	if line == "" {
		return "claude-bot"
	}

	return line
}

// Get implements channel.Channel; DingTalk offers no way to do it.
func (c *Channel) Get(context.Context, string) (channel.Message, error) {
	return channel.Message{}, ErrNoGet
}

// Download implements channel.Channel. att.Key is the file's download code.
func (c *Channel) Download(ctx context.Context, msg channel.Message, att channel.Attachment, dir string) (string, error) {
	var r struct {
		DownloadURL string `json:"downloadUrl"`
	}

	body := map[string]string{"downloadCode": att.Key, "robotCode": c.cfg.ClientID}
	if err := c.call(ctx, pathDownload, body, &r); err != nil {
		return "", fmt.Errorf("dingtalk download: %w", err)
	}

	if r.DownloadURL == "" {
		return "", errors.New("dingtalk download: no download link in the answer")
	}

	data, err := transport.Download(ctx, c.http, r.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("dingtalk download: %w", err)
	}

	name := channel.BaseName(att.Name)
	if name == "" {
		_, msgID, _ := channel.LocateScoped(msg.ID)
		name = channel.PathSafe(msgID)
	}

	if filepath.Ext(name) == "" {
		name += channel.Extension(data)
	}

	path, err := channel.SaveFile(dir, name, data)
	if err != nil {
		return "", fmt.Errorf("dingtalk download: %w", err)
	}

	return path, nil
}

// Ack implements channel.Channel. The OpenAPI has no reaction for robots,
// so receipt is not acknowledged.
func (c *Channel) Ack(context.Context, channel.Message) error {
	return nil
}
