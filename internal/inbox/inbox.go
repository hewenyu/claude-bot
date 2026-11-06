// Package inbox is the file-backed store between the listener (serve) and
// Claude. Messages arrive in incoming/; once the listener has screened them
// they are archived in done/ and what they say goes into tasks (tasks/pending
// and tasks/done), the unit Claude works on. Message files appear by link and
// move by rename, both atomic; tasks are read and written under a file lock.
// pending/ holds messages from versions before tasks, until Migrate.
package inbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel"
)

// Message states, each a subdirectory of the inbox.
const (
	stateIncoming = "incoming"
	statePending  = "pending"
	stateDone     = "done"
)

const dirPerm = 0o750

// ErrNotFound reports a message id that is in neither pending nor done.
var ErrNotFound = errors.New("message not in inbox")

// Inbox is rooted at a directory containing incoming/, pending/ and done/.
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

// Hold stores m as incoming, for the listener to screen before Claude sees
// it. It returns false if m was seen before (Lark re-delivers events that
// were not acknowledged in time, possibly concurrently).
func (b *Inbox) Hold(m channel.Message) (bool, error) {
	if _, err := os.Stat(b.path(statePending, m.ID)); err == nil {
		return false, nil
	}

	return b.put(stateIncoming, m)
}

func (b *Inbox) put(state string, m channel.Message) (bool, error) {
	if _, err := os.Stat(b.path(stateDone, m.ID)); err == nil {
		return false, nil
	}

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal message: %w", err)
	}

	dir := filepath.Join(b.dir, state)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return false, fmt.Errorf("create inbox: %w", err)
	}

	// Not *.json, so readers never see it.
	tmp, err := atomicfile.WriteTemp(dir, ".put-*", append(raw, '\n'))
	if err != nil {
		return false, fmt.Errorf("store message: %w", err)
	}
	defer os.Remove(tmp) //nolint:errcheck // the linked copy is what counts

	// Link fails if the message is already there, so concurrent deliveries
	// of one event store it exactly once and readers never see a partial file.
	if err := os.Link(tmp, b.path(state, m.ID)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}

		return false, fmt.Errorf("commit message: %w", err)
	}

	return true, nil
}

// Archive stores a screened incoming message, as m, in done: what it says
// went into tasks.
func (b *Inbox) Archive(m channel.Message) error {
	return b.move(m, stateDone)
}

// Migrate sends messages left in pending/ by versions before tasks back to
// incoming/, to be screened into tasks.
func (b *Inbox) Migrate() (int, error) {
	msgs, err := b.list(statePending)
	if err != nil {
		return 0, err
	}

	if len(msgs) > 0 {
		if err := os.MkdirAll(filepath.Join(b.dir, stateIncoming), dirPerm); err != nil {
			return 0, fmt.Errorf("create inbox: %w", err)
		}
	}

	for _, m := range msgs {
		if err := os.Rename(b.path(statePending, m.ID), b.path(stateIncoming, m.ID)); err != nil {
			return 0, fmt.Errorf("migrate %s: %w", m.ID, err)
		}
	}

	return len(msgs), nil
}

// move writes m into state and removes it from incoming.
func (b *Inbox) move(m channel.Message, state string) error {
	if err := os.MkdirAll(filepath.Join(b.dir, state), dirPerm); err != nil {
		return fmt.Errorf("create inbox: %w", err)
	}

	if err := atomicfile.WriteJSON(b.path(state, m.ID), m, messagePerm); err != nil {
		return fmt.Errorf("store message: %w", err)
	}

	if err := os.Remove(b.path(stateIncoming, m.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove incoming message: %w", err)
	}

	return nil
}

// messagePerm keeps a moved message as private as a stored one.
const messagePerm = 0o600

// Incoming returns messages awaiting screening, oldest first.
func (b *Inbox) Incoming() ([]channel.Message, error) {
	return b.list(stateIncoming)
}

func (b *Inbox) list(state string) ([]channel.Message, error) {
	msgs, err := readAll[channel.Message](filepath.Join(b.dir, state), "message", time.Time{})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(msgs, func(a, c channel.Message) int { return a.CreatedAt.Compare(c.CreatedAt) })

	return msgs, nil
}

// Find returns a message by id from pending or done.
func (b *Inbox) Find(id string) (channel.Message, error) {
	for _, state := range []string{statePending, stateDone} {
		m, err := readJSON[channel.Message](b.path(state, id), "message")
		if err == nil {
			return m, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return channel.Message{}, err
		}
	}

	return channel.Message{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// readAll reads every JSON file in dir, a message or a task (what), but
// those last written before modSince, if set. A dir not made yet holds
// none. A file that does not parse is set aside (see setAside), so that
// one bad file does not stop everything else in dir.
func readAll[T any](dir, what string, modSince time.Time) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %ss: %w", what, err)
	}

	out := make([]T, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" || modifiedBefore(e, modSince) {
			continue
		}

		path := filepath.Join(dir, e.Name())

		v, err := readJSON[T](path, what)
		if errors.Is(err, fs.ErrNotExist) {
			continue // moved by another process since ReadDir
		}

		if errors.Is(err, errParse) {
			setAside(path, err)

			continue
		}

		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}

// modifiedBefore reports whether e was last written before since; never
// for a zero since, or when that is not known.
func modifiedBefore(e fs.DirEntry, since time.Time) bool {
	if since.IsZero() {
		return false
	}

	info, err := e.Info()

	return err == nil && info.ModTime().Before(since)
}

// badSuffix marks a file set aside: readers skip it, as it is no *.json.
const badSuffix = ".bad"

// setAside renames a file that does not parse to *.json.bad, and says so
// once, on the default logger. Every file is written whole (atomicfile, or
// a link to a synced temp file), so such a file is broken for good, e.g.
// edited by hand, and reading it again would only fail again. Renaming it
// back retries it.
func setAside(path string, why error) {
	if err := os.Rename(path, path+badSuffix); err != nil {
		if !errors.Is(err, fs.ErrNotExist) { // set aside by another reader
			slog.Warn("inbox: skip unreadable file", "path", path, "err", why, "rename_err", err)
		}

		return
	}

	slog.Warn("inbox: set aside unreadable file", "path", path+badSuffix, "err", why)
}

// errParse marks a file that is not the JSON it should be.
var errParse = errors.New("not valid JSON")

// readJSON reads one JSON file, a message or a task (what).
func readJSON[T any](path, what string) (T, error) {
	var v T

	raw, err := os.ReadFile(path) //nolint:gosec // path is built from a sanitized id inside the inbox
	if err != nil {
		return v, fmt.Errorf("read %s: %w", what, err)
	}

	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("parse %s: %w: %w", filepath.Base(path), errParse, err)
	}

	return v, nil
}
