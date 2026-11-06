package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Many notifies while f runs make one more call, not one each, and the
// last notify is always followed by a call.
func TestNotifierCoalesces(t *testing.T) {
	t.Parallel()

	var (
		calls atomic.Int32
		gate  = make(chan struct{})
		once  sync.Once
	)

	n := &notifier{f: func() {
		calls.Add(1)
		once.Do(func() { <-gate }) // the first call waits while notifies pile up
	}}

	n.notify()

	for range 100 {
		n.notify()
	}

	close(gate)

	deadline := time.Now().Add(2 * time.Second)

	for {
		n.mu.Lock()
		running := n.running
		n.mu.Unlock()

		if !running {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("notifier never went idle")
		}

		time.Sleep(time.Millisecond)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("f ran %d times; want 2: the first, and one for all that came meanwhile", got)
	}
}
