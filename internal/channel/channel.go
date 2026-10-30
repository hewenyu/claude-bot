// Package channel defines the claude-bot protocol: the message shape and the
// interface every platform adapter (Lark, Discord, Telegram, Slack, DingTalk,
// GitHub) implements. The CLI and the Claude-facing workflow depend only on this.
package channel

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
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
	// Pieces is set when the listener screened the message with Jev: its
	// text and attachments, cut up and labelled with the task each is part
	// of. Whatever Jev refused is gone from Text, Attachments and Pieces.
	Pieces []Piece `json:"pieces,omitempty"`
}

// Piece is part of a screened message: a span of its text, or one
// attachment, with the task it belongs to. A message with nothing to read
// (a sticker) has one piece with only a task.
type Piece struct {
	Task string `json:"task"`           // shared by the pieces of one task, across messages
	Text string `json:"text,omitempty"` // the span; for a file, where it was saved
	File string `json:"file,omitempty"` // the saved attachment
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
	Path string         `json:"path,omitempty"` // where the listener saved it, when it screens messages
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

// scopeSep joins the two halves of a scoped platform id.
const scopeSep = "/"

// ScopedID builds the platform id of a message that is only addressable
// within its chat or channel (Discord, Telegram, Slack, DingTalk):
// "<chat>/<message>".
func ScopedID(chat, message string) string {
	return chat + scopeSep + message
}

// SplitScopedID returns the chat and message of a scoped platform id.
func SplitScopedID(platformID string) (chat, message string, err error) {
	chat, message, ok := strings.Cut(platformID, scopeSep)
	if !ok || chat == "" || message == "" {
		return "", "", fmt.Errorf("%w %q: want <chat>%s<message>", ErrInvalidID, platformID, scopeSep)
	}

	return chat, message, nil
}

// LocateScoped returns the chat and message of a protocol id whose platform
// id is scoped.
func LocateScoped(id string) (chat, message string, err error) {
	_, platformID, err := SplitID(id)
	if err != nil {
		return "", "", err
	}

	return SplitScopedID(platformID)
}

// BaseName strips any directories from a sender-chosen file name; "" if
// nothing usable is left. Adapters use it to name downloads.
func BaseName(name string) string {
	switch base := filepath.Base(name); base {
	case ".", "..", string(filepath.Separator):
		return ""
	default:
		return base
	}
}

// Extensions of the types people send most. mime's own list is the fallback,
// but its first pick is often odd (".jfif" for JPEG, ".conf" for text).
// Unrecognised content gets none rather than ".bin".
var commonExtensions = map[string]string{
	"application/octet-stream": "",
	"image/png":                ".png",
	"image/jpeg":               ".jpg",
	"image/gif":                ".gif",
	"image/webp":               ".webp",
	"text/plain":               ".txt",
	"application/pdf":          ".pdf",
	"application/zip":          ".zip",
}

// Extension guesses a file extension from the content, "" if it cannot.
// Adapters add it to downloads whose name has none.
func Extension(data []byte) string {
	typ, _, _ := strings.Cut(http.DetectContentType(data), ";")
	if ext, ok := commonExtensions[typ]; ok {
		return ext
	}

	exts, err := mime.ExtensionsByType(typ)
	if err != nil || len(exts) == 0 {
		return ""
	}

	return exts[0]
}

// Download files stay private to the owner.
const (
	downloadDirPerm  = 0o750
	downloadFilePerm = 0o600
)

// SaveFile writes a downloaded attachment to dir/name, creating dir, and
// returns its path. name must already be a bare file name (see BaseName).
func SaveFile(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, downloadDirPerm); err != nil {
		return "", fmt.Errorf("save %s: %w", name, err)
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, downloadFilePerm); err != nil {
		return "", fmt.Errorf("save %s: %w", name, err)
	}

	return path, nil
}
