package inbox

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

func msg(id string, at time.Time) channel.Message {
	return channel.Message{ID: id, Channel: "lark", ChatID: "oc_1", ChatType: "p2p", Text: "hi " + id, CreatedAt: at}
}

func TestInbox_HoldArchive(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

	for _, m := range []channel.Message{msg("lark:om_b", base.Add(time.Minute)), msg("lark:om_a", base)} {
		if fresh, err := b.Hold(m); !fresh || err != nil {
			t.Fatalf("Hold(%s) = %v, %v", m.ID, fresh, err)
		}
	}

	incoming, err := b.Incoming()
	if err != nil || len(incoming) != 2 || incoming[0].ID != "lark:om_a" {
		t.Fatalf("Incoming = %v, %v; want om_a then om_b", incoming, err)
	}

	screened := incoming[0]
	screened.Text = "screened"

	if err := b.Archive(screened); err != nil {
		t.Fatal(err)
	}

	if got, err := b.Find("lark:om_a"); err != nil || got.Text != "screened" {
		t.Errorf("Find = %+v, %v; want the archived copy", got, err)
	}

	if incoming, _ := b.Incoming(); len(incoming) != 1 {
		t.Errorf("incoming after Archive = %v", incoming)
	}

	if _, err := b.Find("lark:nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find missing = %v, want ErrNotFound", err)
	}
}

func TestInbox_Hold_redelivery(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	held, archived := msg("lark:om_a", time.Now()), msg("lark:om_b", time.Now())

	for _, m := range []channel.Message{held, archived} {
		if _, err := b.Hold(m); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Archive(archived); err != nil {
		t.Fatal(err)
	}

	for _, m := range []channel.Message{held, archived} {
		if fresh, _ := b.Hold(m); fresh {
			t.Errorf("re-delivered %s held again", m.ID)
		}
	}
}

func TestInbox_Hold_concurrentRedelivery(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	m := msg("lark:om_1", time.Now())

	var (
		fresh atomic.Int32
		wg    sync.WaitGroup
	)

	for range 8 {
		wg.Go(func() {
			ok, err := b.Hold(m)
			if err != nil {
				t.Error(err)
			}

			if ok {
				fresh.Add(1)
			}
		})
	}

	wg.Wait()

	if n := fresh.Load(); n != 1 {
		t.Errorf("%d concurrent deliveries reported fresh, want exactly 1", n)
	}
}

func TestInbox_Migrate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := New(dir)

	// A message left pending by a version before tasks.
	if err := os.MkdirAll(filepath.Join(dir, statePending), dirPerm); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(b.path(statePending, "lark:om_old"), []byte(`{"id":"lark:om_old","text":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if n, err := b.Migrate(); n != 1 || err != nil {
		t.Fatalf("Migrate = %d, %v", n, err)
	}

	if incoming, _ := b.Incoming(); len(incoming) != 1 || incoming[0].Text != "old" {
		t.Errorf("incoming after Migrate = %v; want the old message, to be screened", incoming)
	}
}

func task(id string, at time.Time, texts ...string) Task {
	t := Task{ID: id, Channel: "lark", ChatID: "oc_1", CreatedAt: at}
	for i, text := range texts {
		t.Append("lark:om_"+text, []Entry{{Kind: EntryUser, Message: "lark:om_" + text, At: at.Add(time.Duration(i) * time.Second), Text: text}})
	}

	return t
}

func TestTask_CloseKeepsWhatArrivedMeanwhile(t *testing.T) {
	t.Parallel()

	tk := task("task:1", time.Now(), "a")
	tk.Shown = len(tk.Entries) // inbox showed it

	// A follow-up arrives while Claude works on it, then Claude replies.
	tk.Append("lark:om_b", []Entry{{Kind: EntryUser, Message: "lark:om_b", At: time.Now(), Text: "b"}})
	tk.Entries = append(tk.Entries, Entry{Kind: EntryBot, At: time.Now(), Text: "fixed a"})

	if tk.Close() {
		t.Fatal("Close finished a task with an unseen follow-up")
	}

	if tk.Handled != 1 || tk.Shown != 0 {
		t.Errorf("Handled = %d, Shown = %d; want 1 (only a), 0", tk.Handled, tk.Shown)
	}

	tk.Shown = len(tk.Entries)
	if !tk.Close() || tk.Handled != len(tk.Entries) {
		t.Errorf("second Close = Handled %d of %d; want all handled", tk.Handled, len(tk.Entries))
	}
}

func TestTask_AppendIsIdempotent(t *testing.T) {
	t.Parallel()

	tk := task("task:1", time.Now(), "a", "b")
	tk.Handled, tk.Shown = 2, 2

	// om_b screened again after a crash: its entries are replaced, not doubled.
	tk.Append("lark:om_b", []Entry{{Kind: EntryUser, Message: "lark:om_b", At: time.Now(), Text: "b"}})

	if len(tk.Entries) != 2 || tk.Handled != 1 || tk.Shown != 1 {
		t.Errorf("entries %d, handled %d, shown %d; want 2, 1, 1", len(tk.Entries), tk.Handled, tk.Shown)
	}
}

func TestInbox_Tasks(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	now := time.Now()
	old, recent, other := task("task:old", now.Add(-48*time.Hour), "o"), task("task:recent", now.Add(-time.Hour), "r"), task("task:other", now, "x")
	other.ChatID = "oc_2"

	err := b.Locked(func() error {
		for _, tk := range []Task{old, recent} {
			if err := b.SaveTask(tk, false); err != nil {
				return err
			}
		}

		return b.SaveTask(other, true)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := b.RecentTasks("lark", "oc_1", "", now.Add(-24*time.Hour))
	if err != nil || len(got) != 1 || got[0].ID != "task:recent" {
		t.Errorf("RecentTasks = %v, %v; want only task:recent", got, err)
	}

	// Reopening moves it to pending.
	if err := b.Locked(func() error { return b.SaveTask(recent, true) }); err != nil {
		t.Fatal(err)
	}

	pending, _ := b.PendingTasks()
	if len(pending) != 2 || pending[0].ID != "task:recent" {
		t.Errorf("PendingTasks = %v; want task:recent (waiting longest), task:other", pending)
	}

	if _, isPending, err := b.Task("task:recent"); !isPending || err != nil {
		t.Errorf("Task(task:recent) pending = %v, %v", isPending, err)
	}

	if _, _, err := b.Task("task:nope"); !errors.Is(err, ErrNoTask) {
		t.Errorf("Task missing = %v, want ErrNoTask", err)
	}
}

// TestInbox_LockedSerializes runs the listener's append and Claude's close
// concurrently: under the lock neither update is lost.
func TestInbox_LockedSerializes(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	if err := b.Locked(func() error { return b.SaveTask(task("task:1", time.Now(), "a"), true) }); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	const n = 20
	for i := range n {
		wg.Go(func() {
			err := b.Locked(func() error {
				tk, _, err := b.Task("task:1")
				if err != nil {
					return err
				}

				id := "lark:om_" + string(rune('A'+i))
				tk.Append(id, []Entry{{Kind: EntryUser, Message: id, At: time.Now()}})

				return b.SaveTask(tk, true)
			})
			if err != nil {
				t.Error(err)
			}
		})
	}

	wg.Wait()

	tk, _, err := b.Task("task:1")
	if err != nil || len(tk.Entries) != n+1 {
		t.Errorf("entries = %d, %v; want %d, none lost", len(tk.Entries), err, n+1)
	}
}
