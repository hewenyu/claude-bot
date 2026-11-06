package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
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
	"(key from " + jevGuide + "), then `claude-bot restart`")

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

// screenIncoming has Jev screen the incoming messages: their pieces go into
// tasks, new ones or ones they continue (reopening those already handled),
// and whatever Jev refuses goes nowhere. The messages are archived, cropped
// and labelled; then the senders of refused tasks are told it is not
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

	tasks, err := screen.Screen(ctx, ask, items, a.threads)
	if err != nil {
		return err
	}

	byID := make(map[string]channel.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}

	// Store the tasks and messages before replying: if this fails nothing
	// was sent, and once it succeeds the refused pieces are gone for good.
	if err := a.inbox.Locked(func(tx *inbox.Tx) error { return a.commit(tx, items, tasks, byID) }); err != nil {
		return err
	}

	// The refusals are owed now that the pieces are gone: shutting down
	// must not cut them off.
	sendCtx := context.WithoutCancel(ctx)

	for _, t := range tasks {
		if !t.Refused() {
			continue
		}

		a.logRefusal(t, byID)

		last := byID[t.Messages[len(t.Messages)-1]]
		if ch, ok := a.channels[last.Channel]; !ok {
			a.log.Warn("send refusal", "id", last.ID, "err", "channel not configured")
		} else if err := replyWithin(sendCtx, ch, last, refusal); err != nil {
			a.log.Warn("send refusal", "id", last.ID, "err", err)
		}

		a.log.Info("refused", "messages", t.Messages)
	}

	return nil
}

// refusalTimeout bounds sending one refusal.
const refusalTimeout = 10 * time.Second

// replyWithin replies to m, giving up after refusalTimeout.
func replyWithin(ctx context.Context, ch channel.Channel, m channel.Message, text string) error {
	ctx, cancel := context.WithTimeout(ctx, refusalTimeout)
	defer cancel()

	return ch.Reply(ctx, m, text)
}

// threads returns the tasks m's conversation may continue: its pending
// tasks and those handled lately, latest first.
func (a *app) threads(m channel.Message) ([]screen.Thread, error) {
	tasks, err := a.inbox.RecentTasks(m.Channel, m.ChatID, m.ThreadID, time.Now().Add(-screen.ReopenWindow))
	if err != nil {
		return nil, err
	}

	return screen.Threads(tasks), nil
}

// commit stores what screening found, inside the inbox lock: each legal
// task's pieces go into its inbox task, new or continued, which is then
// pending; each message is archived without its refused pieces and labelled
// with the tasks its pieces went to; refused files are deleted.
func (a *app) commit(tx *inbox.Tx, items []screen.Item, tasks []screen.Task, byID map[string]channel.Message) error {
	labels := map[string][]channel.Piece{}
	refused := map[string][]screen.Piece{}

	for _, t := range tasks {
		if t.Refused() {
			for _, p := range t.Pieces {
				refused[p.Message] = append(refused[p.Message], p)
			}

			continue
		}

		task, err := taskFor(tx, t, byID)
		if err != nil {
			return err
		}

		for _, id := range t.Messages {
			m := byID[id]
			entries := entriesOf(m, t.Pieces)
			task.Append(id, entries)

			for _, e := range entries {
				labels[id] = append(labels[id], channel.Piece{Task: task.ID, Text: e.Text, File: e.File})
			}
		}

		if err := tx.SaveTask(task, true); err != nil {
			return err
		}
	}

	return a.archive(items, labels, refused)
}

// archive stores the screened messages: where their files were saved, the
// tasks their pieces went to, and without what was refused, whose files it
// deletes.
func (a *app) archive(items []screen.Item, labels map[string][]channel.Piece, refused map[string][]screen.Piece) error {
	for _, it := range items {
		m := it.Msg

		m.Attachments = append([]channel.Attachment(nil), m.Attachments...)
		for i := range m.Attachments {
			if i < len(it.Files) {
				m.Attachments[i].Path = it.Files[i]
			}
		}

		for _, p := range refused[m.ID] {
			if p.File != "" {
				_ = os.Remove(p.File)
			}
		}

		cropped, _ := screen.Crop(m, refused[m.ID])
		cropped.Pieces = labels[m.ID]

		if err := a.inbox.Archive(cropped); err != nil {
			return err
		}
	}

	return nil
}

// taskFor returns the inbox task t continues, or a new one.
func taskFor(tx *inbox.Tx, t screen.Task, byID map[string]channel.Message) (inbox.Task, error) {
	if t.Thread != "" {
		task, _, err := tx.Task(t.Thread)
		if err == nil {
			return task, nil
		}

		if !errors.Is(err, inbox.ErrNoTask) {
			return task, err
		}
	}

	first := byID[t.Messages[0]]

	return inbox.Task{
		ID: inbox.NewTaskID(time.Now()), Channel: first.Channel, ChatID: first.ChatID,
		ChatType: first.ChatType, ThreadID: first.ThreadID, CreatedAt: first.CreatedAt,
	}, nil
}

// entriesOf turns m into task entries: its text, whole and as written (a
// message is never split between tasks), then each of its files. A message
// with neither (a sticker) is one empty entry.
func entriesOf(m channel.Message, pieces []screen.Piece) []inbox.Entry {
	sender := m.Sender
	entry := inbox.Entry{Kind: inbox.EntryUser, Message: m.ID, Sender: &sender, ReplyTo: m.ReplyTo, At: m.CreatedAt}

	var (
		files []inbox.Entry
		text  bool
	)

	for _, p := range pieces {
		switch {
		case p.Message != m.ID:
		case p.Attachment < 0:
			text = true
		default:
			att := m.Attachments[p.Attachment]
			att.Path = p.File

			e := entry
			e.Text, e.File = fileNote(m.ID, att), p.File
			files = append(files, e)
		}
	}

	if !text && len(files) > 0 {
		return files
	}

	if text {
		entry.Text = strings.TrimSpace(m.Text)
	}

	return append([]inbox.Entry{entry}, files...)
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
// download leaves "" in its place, as does a message from a channel no
// longer configured (left incoming by an earlier run).
func (a *app) fetchAttachments(ctx context.Context, msg channel.Message) []string {
	if len(msg.Attachments) == 0 {
		return nil
	}

	paths := make([]string, len(msg.Attachments))

	ch, ok := a.channels[msg.Channel]
	if !ok {
		a.log.Warn("download attachment", "id", msg.ID, "err", "channel not configured")

		return paths
	}

	dir := a.attachmentDir(msg)

	for i, att := range msg.Attachments {
		path, err := ch.Download(ctx, msg, att, dir)
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
	rec := refusalRecord{At: time.Now().UTC(), Pieces: t.Pieces, Scores: t.Scores}
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

// taskLine is one line of `inbox` output: a task with its whole
// conversation. New marks the user entries not handled yet.
type taskLine struct {
	inbox.Task

	Entries []entryLine `json:"entries"`
}

type entryLine struct {
	inbox.Entry

	New bool `json:"new,omitempty"`
}

func lineOf(t inbox.Task) taskLine {
	l := taskLine{Task: t, Entries: make([]entryLine, len(t.Entries))}
	for i, e := range t.Entries {
		l.Entries[i] = entryLine{Entry: e, New: e.Kind == inbox.EntryUser && i >= t.Handled}
	}

	return l
}
