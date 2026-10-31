package channel

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeNames counts lookups on a clock the test moves.
type fakeNames struct {
	Names

	at      time.Time
	calls   map[string]int
	refuse  bool
	refused error
}

func newFakeNames() *fakeNames {
	f := &fakeNames{at: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC), calls: map[string]int{}, refused: errors.New("no permission")}
	f.now = func() time.Time { return f.at }
	f.Lookup = func(_ context.Context, id string) (string, error) {
		f.calls[id]++

		if f.refuse {
			return "", f.refused
		}

		return "name of " + id, nil
	}

	return f
}

func TestNames_cachesFoundNames(t *testing.T) {
	t.Parallel()

	f := newFakeNames()

	for range 2 {
		if name, err := f.Name(t.Context(), "u_a"); name != "name of u_a" || err != nil {
			t.Fatalf("Name(u_a) = %q, %v", name, err)
		}
	}

	if f.calls["u_a"] != 1 {
		t.Errorf("u_a looked up %d times, want 1", f.calls["u_a"])
	}

	f.at = f.at.Add(nameTTL)
	_, _ = f.Name(t.Context(), "u_a")

	if f.calls["u_a"] != 2 {
		t.Errorf("u_a looked up %d times after the ttl, want 2", f.calls["u_a"])
	}

	if name, err := f.Name(t.Context(), ""); name != "" || err != nil || f.calls[""] != 0 {
		t.Errorf("Name(\"\") = %q, %v", name, err)
	}
}

func TestNames_remembersFailures(t *testing.T) {
	t.Parallel()

	f := newFakeNames()
	f.refuse = true

	if name, err := f.Name(t.Context(), "u_b"); name != "" || !errors.Is(err, f.refused) {
		t.Errorf("Name(u_b) = %q, %v; want the error", name, err)
	}

	// Remembered: no error to log again, no new lookup.
	if name, err := f.Name(t.Context(), "u_b"); name != "" || err != nil || f.calls["u_b"] != 1 {
		t.Errorf("Name(u_b) again = %q, %v after %d lookups", name, err, f.calls["u_b"])
	}

	f.at = f.at.Add(nameRetry)
	f.refuse = false

	if name, _ := f.Name(t.Context(), "u_b"); name != "name of u_b" {
		t.Errorf("Name(u_b) after the retry delay = %q", name)
	}
}
