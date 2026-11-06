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

func TestTask_ClosedAtKeepsWhatCameAfter(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tk := task("task:1", now, "a")
	tk.Shown = 1 // Claude has it

	if !tk.ClosedAt(now.Add(time.Minute)) || tk.Handled != 1 || tk.Shown != 0 {
		t.Errorf("closed after it: done = false, or Handled %d, Shown %d; want done, 1, 0", tk.Handled, tk.Shown)
	}

	// Someone writes after the closing: that waits for an answer.
	tk.Append("lark:om_b", []Entry{{Kind: EntryUser, Message: "lark:om_b", At: now.Add(2 * time.Minute), Text: "b"}})

	if tk.ClosedAt(now.Add(time.Minute)) || tk.Handled != 1 {
		t.Errorf("closed before the follow-up: done, or Handled %d; want pending, 1", tk.Handled)
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

	err := b.Locked(func(tx *Tx) error {
		for _, tk := range []Task{old, recent} {
			if err := tx.SaveTask(tk, false); err != nil {
				return err
			}
		}

		return tx.SaveTask(other, true)
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := b.RecentTasks("lark", "oc_1", "", now.Add(-24*time.Hour))
	if err != nil || len(got) != 1 || got[0].ID != "task:recent" {
		t.Errorf("RecentTasks = %v, %v; want only task:recent", got, err)
	}

	// Reopening moves it to pending.
	if err := b.Locked(func(tx *Tx) error { return tx.SaveTask(recent, true) }); err != nil {
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
	if err := b.Locked(func(tx *Tx) error { return tx.SaveTask(task("task:1", time.Now(), "a"), true) }); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	const n = 20
	for i := range n {
		wg.Go(func() {
			err := b.UpdateTask("task:1", func(tk *Task, _ *bool) (bool, error) {
				id := "lark:om_" + string(rune('A'+i))
				tk.Append(id, []Entry{{Kind: EntryUser, Message: id, At: time.Now()}})

				return true, nil
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

func TestInbox_UpdateTask(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	if err := b.Locked(func(tx *Tx) error { return tx.SaveTask(task("task:1", time.Now(), "a"), true) }); err != nil {
		t.Fatal(err)
	}

	// Not saved: nothing changes on disk.
	err := b.UpdateTask("task:1", func(tk *Task, pending *bool) (bool, error) {
		tk.Handled, *pending = 1, false

		return false, nil
	})
	if tk, pending, _ := b.Task("task:1"); err != nil || !pending || tk.Handled != 0 {
		t.Errorf("unsaved update = %v; task %+v pending %v, want it as it was", err, tk, pending)
	}

	// Saved as done: it moves.
	err = b.UpdateTask("task:1", func(tk *Task, pending *bool) (bool, error) {
		tk.Handled, *pending = 1, false

		return true, nil
	})
	if tk, pending, _ := b.Task("task:1"); err != nil || pending || tk.Handled != 1 {
		t.Errorf("saved update = %v; task %+v pending %v, want it done", err, tk, pending)
	}

	if err := b.UpdateTask("task:nope", func(*Task, *bool) (bool, error) { return true, nil }); !errors.Is(err, ErrNoTask) {
		t.Errorf("UpdateTask missing = %v, want ErrNoTask", err)
	}

	var kept *Tx

	_ = b.Locked(func(tx *Tx) error { kept = tx; return nil })

	if err := kept.SaveTask(task("task:2", time.Now(), "b"), true); err == nil {
		t.Error("a Tx saved after its lock was released")
	}
}

// One file that does not parse is set aside; the rest still reads.
func TestInbox_unreadableFileIsSetAside(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := New(dir)

	if err := b.Locked(func(tx *Tx) error { return tx.SaveTask(task("task:1", time.Now(), "a"), true) }); err != nil {
		t.Fatal(err)
	}

	bad := filepath.Join(dir, tasksDir, taskPending, "task_bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	tasks, err := b.PendingTasks()
	if err != nil || len(tasks) != 1 || tasks[0].ID != "task:1" {
		t.Fatalf("PendingTasks = %v, %v; want task:1 despite the bad file", tasks, err)
	}

	if _, err := os.Stat(bad + badSuffix); err != nil {
		t.Errorf("bad file not set aside: %v", err)
	}
}

// RecentTasks does not read done tasks whose files were last written long
// before since: they cannot have been updated after it.
func TestInbox_RecentTasksSkipsOldFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	b := New(dir)
	now := time.Now()

	// Its file says it was written two days ago, whatever it says inside.
	stale := task("task:stale", now, "s")

	if err := b.Locked(func(tx *Tx) error { return tx.SaveTask(stale, false) }); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, tasksDir, taskDone, channel.PathSafe(stale.ID)+".json")
	if err := os.Chtimes(path, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := b.RecentTasks("lark", "oc_1", "", now.Add(-24*time.Hour))
	if err != nil || len(got) != 0 {
		t.Errorf("RecentTasks = %v, %v; want the stale file skipped", got, err)
	}

	if got, _ := b.DoneTasks(); len(got) != 1 {
		t.Errorf("DoneTasks = %v; want every done task", got)
	}
}

func TestNewTaskID_uniqueWithinAMillisecond(t *testing.T) {
	t.Parallel()

	now := time.Now()
	seen := map[string]bool{}

	for range 100 {
		id := NewTaskID(now)
		if seen[id] || channel.PathSafe(id) != "task_"+id[len(TaskPrefix):] {
			t.Fatalf("id %q repeats or does not make a file name", id)
		}

		seen[id] = true
	}
}
