package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
)

const (
	envelopeHello      = "hello"
	envelopeDisconnect = "disconnect"
	envelopeEvents     = "events_api"
	reasonLinkDisabled = "link_disabled" // Socket Mode was turned off for the app
)

// errLinkDisabled ends Listen: reconnecting cannot help until Socket Mode is
// turned back on.
var errLinkDisabled = errors.New("slack: Socket Mode is disabled for the app (Settings → Socket Mode)")

type envelope struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Reason     string          `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
}

// Listen implements channel.Channel. It delivers direct messages and
// @-mentions of the bot, from people only. Dropped connections, and the
// refreshes Slack asks for, reconnect with backoff; only a rejected token or
// Socket Mode being disabled ends it. ready is called on every connect.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	bot, err := transport.Retry(ctx, transport.ReconnectBackoff(), c.log, "slack auth.test", fatal, c.Bot)
	if err != nil || ctx.Err() != nil {
		return err
	}

	return transport.Reconnect(ctx, c.log, Name, fatal, func(ctx context.Context) (bool, error) {
		return c.session(ctx, bot, deliver, ready)
	})
}

// socketURL asks for a fresh Socket Mode endpoint.
func (c *Channel) socketURL(ctx context.Context) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, c.cfg.AppToken, methodOpenSocket, nil, &r); err != nil {
		return "", err
	}

	return r.URL, nil
}

// session runs one Socket Mode connection until it drops, Slack asks for a
// reconnect (nil error) or ctx ends. connected tells whether Slack said hello.
func (c *Channel) session(ctx context.Context, bot Bot, deliver func(channel.Message) error, ready func()) (connected bool, err error) {
	wsURL, err := c.socketURL(ctx)
	if err != nil {
		return false, err
	}

	_, err = transport.Session(ctx, c.dialer, wsURL, nil, func(conn *websocket.Conn, data []byte) (bool, error) {
		hello, end, err := c.frame(ctx, conn, data, bot, deliver)
		if hello {
			connected = true

			c.log.Info("slack connected", "bot", bot.Name, "team", bot.Team)
			ready()
		}

		return end, err
	})
	if err != nil {
		return connected, fmt.Errorf("slack socket: %w", err)
	}

	return connected, nil
}

// frame acknowledges and handles one Socket Mode frame. hello reports that
// the connection is up; end that Slack asked to reconnect (err says whether
// that can help).
func (c *Channel) frame(ctx context.Context, conn *websocket.Conn, data []byte, bot Bot, deliver func(channel.Message) error) (hello, end bool, err error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.log.Warn("slack socket: bad frame", "err", err)

		return false, false, nil
	}

	// Acknowledge first: Slack redelivers what is not acknowledged within
	// 3 s, and the inbox drops duplicates anyway.
	if env.EnvelopeID != "" {
		if err := transport.WriteJSON(conn, map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
			return false, true, fmt.Errorf("slack socket ack: %w", err)
		}
	}

	switch env.Type {
	case envelopeHello:
		return true, false, nil
	case envelopeDisconnect:
		if env.Reason == reasonLinkDisabled {
			return false, true, errLinkDisabled
		}

		return false, true, nil
	case envelopeEvents:
		c.handle(ctx, env.Payload, bot, deliver)
	}

	return false, false, nil
}

func (c *Channel) handle(ctx context.Context, payload json.RawMessage, bot Bot, deliver func(channel.Message) error) {
	var p struct {
		Event event `json:"event"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("slack event: bad payload", "err", err)

		return
	}

	msg, ok := fromEvent(p.Event, bot.UserID)
	if !ok {
		return
	}

	c.names.NameSender(ctx, &msg, c.log, nameWarning)

	if err := deliver(msg); err != nil {
		c.log.Error("slack deliver", "id", msg.ID, "err", err)
	}
}
