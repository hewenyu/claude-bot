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

// Threads a new message may continue: the conversation's pending tasks and
// those handled in the last reopenWindow, at most maxThreads, latest first;
// Jev reads the last transcriptRunes of each.
const (
	reopenWindow    = 24 * time.Hour
	maxThreads      = 10
	transcriptRunes = 800
)

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

	var threadErr error

	tasks, err := screen.Screen(ctx, ask, items, func(m channel.Message) []screen.Thread {
		th, err := a.threads(m)
		if err != nil {
			threadErr = err
		}

		return th
	})
	if err == nil {
		err = threadErr
	}

	if err != nil {
		return err
	}

	byID := make(map[string]channel.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}

	// Store the tasks and messages before replying: if this fails nothing
	// was sent, and once it succeeds the refused pieces are gone for good.
	if err := a.inbox.Locked(func() error { return a.commit(items, tasks, byID) }); err != nil {
		return err
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

// threads returns the tasks m's conversation may continue.
func (a *app) threads(m channel.Message) ([]screen.Thread, error) {
	tasks, err := a.inbox.RecentTasks(m.Channel, m.ChatID, m.ThreadID, time.Now().Add(-reopenWindow))
	if err != nil {
		return nil, err
	}

	out := make([]screen.Thread, 0, min(len(tasks), maxThreads))
	for _, t := range tasks[:min(len(tasks), maxThreads)] {
		var transcript, asked []string

		for _, e := range t.Entries {
			if e.Kind == inbox.EntryBot {
				transcript = append(transcript, "bot: "+e.Text)

				continue
			}

			who := "user"
			if e.Sender != nil && e.Sender.Name != "" {
				who = e.Sender.Name
			}

			transcript = append(transcript, who+": "+e.Text)
			asked = append(asked, e.Text)
		}

		out = append(out, screen.Thread{
			ID:         t.ID,
			Transcript: lastRunes(strings.Join(transcript, "\n"), transcriptRunes),
			Asked:      lastRunes(strings.Join(asked, "\n"), transcriptRunes),
		})
	}

	return out, nil
}

// lastRunes keeps the end of s, the latest of a conversation.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return "…" + string(r[len(r)-n:])
}

// commit stores what screening found, inside the inbox lock: each legal
// task's pieces go into its inbox task, new or continued, which is then
// pending; each message is archived without its refused pieces and labelled
// with the tasks its pieces went to; refused files are deleted.
func (a *app) commit(items []screen.Item, tasks []screen.Task, byID map[string]channel.Message) error {
	labels := map[string][]channel.Piece{}
	refused := map[string][]screen.Piece{}

	for _, t := range tasks {
		if t.Refused() {
			for _, p := range t.Pieces {
				refused[p.Message] = append(refused[p.Message], p)
			}

			continue
		}

		task, err := a.taskFor(t, byID)
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

		if err := a.inbox.SaveTask(task, true); err != nil {
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
func (a *app) taskFor(t screen.Task, byID map[string]channel.Message) (inbox.Task, error) {
	if t.Thread != "" {
		task, _, err := a.inbox.Task(t.Thread)
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

// entriesOf turns the pieces of m into task entries; a message with no
// pieces (a sticker) is one empty entry.
func entriesOf(m channel.Message, pieces []screen.Piece) []inbox.Entry {
	sender := m.Sender

	var entries []inbox.Entry

	for _, p := range pieces {
		if p.Message != m.ID {
			continue
		}

		e := inbox.Entry{Kind: inbox.EntryUser, Message: m.ID, Sender: &sender, ReplyTo: m.ReplyTo, At: m.CreatedAt, Text: p.Text, File: p.File}
		if p.Attachment >= 0 {
			att := m.Attachments[p.Attachment]
			att.Path = p.File
			e.Text = fileNote(m.ID, att)
		}

		entries = append(entries, e)
	}

	if len(entries) == 0 {
		entries = append(entries, inbox.Entry{Kind: inbox.EntryUser, Message: m.ID, Sender: &sender, ReplyTo: m.ReplyTo, At: m.CreatedAt})
	}

	return entries
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
