package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/screen"
	"github.com/hewenyu/claude-bot/internal/transport"
)

// refusal is the whole reply to a task Jev finds illegal.
const refusal = "现在暂时不支持。"

// refusedLog records refused tasks for people to review; Claude never sees them.
const refusedLog = "refused.jsonl"

// errNoJev tells Claude what to ask of the user: every message goes through
// Jev, so nothing works without it.
var errNoJev = errors.New("jev is not set up: run `claude-bot init --typesafe-key <key>` " +
	"(key from https://console.typesafe.ai/keys), then `claude-bot restart`")

// The listener screens a burst of messages together, so that messages sent
// one after another are grouped: it waits until the conversation pauses for
// screenQuiet, but no longer than screenMax.
const (
	screenQuiet = 3 * time.Second
	screenMax   = 15 * time.Second
)

// screener screens incoming messages each time wake fires, after the burst
// settles. When Jev fails, the messages stay incoming and it retries:
// nothing unscreened reaches Claude.
func (a *app) screener(ctx context.Context, ask screen.Asker, wake <-chan struct{}) {
	backoff := transport.Backoff{Start: 5 * time.Second, Cap: 5 * time.Minute}

	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}

		settle(ctx, wake)

		for {
			err := a.screenIncoming(ctx, ask)
			if err == nil {
				backoff.Reset()

				break
			}

			a.log.Error("screen messages; they stay incoming", "err", err)

			if !transport.Sleep(ctx, backoff.Next()) {
				return
			}
		}
	}
}

// settle returns once wake has been quiet for screenQuiet, or screenMax
// after it was called.
func settle(ctx context.Context, wake <-chan struct{}) {
	quiet := time.NewTimer(screenQuiet)
	defer quiet.Stop()

	deadline := time.NewTimer(screenMax)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-quiet.C:
			return
		case <-deadline.C:
			return
		case <-wake:
			quiet.Reset(screenQuiet)
		}
	}
}

// screenIncoming has Jev group the incoming messages into tasks and refuse
// the illegal ones. Each message moves to pending with what is left of it,
// labelled by task and with its files saved; a message with nothing left
// goes to done. Then the senders of refused tasks are told it is not
// supported.
func (a *app) screenIncoming(ctx context.Context, ask screen.Asker) error {
	msgs, err := a.inbox.Incoming()
	if err != nil || len(msgs) == 0 {
		return err
	}

	items := make([]screen.Item, len(msgs))
	for i, m := range msgs {
		items[i] = screen.Item{Msg: m, Files: a.fetchAttachments(ctx, m)}
	}

	tasks, err := screen.Screen(ctx, ask, items)
	if err != nil {
		return err
	}

	byID := make(map[string]channel.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}

	labelled, refused := label(tasks, byID)

	// Move the messages before replying: if this fails nothing was sent, and
	// once it succeeds the refused pieces are gone for good.
	for _, it := range items {
		if err := a.settleMessage(it, labelled[it.Msg.ID], refused[it.Msg.ID]); err != nil {
			return err
		}
	}

	for _, t := range tasks {
		if !t.Refused() {
			continue
		}

		a.logRefusal(t, byID)

		last := byID[t.Messages[len(t.Messages)-1]]
		if err := a.channels[last.Channel].Reply(ctx, last, refusal); err != nil {
			a.log.Warn("send refusal", "id", last.ID, "err", err)
		}

		a.log.Info("refused", "messages", t.Messages)
	}

	return nil
}

// label names every legal task and returns, by message, its labelled
// pieces and its refused ones. A task is named after its first message and
// how many tasks start there: "lark:om_1/2". A file's piece says where the
// file was saved.
func label(tasks []screen.Task, byID map[string]channel.Message) (map[string][]channel.Piece, map[string][]screen.Piece) {
	labelled := map[string][]channel.Piece{}
	refused := map[string][]screen.Piece{}
	started := map[string]int{}

	for _, t := range tasks {
		if t.Refused() {
			for _, p := range t.Pieces {
				refused[p.Message] = append(refused[p.Message], p)
			}

			continue
		}

		first := t.Messages[0]
		started[first]++
		id := fmt.Sprintf("%s/%d", first, started[first])

		if len(t.Pieces) == 0 {
			labelled[first] = append(labelled[first], channel.Piece{Task: id})
		}

		for _, p := range t.Pieces {
			text := p.Text
			if p.Attachment >= 0 {
				att := byID[p.Message].Attachments[p.Attachment]
				att.Path = p.File
				text = fileNote(p.Message, att)
			}

			labelled[p.Message] = append(labelled[p.Message], channel.Piece{Task: id, Text: text, File: p.File})
		}
	}

	return labelled, refused
}

// settleMessage records where each attachment was saved and the message's
// pieces, crops what was refused, and moves the message on.
func (a *app) settleMessage(it screen.Item, pieces []channel.Piece, refused []screen.Piece) error {
	m := it.Msg

	m.Attachments = append([]channel.Attachment(nil), m.Attachments...)
	for i := range m.Attachments {
		if i < len(it.Files) {
			m.Attachments[i].Path = it.Files[i]
		}
	}

	for _, p := range refused {
		if p.File != "" {
			_ = os.Remove(p.File)
		}
	}

	cropped, left := screen.Crop(m, refused)
	cropped.Pieces = pieces

	if len(refused) > 0 && !left {
		cropped.Pieces = nil

		return a.inbox.Reject(cropped)
	}

	return a.inbox.Admit(cropped)
}

// fileNote describes an uploaded file and where the listener saved it.
func fileNote(id string, att channel.Attachment) string {
	name := att.Name
	if name == "" {
		name = filepath.Base(att.Path)
	}

	if att.Path == "" {
		return fmt.Sprintf("用户上传的文件 %s 下载失败，可以用 claude-bot download %s 重试", name, id)
	}

	return fmt.Sprintf("用户上传的文件 %s，已保存在 %s", name, att.Path)
}

// fetchAttachments downloads msg's attachments, as `download` does. A failed
// download leaves "" in its place.
func (a *app) fetchAttachments(ctx context.Context, msg channel.Message) []string {
	if len(msg.Attachments) == 0 {
		return nil
	}

	paths := make([]string, len(msg.Attachments))
	dir := a.attachmentDir(msg)

	for i, att := range msg.Attachments {
		path, err := a.channels[msg.Channel].Download(ctx, msg, att, dir)
		if err != nil {
			a.log.Warn("download attachment", "id", msg.ID, "err", err)

			continue
		}

		paths[i] = path
	}

	return paths
}

// attachmentDir is where a message's attachments are saved: one directory
// per message, since attachments of different messages often share a name
// (image.png, crash.log).
func (a *app) attachmentDir(msg channel.Message) string {
	return filepath.Join(a.path(filesDir), channel.PathSafe(msg.ID))
}

// messageRef is a message without its content; a task's pieces carry that.
type messageRef struct {
	ID        string           `json:"id"`
	ChatID    string           `json:"chat_id"`
	ChatType  channel.ChatType `json:"chat_type"`
	ThreadID  string           `json:"thread_id,omitempty"`
	ReplyTo   string           `json:"reply_to,omitempty"`
	Sender    channel.User     `json:"sender"`
	CreatedAt time.Time        `json:"created_at"`
}

func refOf(m channel.Message) messageRef {
	return messageRef{ID: m.ID, ChatID: m.ChatID, ChatType: m.ChatType, ThreadID: m.ThreadID, ReplyTo: m.ReplyTo, Sender: m.Sender, CreatedAt: m.CreatedAt}
}

// refusalRecord is one line of refused.jsonl.
type refusalRecord struct {
	At       time.Time          `json:"at"`
	Messages []messageRef       `json:"messages"`
	Pieces   []screen.Piece     `json:"pieces"`
	Scores   map[string]float64 `json:"scores"`
}

// logRefusal appends a refused task to refused.jsonl, best effort.
func (a *app) logRefusal(t screen.Task, byID map[string]channel.Message) {
	rec := refusalRecord{At: time.Now(), Pieces: t.Pieces, Scores: t.Scores}
	for _, id := range t.Messages {
		rec.Messages = append(rec.Messages, refOf(byID[id]))
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return
	}

	f, err := os.OpenFile(a.path(refusedLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, privatePerm)
	if err != nil {
		a.log.Warn("log refusal", "err", err)

		return
	}
	defer f.Close() //nolint:errcheck // best effort

	_, _ = f.Write(append(line, '\n'))
}

// taskLine is one line of `inbox` output: one task, made of pieces of one
// or more pending messages.
type taskLine struct {
	Task     string       `json:"task"`
	Messages []messageRef `json:"messages"`
	Pieces   []taskPiece  `json:"pieces"`
}

type taskPiece struct {
	Message string `json:"message"`
	Text    string `json:"text,omitempty"`
	File    string `json:"file,omitempty"`
}

// tasksOf gathers the pieces of pending messages into tasks, in the order
// they first appear. A message from before screening is a task of its own.
func tasksOf(msgs []channel.Message) []taskLine {
	var (
		lines []taskLine
		index = map[string]int{}
	)

	for _, m := range msgs {
		pieces := m.Pieces
		if len(pieces) == 0 {
			pieces = []channel.Piece{{Task: m.ID, Text: m.Text}}
		}

		for _, p := range pieces {
			i, ok := index[p.Task]
			if !ok {
				i = len(lines)
				index[p.Task] = i

				lines = append(lines, taskLine{Task: p.Task})
			}

			t := &lines[i]
			if len(t.Messages) == 0 || t.Messages[len(t.Messages)-1].ID != m.ID {
				t.Messages = append(t.Messages, refOf(m))
			}

			if p.Text != "" || p.File != "" {
				t.Pieces = append(t.Pieces, taskPiece{Message: m.ID, Text: p.Text, File: p.File})
			}
		}
	}

	return lines
}
