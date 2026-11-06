package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/term"
)

// The page's tasks: what each project was asked, and how it went.
const (
	taskListLimit = 300 // handled tasks the page lists
	titleRunes    = 120
	githubWeb     = "https://github.com/"
)

// taskState is a task's state as the page shows it.
type taskState string

// Task states.
const (
	taskPending taskState = "pending" // waiting for Claude
	taskWorking taskState = "working" // Claude has it
	taskReplied taskState = "replied" // handled, the bot answered
	taskClosed  taskState = "closed"  // handled without an answer
)

// handled reports whether a task in state s is archived.
func (s taskState) handled() bool { return s == taskReplied || s == taskClosed }

// taskSummary is a task as the list shows it.
type taskSummary struct {
	ID        string           `json:"id"`
	Channel   string           `json:"channel"`
	ChatID    string           `json:"chat_id"`
	ChatType  channel.ChatType `json:"chat_type"`
	State     taskState        `json:"state"`
	Title     string           `json:"title"`
	Senders   []string         `json:"senders"`
	Entries   int              `json:"entries"`
	Replies   int              `json:"replies"`
	Unhandled int              `json:"unhandled"` // user entries Claude has not handled
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Link      string           `json:"link,omitempty"`
}

func summarize(t inbox.Task, pending bool) taskSummary {
	s := taskSummary{
		ID: t.ID, Channel: t.Channel, ChatID: t.ChatID, ChatType: t.ChatType,
		Entries: len(t.Entries), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Link: taskLink(t), Senders: []string{},
	}

	for i, e := range t.Entries {
		if e.Kind == inbox.EntryBot {
			s.Replies++
			s.UpdatedAt = later(s.UpdatedAt, e.At)

			continue
		}

		if s.Title == "" {
			s.Title = titleOf(e)
		}

		if e.Sender != nil && !slices.Contains(s.Senders, senderName(*e.Sender)) {
			s.Senders = append(s.Senders, senderName(*e.Sender))
		}

		if i >= t.Handled {
			s.Unhandled++
		}
	}

	switch {
	case pending && t.Shown > 0:
		s.State = taskWorking
	case pending:
		s.State = taskPending
	case s.Replies > 0:
		s.State = taskReplied
	default:
		s.State = taskClosed
	}

	return s
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

func senderName(u channel.User) string {
	if u.Name != "" {
		return u.Name
	}

	return u.ID
}

// titleOf is the first line of an entry, shortened.
func titleOf(e inbox.Entry) string {
	line, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
	if line == "" && e.File != "" {
		line = filepath.Base(e.File)
	}

	return term.Clip(line, titleRunes)
}

// taskLink is where a task can be seen on its platform, if it has an address.
func taskLink(t inbox.Task) string {
	if t.Channel != github.Name {
		return ""
	}

	repo, number, ok := strings.CutLast(t.ChatID, "/")
	if !ok || strings.Count(repo, "/") != 1 {
		return ""
	}

	if _, err := strconv.Atoi(number); err != nil {
		return ""
	}

	return githubWeb + repo + "/issues/" + number
}

func (w *web) tasks(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	box := projectInbox(p)

	pending, err := box.PendingTasks()
	if err != nil {
		return nil, err
	}

	done, err := box.DoneTasks()
	if err != nil {
		return nil, err
	}

	out := make([]taskSummary, 0, len(pending)+len(done))
	for _, t := range pending {
		out = append(out, summarize(t, true))
	}

	for _, t := range done {
		out = append(out, summarize(t, false))
	}

	return listTasks(out, taskListLimit), nil
}

// listTasks puts the latest first and keeps every open task, but only the
// latest limit handled ones: the page shows the open tasks and keeps the
// handled ones in its archive.
func listTasks(all []taskSummary, limit int) []taskSummary {
	slices.SortFunc(all, func(a, b taskSummary) int { return b.UpdatedAt.Compare(a.UpdatedAt) })

	out := all[:0]
	done := 0

	for _, s := range all {
		if s.State.handled() {
			if done == limit {
				continue
			}

			done++
		}

		out = append(out, s)
	}

	return out
}

// taskDetail is a task with its whole conversation.
type taskDetail struct {
	taskSummary

	Handled int           `json:"handled"`
	Thread  string        `json:"thread_id,omitempty"`
	Log     []inbox.Entry `json:"log"`
}

func (w *web) task(r *http.Request) (any, error) {
	p, err := w.project(r)
	if err != nil {
		return nil, err
	}

	id := r.PathValue("task")
	if !strings.HasPrefix(id, inbox.TaskPrefix) {
		return nil, fmt.Errorf("%w: %s", inbox.ErrNoTask, id)
	}

	t, pending, err := projectInbox(p).Task(id)
	if err != nil {
		return nil, err
	}

	return taskDetail{taskSummary: summarize(t, pending), Handled: t.Handled, Thread: t.ThreadID, Log: t.Entries}, nil
}
