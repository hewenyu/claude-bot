package inbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

func msg(id string, at time.Time) channel.Message {
	return channel.Message{ID: id, Channel: "lark", ChatID: "oc_1", ChatType: "p2p", Text: "hi " + id, CreatedAt: at}
}

func TestInbox_Put(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	m := msg("lark:om_1", time.Now())

	fresh, err := b.Put(m)
	if err != nil || !fresh {
		t.Fatalf("first Put = %v, %v; want true, nil", fresh, err)
	}

	if fresh, _ := b.Put(m); fresh {
		t.Error("re-delivered pending message reported as fresh")
	}

	if err := b.Done(m.ID); err != nil {
		t.Fatal(err)
	}

	if fresh, _ := b.Put(m); fresh {
		t.Error("re-delivered done message reported as fresh")
	}
}

func TestInbox_Pending(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	base := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

	for _, m := range []channel.Message{msg("lark:om_c", base.Add(2*time.Minute)), msg("lark:om_a", base), msg("lark:om_b", base.Add(time.Minute))} {
		if _, err := b.Put(m); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Done("lark:om_b"); err != nil {
		t.Fatal(err)
	}

	got, err := b.Pending()
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].ID != "lark:om_a" || got[1].ID != "lark:om_c" {
		t.Fatalf("Pending() = %v, want om_a then om_c", ids(got))
	}

	if got[0].Text != "hi lark:om_a" || !got[0].CreatedAt.Equal(base) {
		t.Errorf("round trip lost fields: %+v", got[0])
	}
}

func TestInbox_Pending_empty(t *testing.T) {
	t.Parallel()

	got, err := New(t.TempDir()).Pending()
	if err != nil || len(got) != 0 {
		t.Fatalf("Pending() on fresh inbox = %v, %v", got, err)
	}
}

func TestInbox_Wait(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())

	go func() {
		time.Sleep(50 * time.Millisecond)

		_, _ = b.Put(msg("lark:om_late", time.Now()))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := b.Wait(ctx, 10*time.Millisecond)
	if err != nil || len(got) != 1 || got[0].ID != "lark:om_late" {
		t.Fatalf("Wait() = %v, %v", ids(got), err)
	}
}

func TestInbox_Wait_timeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	got, err := New(t.TempDir()).Wait(ctx, 5*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("Wait() on empty inbox = %v, %v; want nothing, nil", got, err)
	}
}

func TestInbox_Find(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	if _, err := b.Put(msg("lark:om_1", time.Now())); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Find("lark:om_1"); err != nil {
		t.Errorf("Find pending: %v", err)
	}

	if err := b.Done("lark:om_1"); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Find("lark:om_1"); err != nil {
		t.Errorf("Find done: %v", err)
	}

	if _, err := b.Find("lark:nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find missing err = %v, want ErrNotFound", err)
	}
}

func TestInbox_Done(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	if _, err := b.Put(msg("lark:om_1", time.Now())); err != nil {
		t.Fatal(err)
	}

	if err := b.Done("lark:om_1"); err != nil {
		t.Fatal(err)
	}

	if err := b.Done("lark:om_1"); err != nil {
		t.Errorf("second Done should be a no-op, got %v", err)
	}

	if err := b.Done("lark:missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Done missing err = %v, want ErrNotFound", err)
	}
}

func ids(ms []channel.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}

	return out
}

func TestInbox_Put_concurrentRedelivery(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	m := msg("lark:om_1", time.Now())

	var (
		wg    sync.WaitGroup
		fresh atomic.Int32
	)

	for range 8 {
		wg.Go(func() {
			ok, err := b.Put(m)
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
		t.Errorf("%d deliveries reported fresh, want exactly 1", n)
	}

	if got, err := b.Pending(); err != nil || len(got) != 1 {
		t.Errorf("Pending() = %v, %v; want the one message", ids(got), err)
	}
}

func TestInbox_HoldAdmitReject(t *testing.T) {
	t.Parallel()

	b := New(t.TempDir())
	held, refused := msg("lark:om_1", time.Now()), msg("lark:om_2", time.Now())

	for _, m := range []channel.Message{held, refused} {
		if fresh, err := b.Hold(m); !fresh || err != nil {
			t.Fatalf("Hold(%s) = %v, %v", m.ID, fresh, err)
		}
	}

	if pending, _ := b.Pending(); len(pending) != 0 {
		t.Fatalf("held messages are pending: %v", pending)
	}

	held.Text = "screened"
	if err := b.Admit(held); err != nil {
		t.Fatal(err)
	}

	if err := b.Reject(refused); err != nil {
		t.Fatal(err)
	}

	pending, _ := b.Pending()
	incoming, _ := b.Incoming()

	if len(pending) != 1 || pending[0].Text != "screened" || len(incoming) != 0 {
		t.Errorf("pending = %v, incoming = %v; want the screened message pending", pending, incoming)
	}

	// Redeliveries of either are not held again.
	for _, m := range []channel.Message{held, refused} {
		if fresh, _ := b.Hold(m); fresh {
			t.Errorf("redelivered %s held again", m.ID)
		}
	}
}
