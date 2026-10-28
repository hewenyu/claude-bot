package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	backoffStart = 2 * time.Second
	backoffCap   = time.Minute
	pingEvery    = 30 * time.Second
	readTimeout  = 3 * pingEvery // no frame, not even a pong, for this long: reconnect
	writeTimeout = 10 * time.Second
	maxFrame     = 4 << 20

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
	bot, err := c.Bot(ctx)
	if err != nil {
		return err
	}

	var retry time.Duration

	for {
		connected, err := c.session(ctx, bot, deliver, ready)

		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // cancelled: the connection error is just the shutdown
		case fatal(err) || errors.Is(err, errLinkDisabled):
			return err
		case connected:
			retry = 0
		}

		if err == nil && connected {
			c.log.Info("slack reconnecting", "reason", "requested by Slack")

			continue
		}

		retry = min(max(2*retry, backoffStart), backoffCap)
		c.log.Warn("slack connection", "err", err, "retry_in", retry)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(retry):
		}
	}
}

// socketURL asks for a fresh Socket Mode endpoint.
func (c *Channel) socketURL(ctx context.Context) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, c.cfg.AppToken, "apps.connections.open", nil, &r); err != nil {
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

	conn, resp, err := c.dialer.DialContext(ctx, wsURL, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}

	if err != nil {
		return false, fmt.Errorf("slack socket: %w", err)
	}

	defer conn.Close() //nolint:errcheck // closing a finished connection

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	done := make(chan struct{})
	defer close(done)

	keepAlive(conn, done)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return connected, nil
			}

			return connected, fmt.Errorf("slack socket: %w", err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

		hello, end, err := c.frame(conn, data, bot, deliver)
		if hello {
			connected = true

			c.log.Info("slack connected", "bot", bot.Name, "team", bot.Team)
			ready()
		}

		if end || err != nil {
			return connected, err
		}
	}
}

// frame acknowledges and handles one Socket Mode frame. hello reports that
// the connection is up; end that Slack asked to reconnect (err says whether
// that can help).
func (c *Channel) frame(conn *websocket.Conn, data []byte, bot Bot, deliver func(channel.Message) error) (hello, end bool, err error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.log.Warn("slack socket: bad frame", "err", err)

		return false, false, nil
	}

	// Acknowledge first: Slack redelivers what is not acknowledged within
	// 3 s, and the inbox drops duplicates anyway.
	if env.EnvelopeID != "" {
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := conn.WriteJSON(map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
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
		c.handle(env.Payload, bot, deliver)
	}

	return false, false, nil
}

// keepAlive pings the server until done is closed; every frame, pongs
// included, pushes the read deadline back.
func keepAlive(conn *websocket.Conn, done <-chan struct{}) {
	conn.SetReadLimit(maxFrame)
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))

		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout))
	})

	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()

		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout))
			}
		}
	}()
}

func (c *Channel) handle(payload json.RawMessage, bot Bot, deliver func(channel.Message) error) {
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

	if err := deliver(msg); err != nil {
		c.log.Error("slack deliver", "id", msg.ID, "err", err)
	}
}
