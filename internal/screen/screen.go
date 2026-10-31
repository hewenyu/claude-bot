// Package screen prepares pending messages for Claude with Jev, which does
// two things only: it groups a conversation's messages into tasks, and it
// tells which tasks ask for something illegal. One message is always one
// task, whole: code cuts messages into pieces (sentences, clauses, attached
// files) only so that Jev compares short spans, and does the grouping
// arithmetic and the thresholds; Jev answers one yes/no question at a time.
package screen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/jev"
)

// Asker answers yes/no questions about a state; *jev.Client is one.
type Asker interface {
	Nouls(ctx context.Context, state any, questions map[string]jev.Noul) (map[string]float64, error)
}

// Thresholds on Jev's probabilities.
const (
	// SameTask joins two pieces of different messages into one task.
	SameTask = 0.7
	// Continues joins a piece to an earlier task. Lower than SameTask: the
	// earlier task is a whole conversation Jev reads only part of, and a
	// further requirement on it ("对了，版本要从0.2开始") scores 0.5–0.7,
	// while new asks score under 0.15 in the live evals.
	Continues = 0.5
	// Refuse marks a task illegal. Set high: a false refusal turns away a
	// real user, while a miss still reaches Claude, which has rules of its own.
	Refuse = 0.8
)

// Bounds on what Jev reads.
const (
	// maxPieces bounds a conversation's pieces at the finest cut that keeps
	// within it; the pairwise questions grow with its square.
	maxPieces = 40
	// maxPieceRunes bounds one piece; the rest of a long piece adds little.
	maxPieceRunes = 400
	// excerptBytes is how much of a text file Jev reads.
	excerptBytes = 1500
)

// Item is a pending message and the downloaded path of each attachment
// ("" when it could not be downloaded).
type Item struct {
	Msg   channel.Message
	Files []string
}

// Piece is part of a message: a span of its text, or one attachment.
type Piece struct {
	Message    string `json:"message"`
	Text       string `json:"text,omitempty"` // the span, trimmed; empty for files
	File       string `json:"file,omitempty"` // downloaded attachment
	Attachment int    `json:"-"`              // index in Attachments; -1 for text
	start, end int    // span of the message text
	jevText    string // what Jev reads
}

// Thread is a task already in the inbox that new pieces may continue.
type Thread struct {
	ID string
	// Transcript is its conversation so far, people and bot, as Jev reads
	// it; Asked is only what people said, the context of a legality check.
	Transcript, Asked string
}

// Task is a group of pieces about the same thing: every piece of each of its
// messages. Thread, if set, is the task in the inbox they continue. Messages
// lists, oldest first, every message it draws on; a message with nothing to
// read (a sticker) is a task of its own with no pieces.
type Task struct {
	Thread   string
	Messages []string
	Pieces   []Piece
	// Scores is the probability of each illegal category.
	Scores map[string]float64
}

// Refused reports whether the task asks for something illegal.
func (t Task) Refused() bool {
	for _, p := range t.Scores {
		if p >= Refuse {
			return true
		}
	}

	return false
}

// Screen cuts the items into pieces, groups each conversation's pieces into
// tasks, joining a group to one of the conversation's threads when it
// continues it, and scores every task for legality, in the context of the
// thread it continues. Tasks come out in the order of their first piece.
func Screen(ctx context.Context, ask Asker, items []Item, threads func(channel.Message) []Thread) ([]Task, error) {
	var tasks []Task

	for _, conv := range conversations(items) {
		pieces := cutConversation(conv)
		open := threads(conv[0].Msg)

		groups, err := group(ctx, ask, pieces, open)
		if err != nil {
			return nil, err
		}

		for _, g := range groups {
			t := Task{Thread: g.thread, Pieces: g.pieces}

			var asked string
			if i := slices.IndexFunc(open, func(th Thread) bool { return th.ID == g.thread }); i >= 0 {
				asked = open[i].Asked
			}

			for _, p := range t.Pieces {
				if !slices.Contains(t.Messages, p.Message) {
					t.Messages = append(t.Messages, p.Message)
				}
			}

			if t.Scores, err = score(ctx, ask, asked, t.Pieces); err != nil {
				return nil, err
			}

			tasks = append(tasks, t)
		}

		for _, it := range conv {
			if !slices.ContainsFunc(pieces, func(p Piece) bool { return p.Message == it.Msg.ID }) {
				tasks = append(tasks, Task{Messages: []string{it.Msg.ID}})
			}
		}
	}

	return tasks, nil
}

// conversations splits items by chat and thread, keeping their order.
func conversations(items []Item) [][]Item {
	var (
		keys  []string
		byKey = map[string][]Item{}
	)

	for _, it := range items {
		k := it.Msg.Channel + "\x00" + it.Msg.ChatID + "\x00" + it.Msg.ThreadID
		if _, ok := byKey[k]; !ok {
			keys = append(keys, k)
		}

		byKey[k] = append(byKey[k], it)
	}

	out := make([][]Item, 0, len(keys))
	for _, k := range keys {
		out = append(out, byKey[k])
	}

	return out
}

// cutConversation cuts a conversation at the finest level that keeps it
// within maxPieces.
func cutConversation(conv []Item) []Piece {
	var pieces []Piece

	for lv := byClause; lv <= byMessage; lv++ {
		pieces = pieces[:0]

		for _, it := range conv {
			for _, s := range cut(it.Msg.Text, lv) {
				text := strings.TrimSpace(it.Msg.Text[s.start:s.end])
				pieces = append(pieces, Piece{
					Message: it.Msg.ID, Text: text, Attachment: -1,
					start: s.start, end: s.end, jevText: truncate(text, maxPieceRunes),
				})
			}

			for i, att := range it.Msg.Attachments {
				path := ""
				if i < len(it.Files) {
					path = it.Files[i]
				}

				pieces = append(pieces, Piece{Message: it.Msg.ID, File: path, Attachment: i, jevText: describe(att, path)})
			}
		}

		if len(pieces) <= maxPieces {
			break
		}
	}

	return pieces
}

// describe is what Jev reads for an attachment: its name and, for a text
// file, how it begins.
func describe(att channel.Attachment, path string) string {
	name := att.Name
	if name == "" && path != "" {
		name = filepath.Base(path)
	}

	head := fmt.Sprintf("[%s %s]", att.Kind, name)
	if path == "" || att.Kind == channel.AttachmentImage {
		return head
	}

	if excerpt := textExcerpt(path); excerpt != "" {
		return head + "\n" + excerpt
	}

	return head
}

// textExcerpt returns the start of a text file, "" for other files.
func textExcerpt(path string) string {
	f, err := os.Open(path) //nolint:gosec // a download claude-bot saved itself
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck // read-only

	buf := make([]byte, excerptBytes)
	n, _ := f.Read(buf)
	buf = buf[:n]

	// Drop a rune cut in half at the end.
	for i := 0; i < utf8.UTFMax-1 && len(buf) > 0 && !utf8.Valid(buf); i++ {
		buf = buf[:len(buf)-1]
	}

	if !utf8.Valid(buf) || slices.Contains(buf, 0) {
		return ""
	}

	return string(buf)
}

func truncate(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}

	return string([]rune(s)[:runes]) + "…"
}

// Crop removes pieces from their message: text spans are cut out and
// attachments dropped. It reports whether anything readable is left.
func Crop(msg channel.Message, pieces []Piece) (channel.Message, bool) {
	var (
		text strings.Builder
		at   int
		drop = map[int]bool{}
	)

	spans := slices.SortedFunc(slices.Values(pieces), func(a, b Piece) int { return a.start - b.start })
	for _, p := range spans {
		if p.Message != msg.ID {
			continue
		}

		if p.Attachment >= 0 {
			drop[p.Attachment] = true

			continue
		}

		text.WriteString(msg.Text[at:p.start])
		at = p.end
	}

	text.WriteString(msg.Text[at:])

	var atts []channel.Attachment

	for i, att := range msg.Attachments {
		if !drop[i] {
			atts = append(atts, att)
		}
	}

	msg.Text, msg.Attachments = strings.TrimSpace(text.String()), atts

	return msg, hasLetter(msg.Text) || len(atts) > 0
}
