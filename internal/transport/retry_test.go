package transport

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

var (
	errFatal = errors.New("refused")
	errBlip  = errors.New("connection reset")
)

func isFatal(err error) bool { return errors.Is(err, errFatal) }

func TestRetry(t *testing.T) {
	t.Parallel()

	fast := Backoff{Start: time.Millisecond, Cap: time.Millisecond}
	log := slog.New(slog.DiscardHandler)

	tries := 0

	v, err := Retry(t.Context(), fast, log, "test", isFatal, func(context.Context) (int, error) {
		if tries++; tries < 3 {
			return 0, errBlip
		}

		return 42, nil
	})
	if v != 42 || err != nil || tries != 3 {
		t.Errorf("Retry() = %d, %v after %d tries; want 42 after 3", v, err, tries)
	}

	tries = 0
	if _, err := Retry(t.Context(), fast, log, "test", isFatal, func(context.Context) (int, error) {
		tries++

		return 0, errFatal
	}); !errors.Is(err, errFatal) || tries != 1 {
		t.Errorf("Retry() = %v after %d tries; want the fatal error at once", err, tries)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := Retry(ctx, fast, log, "test", isFatal, func(context.Context) (int, error) { return 0, errBlip }); err != nil {
		t.Errorf("Retry() on a cancelled context = %v, want nil", err)
	}
}

func TestReconnect(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	sessions := 0

	// Asked to reconnect twice (no wait), then refused.
	err := Reconnect(t.Context(), log, "test", isFatal, func(context.Context) (bool, error) {
		if sessions++; sessions < 3 {
			return true, nil
		}

		return false, errFatal
	})
	if !errors.Is(err, errFatal) || sessions != 3 {
		t.Errorf("Reconnect() = %v after %d sessions; want the fatal error after 3", err, sessions)
	}

	ctx, cancel := context.WithCancel(t.Context())

	err = Reconnect(ctx, log, "test", isFatal, func(context.Context) (bool, error) {
		cancel()

		return true, errBlip
	})
	if err != nil {
		t.Errorf("Reconnect() after cancel = %v, want nil", err)
	}
}

func TestRetryOnce(t *testing.T) {
	t.Parallel()

	limited := func(wait time.Duration) func(error) (time.Duration, bool) {
		return func(err error) (time.Duration, bool) { return wait, errors.Is(err, errBlip) }
	}

	calls := 0
	call := func() error {
		if calls++; calls == 1 {
			return errBlip
		}

		return nil
	}

	if err := RetryOnce(t.Context(), limited(time.Millisecond), call); err != nil || calls != 2 {
		t.Errorf("RetryOnce() = %v after %d calls; want success on the second", err, calls)
	}

	calls = 0
	if err := RetryOnce(t.Context(), limited(maxRetryAfter+time.Second), call); !errors.Is(err, errBlip) || calls != 1 {
		t.Errorf("RetryOnce() with a long wait = %v after %d calls; want no retry", err, calls)
	}
}

func TestPoll(t *testing.T) {
	t.Parallel()

	fast := Backoff{Start: time.Millisecond, Cap: time.Millisecond}
	log := slog.New(slog.DiscardHandler)
	polls, connected := 0, 0

	// Fails, succeeds twice, fails, then is refused.
	err := Poll(t.Context(), log, "test", 0, fast, isFatal, func(context.Context) error {
		switch polls++; polls {
		case 1, 4:
			return errBlip
		case 5:
			return errFatal
		}

		return nil
	}, func() { connected++ })
	if !errors.Is(err, errFatal) || polls != 5 {
		t.Errorf("Poll() = %v after %d polls; want the fatal error after 5", err, polls)
	}

	if connected != 1 {
		t.Errorf("connected called %d times, want once", connected)
	}

	ctx, cancel := context.WithCancel(t.Context())

	err = Poll(ctx, log, "test", time.Hour, fast, isFatal, func(context.Context) error {
		cancel()

		return errBlip
	}, nil)
	if err != nil {
		t.Errorf("Poll() after cancel = %v, want nil", err)
	}
}
