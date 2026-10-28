// Package inbox is a file-backed queue between the listener (serve) and
// Claude. Each message is one JSON file in pending/ until it is moved to
// done/. Files appear by link and move by rename, both atomic, so no locking
// is needed.
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel"
)

// Message states, each a subdirectory of the inbox.
const (
	statePending = "pending"
	stateDone    = "done"
)

const dirPerm = 0o750

// ErrNotFound reports a message id that is in neither pending nor done.
var ErrNotFound = errors.New("message not in inbox")

// Inbox is rooted at a directory containing pending/ and done/.
type Inbox struct {
	dir string
}

// New returns an Inbox rooted at dir.
func New(dir string) *Inbox {
	return &Inbox{dir: dir}
}

func (b *Inbox) path(state, id string) string {
	return filepath.Join(b.dir, state, channel.PathSafe(id)+".json")
}

// Put stores m as pending. It returns false if m was seen before (Lark
// re-delivers events that were not acknowledged in time, possibly
// concurrently).
func (b *Inbox) Put(m channel.Message) (bool, error) {
	if _, err := os.Stat(b.path(stateDone, m.ID)); err == nil {
		return false, nil
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal message: %w", err)
	}

	dir := filepath.Join(b.dir, statePending)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return false, fmt.Errorf("create inbox: %w", err)
	}

	// Not *.json, so Pending never reads it.
	tmp, err := atomicfile.WriteTemp(dir, ".put-*", append(raw, '\n'))
	if err != nil {
		return false, fmt.Errorf("store message: %w", err)
	}
	defer os.Remove(tmp) //nolint:errcheck // the linked copy is what counts

	// Link fails if the message is already pending, so concurrent deliveries
	// of one event store it exactly once and readers never see a partial file.
	if err := os.Link(tmp, b.path(statePending, m.ID)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}

		return false, fmt.Errorf("commit message: %w", err)
	}

	return true, nil
}

// Pending returns unhandled messages, oldest first.
func (b *Inbox) Pending() ([]channel.Message, error) {
	entries, err := os.ReadDir(filepath.Join(b.dir, statePending))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}

	msgs := make([]channel.Message, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}

		m, err := read(filepath.Join(b.dir, statePending, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // marked done by another process since ReadDir
		}

		if err != nil {
			return nil, err
		}

		msgs = append(msgs, m)
	}

	slices.SortFunc(msgs, func(a, c channel.Message) int { return a.CreatedAt.Compare(c.CreatedAt) })

	return msgs, nil
}

// Wait returns pending messages, polling until at least one exists or ctx ends.
func (b *Inbox) Wait(ctx context.Context, every time.Duration) ([]channel.Message, error) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		msgs, err := b.Pending()
		if err != nil || len(msgs) > 0 {
			return msgs, err
		}

		select {
		case <-ctx.Done():
			return nil, nil // timing out with an empty inbox is the normal outcome
		case <-t.C:
		}
	}
}

// Find returns a message by id from pending or done.
func (b *Inbox) Find(id string) (channel.Message, error) {
	for _, state := range []string{statePending, stateDone} {
		m, err := read(b.path(state, id))
		if err == nil {
			return m, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return channel.Message{}, err
		}
	}

	return channel.Message{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Done marks a message handled. Marking an already-done message is a no-op.
func (b *Inbox) Done(id string) error {
	if _, err := os.Stat(b.path(stateDone, id)); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Join(b.dir, stateDone), dirPerm); err != nil {
		return fmt.Errorf("create inbox: %w", err)
	}

	if err := os.Rename(b.path(statePending, id), b.path(stateDone, id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}

		return fmt.Errorf("mark done: %w", err)
	}

	return nil
}

func read(path string) (channel.Message, error) {
	var m channel.Message

	raw, err := os.ReadFile(path) //nolint:gosec // path is built from a sanitized id inside the inbox
	if err != nil {
		return m, fmt.Errorf("read message: %w", err)
	}

	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	return m, nil
}
