package main

import (
	"context"
	"errors"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// A chat can be closed on the platform by anyone, from anywhere: a GitHub
// issue closed, a PR merged. Its pending tasks then have nothing left to
// answer.
const (
	closedEvery   = time.Minute      // how often the listener looks
	closedTimeout = 30 * time.Second // for one look at every pending task
)

// closable reports whether a channel of the app can close its chats.
func (a *app) closable() bool {
	for _, ch := range a.channels {
		if _, ok := ch.(channel.Closable); ok {
			return true
		}
	}

	return false
}

// watchClosed settles, at once and then every closedEvery, the pending
// tasks whose chat was closed on the platform.
func (a *app) watchClosed(ctx context.Context) {
	t := time.NewTicker(closedEvery)
	defer t.Stop()

	for {
		tasks, err := a.inbox.PendingTasks()
		if err != nil {
			a.log.Warn("look for closed chats", "err", err)
		} else {
			a.settleClosed(ctx, tasks)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// settleClosed marks done those of tasks whose chat was closed on the
// platform after everything said in them, and returns the rest. A task
// whose chat cannot be checked stays.
func (a *app) settleClosed(ctx context.Context, tasks []inbox.Task) []inbox.Task {
	ctx, cancel := context.WithTimeout(ctx, closedTimeout)
	defer cancel()

	open := tasks[:0:0]

	for _, t := range tasks {
		if !a.settleIfClosed(ctx, t) {
			open = append(open, t)
		}
	}

	return open
}

// settleIfClosed reports whether t is done because its chat was closed.
func (a *app) settleIfClosed(ctx context.Context, t inbox.Task) bool {
	c, ok := a.channels[t.Channel].(channel.Closable)
	if !ok {
		return false
	}

	at, err := c.ClosedAt(ctx, t.ChatID)
	if err != nil {
		a.log.Warn("is the chat closed", "task", t.ID, "chat", t.ChatID, "err", err)

		return false
	}

	if at.IsZero() {
		return false
	}

	done, settled := false, false

	err = a.inbox.UpdateTask(t.ID, func(t *inbox.Task, pending *bool) (bool, error) { // as it is now, not as listed
		if !*pending {
			done = true

			return false, nil
		}

		if !t.ClosedAt(at) {
			return false, nil
		}

		done, settled, *pending = true, true, false

		return true, nil
	})
	if errors.Is(err, inbox.ErrNoTask) {
		done, err = true, nil
	}

	if err != nil {
		a.log.Warn("settle closed chat", "task", t.ID, "err", err)

		return false
	}

	if settled {
		a.log.Info("chat closed on the platform: task done", "task", t.ID, "chat", t.ChatID, "closed_at", at)
	}

	return done
}
