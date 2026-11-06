package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/transport"
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
	_, err := transport.Retry(ctx, transport.ReconnectBackoff(), c.log, "dingtalk credentials", fatal, c.App)
	if err != nil || ctx.Err() != nil {
		return err
	}

	return transport.Reconnect(ctx, c.log, Name, fatal, func(ctx context.Context) (bool, error) {
		return c.session(ctx, deliver, ready)
	})
}

// streamURL registers a Stream connection for robot messages and returns
// the address to open it at.
func (c *Channel) streamURL(ctx context.Context) (string, error) {
	body := map[string]any{
		"clientId":      c.cfg.ClientID,
		"clientSecret":  c.cfg.ClientSecret,
		"subscriptions": []map[string]string{{"type": frameCallback, "topic": topicBotMessage}},
		"ua":            transport.UserAgent,
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

	opened, err := transport.Session(ctx, c.dialer, wsURL, func() {
		c.log.Info("dingtalk connected", "client_id", c.cfg.ClientID)
		ready()
	}, func(conn *websocket.Conn, data []byte) (bool, error) {
		return c.frame(conn, data, deliver)
	})
	if err != nil {
		return opened, fmt.Errorf("dingtalk stream: %w", err)
	}

	return opened, nil
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
		Headers: frameHeaders{MessageID: f.Headers.MessageID, ContentType: transport.JSON},
		Message: ackMessage,
		Data:    data,
	}

	if err := transport.WriteJSON(conn, a); err != nil {
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
