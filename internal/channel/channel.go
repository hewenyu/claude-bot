// Package channel defines the claude-bot protocol: the message shape and the
// interface every platform adapter (Lark, Discord, Telegram, Slack, DingTalk,
// GitHub) implements. The CLI and the Claude-facing workflow depend only on this.
package channel

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hewenyu/claude-bot/internal/transport"
)

// Errors about protocol ids.
var (
	ErrInvalidID      = errors.New("invalid id")      // no "<channel>:" prefix
	ErrUnknownChannel = errors.New("unknown channel") // prefix not configured
)

// ErrNoGet is returned by Get on platforms that cannot fetch a message by id.
var ErrNoGet = fmt.Errorf("cannot fetch messages by id: %w", errors.ErrUnsupported)

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
	// ends. It calls ready once the first connection is up; adapters may call
	// it again on reconnects, which must be harmless. deliver must return
	// quickly.
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

// Closable is a Channel whose chats can be closed on the platform, by anyone
// and from anywhere: a GitHub issue closed, a PR closed or merged.
type Closable interface {
	// ClosedAt reports when the chat (a platform chat id) was closed, or
	// the zero time while it is open.
	ClosedAt(ctx context.Context, chatID string) (time.Time, error)
}

// Identifier is a Channel that can tell who its credentials belong to.
type Identifier interface {
	// Identity asks the platform and returns a human-readable "who am I",
	// e.g. "Telegram @my_bot". It doubles as a credentials check.
	Identity(ctx context.Context) (string, error)
}

// MentionsSender reports whether a reply to msg @-mentions its sender, so
// they are notified: everywhere but in direct chats. A message fetched with
// Get may carry no chat type; it counts as a group message.
func MentionsSender(msg Message) bool {
	return msg.ChatType != ChatDirect
}

// Memo holds what an adapter fetches once, e.g. the bot's identity. Only a
// success is kept, so a failed fetch is tried again on the next call. The
// zero value is ready to use.
type Memo[T any] struct {
	mu  sync.Mutex
	v   T
	set bool
}

// Get returns the kept value, fetching it first if there is none yet.
func (m *Memo[T]) Get(ctx context.Context, fetch func(context.Context) (T, error)) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.set {
		return m.v, nil
	}

	v, err := fetch(ctx)
	if err != nil {
		var zero T

		return zero, err
	}

	m.v, m.set = v, true

	return v, nil
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

// KindOf tells images from other files by MIME type.
func KindOf(mimeType string) AttachmentKind {
	if strings.HasPrefix(mimeType, "image/") {
		return AttachmentImage
	}

	return AttachmentFile
}

// UniqueNames makes the names of one message's attachments unique, so their
// downloads do not overwrite each other: a second "image.png" becomes
// "image-2.png". Unnamed attachments are left alone: downloads name them
// after their key.
func UniqueNames(atts []Attachment) {
	seen := map[string]bool{}

	for i := range atts {
		name := BaseName(atts[i].Name)
		if name == "" {
			continue
		}

		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)

		// Case is ignored: so do the file systems of macOS and Windows.
		for n := 2; seen[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}

		seen[strings.ToLower(name)] = true

		if name != BaseName(atts[i].Name) {
			atts[i].Name = name
		}
	}
}

// FileName is the local name of a download: the first usable of names (the
// attachment's own name first, then any the platform reports), else key made
// path-safe, plus an extension sniffed from data when it has none.
func FileName(key string, data []byte, names ...string) string {
	name := ""

	for _, n := range names {
		if name = BaseName(n); name != "" {
			break
		}
	}

	if name == "" {
		name = PathSafe(key)
	}

	if filepath.Ext(name) == "" {
		name += Extension(data)
	}

	return name
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
	downloadDirPerm = 0o750
	sniffLen        = 512 // bytes http.DetectContentType looks at
)

// SaveFile writes a downloaded attachment to dir/name, creating dir, and
// returns its path. name must already be a bare file name (see BaseName).
func SaveFile(dir, name string, data []byte) (string, error) {
	return save(dir, name, bytes.NewReader(data), int64(len(data)))
}

// SaveStream writes a download read from r into dir, creating dir, and
// returns its path. The file is named as FileName does, the extension
// sniffed from its first bytes. A file over transport.MaxDownload fails with
// transport.ErrTooLarge and leaves nothing behind.
func SaveStream(dir, key string, r io.Reader, names ...string) (string, error) {
	return saveStream(dir, key, r, transport.MaxDownload, names...)
}

func saveStream(dir, key string, r io.Reader, limit int64, names ...string) (string, error) {
	br := bufio.NewReaderSize(r, sniffLen)

	head, err := br.Peek(sniffLen)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read file: %w", err)
	}

	return save(dir, FileName(key, head, names...), br, limit)
}

// SaveURL downloads url with c, sending header, into dir: SaveStream, named
// after key and names.
func SaveURL(ctx context.Context, c *http.Client, url string, header http.Header, dir, key string, names ...string) (string, error) {
	var path string

	err := transport.Download(ctx, c, url, header, func(r io.Reader) error {
		var err error

		path, err = SaveStream(dir, key, r, names...)

		return err
	})

	return path, err
}

// save copies at most limit bytes of r to dir/name through a temporary file
// renamed into place, so a failed or refused download leaves no partial
// file behind.
func save(dir, name string, r io.Reader, limit int64) (string, error) {
	if err := os.MkdirAll(dir, downloadDirPerm); err != nil {
		return "", fmt.Errorf("save %s: %w", name, err)
	}

	tmp, err := os.CreateTemp(dir, ".download-*") // mode 0600
	if err != nil {
		return "", fmt.Errorf("save %s: %w", name, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // already gone once renamed

	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	switch {
	case err != nil:
		return "", fmt.Errorf("save %s: %w", name, err)
	case n > limit:
		return "", fmt.Errorf("save %s: file %w: over %d MiB", name, transport.ErrTooLarge, limit>>20)
	}

	path := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("save %s: %w", name, err)
	}

	return path, nil
}
