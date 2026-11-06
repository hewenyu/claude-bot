//go:build unix

package filelock

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockHeld(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.lock")

	unlock, err := Lock(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// flock locks belong to the open file, so a second open in this
	// process conflicts as another process's would.
	if _, err := TryLock(path, 0o600); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while held = %v, want ErrLocked", err)
	}

	unlock()

	again, err := TryLock(path, 0o600)
	if err != nil {
		t.Fatalf("TryLock after unlock: %v", err)
	}

	again()
}

func TestLockWaits(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.lock")

	unlock, err := Lock(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan struct{})

	go func() {
		if u, err := Lock(path, 0o600); err == nil {
			u()
		}

		close(got)
	}()

	select {
	case <-got:
		t.Fatal("Lock did not wait for the holder")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	<-got
}
