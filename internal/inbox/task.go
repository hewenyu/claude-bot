package inbox

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/filelock"
)

// Task states, each a subdirectory of tasks/.
const (
	tasksDir    = "tasks"
	taskPending = "pending"
	taskDone    = "done"
	tasksLock   = "tasks.lock"
)

// TaskPrefix starts every task id, which tells it from a message id.
const TaskPrefix = "task:"

// EntryKind is who an entry of a task's conversation is from.
type EntryKind string

// Entry kinds.
const (
	EntryUser EntryKind = "user"
	EntryBot  EntryKind = "bot"
)

// Entry is one turn of a task's conversation: a piece of what a user said
// or sent, or one reply of the bot.
type Entry struct {
	Kind    EntryKind     `json:"kind"`               // EntryUser or EntryBot
	Message string        `json:"message,omitempty"`  // user entries: the message this is part of
	Sender  *channel.User `json:"sender,omitempty"`   // user entries
	ReplyTo string        `json:"reply_to,omitempty"` // user entries: what the message replies to
	At      time.Time     `json:"at"`
	Text    string        `json:"text,omitempty"`
	File    string        `json:"file,omitempty"` // a saved attachment; Text says what it is
}

// Task is one thing people asked for, with its whole conversation. New
// messages about it are appended, even from other people in a group and
// after it was handled (it reopens). Entries before Handled were handled.
type Task struct {
	ID        string           `json:"id"`
	Channel   string           `json:"channel"`
	ChatID    string           `json:"chat_id"`
	ChatType  channel.ChatType `json:"chat_type"`
	ThreadID  string           `json:"thread_id,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"` // of the latest user entry
	Entries   []Entry          `json:"entries"`
	Handled   int              `json:"handled"`
	// Shown is how many entries the task had when `inbox` last showed it:
	// closing it handles those, not what arrived while Claude worked.
	Shown int `json:"shown,omitempty"`
}

var taskSeq atomic.Int64

// NewTaskID returns an id no other task has: the time keeps ids apart
// across restarts, the sequence, which does not wrap, within one
// millisecond.
func NewTaskID(now time.Time) string {
	return TaskPrefix + strconv.FormatInt(now.UnixMilli(), 36) + strconv.FormatInt(taskSeq.Add(1), 36)
}

// Append adds user entries for one message, replacing any it already had
// (a message screened again after a crash), and reopens the task.
func (t *Task) Append(msg string, entries []Entry) {
	kept := t.Entries[:0:0]

	for i, e := range t.Entries {
		if e.Kind == EntryUser && e.Message == msg {
			if i < t.Handled {
				t.Handled--
			}

			if i < t.Shown {
				t.Shown--
			}

			continue
		}

		kept = append(kept, e)
	}

	kept = append(kept, entries...)
	t.Entries = kept

	for _, e := range entries {
		if e.At.After(t.UpdatedAt) {
			t.UpdatedAt = e.At
		}
	}
}

// Close marks handled what `inbox` last showed. It reports whether the
// task is done: not if user entries arrived after that.
func (t *Task) Close() bool {
	upto := t.Shown
	if upto <= 0 || upto > len(t.Entries) {
		upto = len(t.Entries)
	}

	t.Shown = 0

	for _, e := range t.Entries[upto:] {
		if e.Kind == EntryUser {
			t.Handled = upto

			return false
		}
	}

	t.Handled = len(t.Entries)

	return true
}

// ClosedAt marks the task handled because its chat was closed on the
// platform at at. It reports whether the task is done: not if someone wrote
// after the closing, which still waits for an answer.
func (t *Task) ClosedAt(at time.Time) bool {
	for _, e := range t.Entries[min(t.Handled, len(t.Entries)):] {
		if e.Kind == EntryUser && e.At.After(at) {
			return false
		}
	}

	t.Handled, t.Shown = len(t.Entries), 0

	return true
}

// LastUserMessage is the id of the latest message in the task, the one a
// reply answers.
func (t Task) LastUserMessage() string {
	for _, e := range slices.Backward(t.Entries) {
		if e.Kind == EntryUser {
			return e.Message
		}
	}

	return ""
}

// ErrNoTask reports a task id that is not in the inbox.
var ErrNoTask = errors.New("task not in inbox")

func (b *Inbox) taskPath(state, id string) string {
	return filepath.Join(b.dir, tasksDir, state, channel.PathSafe(id)+".json")
}

// Locked runs f holding the inbox's task lock, shared by the listener and
// every command, so that read-modify-write of tasks never loses an update.
func (b *Inbox) Locked(f func() error) error {
	if err := os.MkdirAll(b.dir, dirPerm); err != nil {
		return fmt.Errorf("create inbox: %w", err)
	}

	unlock, err := filelock.Lock(filepath.Join(b.dir, tasksLock), messagePerm)
	if err != nil {
		return err
	}
	defer unlock()

	return f()
}

// SaveTask stores t as pending or done. Call it inside Locked.
func (b *Inbox) SaveTask(t Task, pending bool) error {
	state, other := taskDone, taskPending
	if pending {
		state, other = taskPending, taskDone
	}

	if err := os.MkdirAll(filepath.Join(b.dir, tasksDir, state), dirPerm); err != nil {
		return fmt.Errorf("create inbox: %w", err)
	}

	if err := atomicfile.WriteJSON(b.taskPath(state, t.ID), t, messagePerm); err != nil {
		return fmt.Errorf("store task: %w", err)
	}

	if err := os.Remove(b.taskPath(other, t.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("move task: %w", err)
	}

	return nil
}

// Task returns a task by id and whether it is pending.
func (b *Inbox) Task(id string) (Task, bool, error) {
	for _, pending := range []bool{true, false} {
		state := taskDone
		if pending {
			state = taskPending
		}

		t, err := readJSON[Task](b.taskPath(state, id), "task")
		if err == nil {
			return t, pending, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return t, false, err
		}
	}

	return Task{}, false, fmt.Errorf("%w: %s", ErrNoTask, id)
}

// PendingTasks returns the tasks waiting for Claude, the longest waiting
// first.
func (b *Inbox) PendingTasks() ([]Task, error) {
	tasks, err := b.tasks(taskPending)

	slices.SortFunc(tasks, func(x, y Task) int { return waitingSince(x).Compare(waitingSince(y)) })

	return tasks, err
}

// DoneTasks returns the tasks that were handled, in no particular order.
func (b *Inbox) DoneTasks() ([]Task, error) {
	return b.tasks(taskDone)
}

// waitingSince is when the first unhandled user entry arrived.
func waitingSince(t Task) time.Time {
	for _, e := range t.Entries[min(t.Handled, len(t.Entries)):] {
		if e.Kind == EntryUser {
			return e.At
		}
	}

	return t.UpdatedAt
}

// RecentTasks returns the tasks of a conversation that are pending or were
// updated after since, the latest first.
func (b *Inbox) RecentTasks(ch, chatID, threadID string, since time.Time) ([]Task, error) {
	var out []Task

	for _, state := range []string{taskPending, taskDone} {
		tasks, err := b.tasks(state)
		if err != nil {
			return nil, err
		}

		for _, t := range tasks {
			if t.Channel == ch && t.ChatID == chatID && t.ThreadID == threadID && (state == taskPending || t.UpdatedAt.After(since)) {
				out = append(out, t)
			}
		}
	}

	slices.SortFunc(out, func(x, y Task) int { return y.UpdatedAt.Compare(x.UpdatedAt) })

	return out, nil
}

// WaitTasks returns pending tasks, polling until at least one exists or ctx
// ends.
func (b *Inbox) WaitTasks(ctx context.Context, every time.Duration) ([]Task, error) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		tasks, err := b.PendingTasks()
		if err != nil || len(tasks) > 0 {
			return tasks, err
		}

		select {
		case <-ctx.Done():
			return nil, nil // timing out with nothing to do is the normal outcome
		case <-t.C:
		}
	}
}

func (b *Inbox) tasks(state string) ([]Task, error) {
	return readAll[Task](filepath.Join(b.dir, tasksDir, state), "task")
}
