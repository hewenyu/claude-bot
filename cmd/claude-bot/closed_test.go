package main

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// closingChannel is a fakeChannel whose chats closed at the given times.
type closingChannel struct {
	fakeChannel

	closed map[string]time.Time
}

func (c *closingChannel) Name() string { return "gh" }

func (c *closingChannel) ClosedAt(_ context.Context, chatID string) (time.Time, error) {
	return c.closed[chatID], nil
}

func TestSettleClosed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	gh := &closingChannel{closed: map[string]time.Time{"o/r/1": now.Add(time.Minute), "o/r/2": now.Add(time.Minute)}}
	a := assemble(t.TempDir(), slog.New(slog.DiscardHandler), gh, &fakeChannel{})

	save := func(id, ch, chat string, at time.Time) {
		tk := inbox.Task{ID: id, Channel: ch, ChatID: chat, CreatedAt: at}
		tk.Append(id+"/m", []inbox.Entry{{Kind: inbox.EntryUser, Message: id + "/m", At: at, Text: "?"}})

		if err := a.inbox.Locked(func(tx *inbox.Tx) error { return tx.SaveTask(tk, true) }); err != nil {
			t.Fatal(err)
		}
	}

	save("task:closed", "gh", "o/r/1", now)                   // closed after it was asked
	save("task:after", "gh", "o/r/2", now.Add(2*time.Minute)) // asked again after the closing
	save("task:open", "gh", "o/r/3", now)                     // still open
	save("task:other", "fake", "o/r/1", now)                  // a channel that cannot close chats

	tasks, err := a.inbox.PendingTasks()
	if err != nil {
		t.Fatal(err)
	}

	// Listed pending, then gone (handled, say) before settleClosed got to it.
	tasks = append(tasks, inbox.Task{ID: "task:gone", Channel: "gh", ChatID: "o/r/1"})

	left := ids(a.settleClosed(t.Context(), tasks))

	pending, err := a.inbox.PendingTasks()
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"task:after", "task:open", "task:other"}
	if still := ids(pending); !slices.Equal(left, want) || !slices.Equal(still, want) {
		t.Errorf("returned %v, pending %v; want %v", left, still, want)
	}

	if tk, pending, err := a.inbox.Task("task:closed"); err != nil || pending || tk.Handled != len(tk.Entries) {
		t.Errorf("task:closed = %+v, pending %v, %v; want done, all handled", tk, pending, err)
	}
}

var _ channel.Closable = (*closingChannel)(nil)

// ids returns the tasks' ids, sorted.
func ids(tasks []inbox.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, tk := range tasks {
		out = append(out, tk.ID)
	}

	slices.Sort(out)

	return out
}
