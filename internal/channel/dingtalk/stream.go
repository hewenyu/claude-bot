package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
)

const (
	backoffStart = 2 * time.Second // first wait after a failed connection
	backoffCap   = time.Minute
	userAgent    = "claude-bot"
)

// Stream mode frames: their types, the topics claude-bot handles, and the
// subscription that asks for robot messages.
const (
	frameSystem      = "SYSTEM"
	frameCallback    = "CALLBACK"
	topicPing        = "ping"
	topicDisconnect  = "disconnect"
	topicBotMessage  = "/v1.0/im/bot/messages/get"
	ackOK            = 200
	ackMessage       = "OK"
	ackEmptyResponse = `{"response":null}`
)

// frame is what the Stream server sends; data is a JSON document in a string.
type frame struct {
	Type    string       `json:"type"`
	Headers frameHeaders `json:"headers"`
	Data    string       `json:"data"`
}

type frameHeaders struct {
	MessageID   string `json:"messageId"`
	Topic       string `json:"topic"`
	ContentType string `json:"contentType,omitempty"`
}

// ack answers a frame; unanswered callbacks are pushed again.
type ack struct {
	Code    int          `json:"code"`
	Headers frameHeaders `json:"headers"`
	Message string       `json:"message"`
	Data    string       `json:"data"`
}

// Listen implements channel.Channel. It delivers direct messages to the
// robot and group messages that @-mention it (the only group messages
// DingTalk pushes to a robot). Dropped connections, and the reconnects the
// server asks for, are retried with backoff; only refused credentials end it.
// ready is called on every connect.
func (c *Channel) Listen(ctx context.Context, deliver func(channel.Message) error, ready func()) error {
	if _, err := c.App(ctx); err != nil {
		return err
	}

	backoff := transport.Backoff{Start: backoffStart, Cap: backoffCap}

	for {
		connected, err := c.session(ctx, deliver, ready)

		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // cancelled: the connection error is just the shutdown
		case fatal(err):
			return err
		case connected:
			backoff.Reset()
		}

		if err == nil && connected {
			c.log.Info("dingtalk reconnecting", "reason", "requested by DingTalk")

			continue
		}

		wait := backoff.Next()
		c.log.Warn("dingtalk connection", "err", err, "retry_in", wait)

		if !transport.Sleep(ctx, wait) {
			return nil
		}
	}
}

// streamURL registers a Stream connection for robot messages and returns
// the address to open it at.
func (c *Channel) streamURL(ctx context.Context) (string, error) {
	body := map[string]any{
		"clientId":      c.cfg.ClientID,
		"clientSecret":  c.cfg.ClientSecret,
		"subscriptions": []map[string]string{{"type": frameCallback, "topic": topicBotMessage}},
		"ua":            userAgent,
	}

	var r struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
	}
	if err := c.call(ctx, pathOpenStream, body, &r); err != nil {
		return "", err
	}

	u, err := url.Parse(r.Endpoint)
	if err != nil || r.Endpoint == "" || r.Ticket == "" {
		return "", fmt.Errorf("dingtalk %s: no usable endpoint in the answer", pathOpenStream)
	}

	q := u.Query()
	q.Set("ticket", r.Ticket)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// session runs one Stream connection until it drops, the server asks for a
// reconnect (nil error) or ctx ends. connected tells whether it was opened.
func (c *Channel) session(ctx context.Context, deliver func(channel.Message) error, ready func()) (connected bool, err error) {
	wsURL, err := c.streamURL(ctx)
	if err != nil {
		return false, err
	}

	conn, resp, err := c.dialer.DialContext(ctx, wsURL, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}

	if err != nil {
		return false, fmt.Errorf("dingtalk stream: %w", err)
	}

	defer conn.Close() //nolint:errcheck // closing a finished connection

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	done := make(chan struct{})
	defer close(done)

	touch := transport.KeepAlive(conn, done)

	c.log.Info("dingtalk connected", "client_id", c.cfg.ClientID)
	ready()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}

			return true, fmt.Errorf("dingtalk stream: %w", err)
		}

		touch()

		end, err := c.frame(conn, data, deliver)
		if end || err != nil {
			return true, err
		}
	}
}

// frame answers and handles one frame. end reports that the server is about
// to close the connection.
func (c *Channel) frame(conn *websocket.Conn, data []byte, deliver func(channel.Message) error) (end bool, err error) {
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		c.log.Warn("dingtalk stream: bad frame", "err", err)

		return false, nil
	}

	if f.Type == frameSystem {
		switch f.Headers.Topic {
		case topicDisconnect:
			return true, nil
		case topicPing:
			return false, c.ack(conn, f, f.Data) // the ping's data goes back as is
		}

		return false, nil // other system notices expect no answer
	}

	// Answer first: DingTalk pushes again what is not answered in time, and
	// the inbox drops duplicates anyway.
	if err := c.ack(conn, f, ackEmptyResponse); err != nil {
		return false, err
	}

	if f.Type == frameCallback && f.Headers.Topic == topicBotMessage {
		c.handle(f.Data, deliver)
	}

	return false, nil
}

func (c *Channel) ack(conn *websocket.Conn, f frame, data string) error {
	a := ack{
		Code:    ackOK,
		Headers: frameHeaders{MessageID: f.Headers.MessageID, ContentType: "application/json"},
		Message: ackMessage,
		Data:    data,
	}

	_ = conn.SetWriteDeadline(time.Now().Add(transport.WriteTimeout))

	if err := conn.WriteJSON(a); err != nil {
		return fmt.Errorf("dingtalk stream ack: %w", err)
	}

	return nil
}

func (c *Channel) handle(data string, deliver func(channel.Message) error) {
	var m robotMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		c.log.Warn("dingtalk message: bad payload", "err", err)

		return
	}

	msg, ok := fromRobotMessage(m)
	if !ok {
		return
	}

	if err := deliver(msg); err != nil {
		c.log.Error("dingtalk deliver", "id", msg.ID, "err", err)
	}
}
