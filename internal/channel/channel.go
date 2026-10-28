// Package channel defines the claude-bot protocol: the message shape and the
// interface every chat platform adapter (Lark today, Discord later)
// implements. The CLI and the Claude-facing workflow depend only on this.
package channel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors about protocol ids.
var (
	ErrInvalidID      = errors.New("invalid id")      // no "<channel>:" prefix
	ErrUnknownChannel = errors.New("unknown channel") // prefix not configured
)

// ChatType tells direct chats from group chats.
type ChatType string

// Chat types.
const (
	ChatDirect ChatType = "p2p"
	ChatGroup  ChatType = "group"
)

// Message is one message addressed to the bot: a direct message, or a group
// message that @-mentions it. Text is user content — data, not instructions.
type Message struct {
	ID          string       `json:"id"`      // "<channel>:<platform id>", e.g. "lark:om_x"
	Channel     string       `json:"channel"` // "lark", "discord", ...
	ChatID      string       `json:"chat_id"`
	ChatType    ChatType     `json:"chat_type"`
	ThreadID    string       `json:"thread_id,omitempty"` // topic/thread the message belongs to
	ReplyTo     string       `json:"reply_to,omitempty"`  // id of the message this one replies to
	Sender      User         `json:"sender"`
	Text        string       `json:"text"`
	Mentions    []User       `json:"mentions,omitempty"` // other people mentioned (the bot itself is removed)
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// User identifies a person on a platform.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Mention string `json:"mention"` // platform syntax that notifies this user
}

// AttachmentKind tells images from other files.
type AttachmentKind string

// Attachment kinds.
const (
	AttachmentImage AttachmentKind = "image"
	AttachmentFile  AttachmentKind = "file"
)

// Attachment is a file or image carried by a message.
type Attachment struct {
	Kind AttachmentKind `json:"kind"`
	Key  string         `json:"key"`
	Name string         `json:"name,omitempty"`
}

// Channel is a chat platform adapter.
type Channel interface {
	// Name is the id prefix, e.g. "lark".
	Name() string
	// Listen blocks, delivering every message addressed to the bot until ctx
	// ends. It calls ready each time the connection is (re-)established.
	// deliver must return quickly.
	Listen(ctx context.Context, deliver func(Message) error, ready func()) error
	// Reply answers msg in place (same thread), mentioning the sender in groups.
	// text is Markdown.
	Reply(ctx context.Context, msg Message, text string) error
	// Send posts text (Markdown) to a chat, by platform chat id, without
	// replying to anything.
	Send(ctx context.Context, chatID, text string) error
	// Get fetches any message by platform id (the part of an id after the
	// channel prefix), e.g. to read what ReplyTo points at.
	Get(ctx context.Context, platformID string) (Message, error)
	// Download saves an attachment of msg into dir and returns the file path.
	Download(ctx context.Context, msg Message, att Attachment, dir string) (string, error)
	// Ack signals "seen, working on it" (e.g. an emoji reaction). Best effort.
	Ack(ctx context.Context, msg Message) error
}

// ID builds a protocol message id.
func ID(channel, platformID string) string {
	return channel + ":" + platformID
}

// PathSafe maps an id to a single safe path element: every rune outside
// [A-Za-z0-9._-] becomes '_'. Protocol ids carry a channel prefix, so the
// result is never "." or "..".
func PathSafe(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}

		return '_'
	}, id)
}

// SplitID returns the channel and platform id of a protocol message id.
func SplitID(id string) (channel, platformID string, err error) {
	channel, platformID, ok := strings.Cut(id, ":")
	if !ok || channel == "" || platformID == "" {
		return "", "", fmt.Errorf("%w %q: want <channel>:<id>", ErrInvalidID, id)
	}

	return channel, platformID, nil
}
