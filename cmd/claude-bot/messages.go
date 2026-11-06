package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// inboxPoll is how often `inbox --wait` looks for pending tasks.
const inboxPoll = time.Second

func cmdInbox(ctx context.Context, project string, args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "block up to this long until a task is pending (e.g. 55m)")

	a, _, err := openApp(ctx, project, fs, args, exactly(0), "inbox [--wait <duration>]")
	if err != nil {
		return err
	}

	warnings, err := a.checkListener()
	if err != nil {
		return err
	}

	warn(warnings)

	if a.typesafe == nil {
		return errNoJev
	}

	tasks, err := a.pendingTasks(ctx, *wait)
	if err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "claude-bot: inbox empty")

		return nil
	}

	lines, err := a.show(tasks)
	if err != nil {
		return err
	}

	return writeLines(lines)
}

// pendingTasks returns the pending tasks, waiting up to wait for one. A
// task whose chat was closed on the platform is settled, not returned:
// the listener may not have noticed yet.
func (a *app) pendingTasks(ctx context.Context, wait time.Duration) ([]inbox.Task, error) {
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	for {
		var (
			tasks []inbox.Task
			err   error
		)

		if wait > 0 {
			tasks, err = a.inbox.WaitTasks(wctx, inboxPoll)
		} else {
			tasks, err = a.inbox.PendingTasks()
		}

		if err == nil && len(tasks) == 0 {
			err = ctx.Err() // interrupted is not "inbox empty"
		}

		if err != nil || len(tasks) == 0 {
			return nil, err
		}

		if tasks = a.settleClosed(ctx, tasks); len(tasks) > 0 || wctx.Err() != nil {
			return tasks, nil
		}
	}
}

// show records how much of each task Claude is shown, so that closing it
// leaves pending whatever arrives meanwhile, and returns the tasks as shown.
func (a *app) show(tasks []inbox.Task) ([]taskLine, error) {
	lines := make([]taskLine, 0, len(tasks))

	for _, t := range tasks {
		err := a.inbox.UpdateTask(t.ID, func(t *inbox.Task, pending *bool) (bool, error) {
			if !*pending {
				return false, nil // closed since it was listed
			}

			t.Shown = len(t.Entries)
			lines = append(lines, lineOf(*t))

			return true, nil
		})
		if err != nil && !errors.Is(err, inbox.ErrNoTask) {
			return nil, err
		}
	}

	return lines, nil
}

func cmdReply(ctx context.Context, project string, args []string) error {
	r, err := parseReplyArgs(args)
	if err != nil {
		return err
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	text, err := textArg(r.text)
	if err != nil {
		return err
	}

	taskID, err := a.taskOf(r.id)
	if err != nil {
		return err
	}

	target := r.id

	if taskID != "" {
		t, _, err := a.inbox.Task(taskID)
		if err != nil {
			return err
		}

		target = t.LastUserMessage()
	}

	ch, msg, err := a.lookup(ctx, target)
	if err != nil {
		return err
	}

	if err := ch.Reply(ctx, msg, text); err != nil {
		return err
	}

	if taskID == "" {
		fmt.Println("replied to", r.id)

		return nil
	}

	bot := inbox.Entry{Kind: inbox.EntryBot, At: time.Now().UTC(), Text: text}

	return a.closeTask(taskID, &bot, !r.keep)
}

// taskOf returns the task id is or belongs to: a task id itself, or the one
// task a screened message went into; "" for a message outside tasks (from
// before them, or refused).
func (a *app) taskOf(id string) (string, error) {
	if strings.HasPrefix(id, inbox.TaskPrefix) {
		if _, _, err := a.inbox.Task(id); err != nil {
			return "", err
		}

		return id, nil
	}

	msg, err := a.inbox.Find(id)
	if errors.Is(err, inbox.ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var tasks []string

	for _, p := range msg.Pieces {
		if !slices.Contains(tasks, p.Task) {
			tasks = append(tasks, p.Task)
		}
	}

	switch len(tasks) {
	case 0:
		return "", nil
	case 1:
		return tasks[0], nil
	}

	return "", fmt.Errorf("%w: message %s is part of tasks %s; name the task", errUsage, id, strings.Join(tasks, ", "))
}

// closeTask adds the bot's reply, if any, to a task and, if finish is set,
// closes it: done, unless people wrote more since `inbox` showed it.
func (a *app) closeTask(id string, bot *inbox.Entry, finish bool) error {
	var state string

	err := a.inbox.UpdateTask(id, func(t *inbox.Task, pending *bool) (bool, error) {
		if bot != nil {
			t.Entries = append(t.Entries, *bot)
		}

		switch {
		case !finish:
			state = "still pending"
		case t.Close():
			*pending, state = false, "done"
		default:
			*pending, state = true, "still pending: people wrote more while you worked on it; run `claude-bot inbox`"
		}

		return true, nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("%s: %s\n", id, state)

	return nil
}

// lookup returns the channel that owns id and the message itself, from the
// inbox or, when it is older than the inbox, from the platform.
func (a *app) lookup(ctx context.Context, id string) (channel.Channel, channel.Message, error) {
	ch, platformID, err := a.channelFor(id)
	if err != nil {
		return nil, channel.Message{}, err
	}

	msg, err := a.inbox.Find(id)
	if errors.Is(err, inbox.ErrNotFound) {
		msg, err = ch.Get(ctx, platformID)
	}

	return ch, msg, err
}

func cmdDone(ctx context.Context, project string, args []string) error {
	a, ids, err := openApp(ctx, project, nil, args, atLeast(1), "done <id> [<id> ...]")
	if err != nil {
		return err
	}

	// One unknown id must not leave the rest pending.
	var errs []error

	for _, id := range ids {
		taskID, err := a.taskOf(id)
		if err == nil && taskID == "" {
			err = fmt.Errorf("%w: %s", inbox.ErrNoTask, id)
		}

		if err == nil {
			err = a.closeTask(taskID, nil, true)
		}

		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func cmdGet(ctx context.Context, project string, args []string) error {
	a, args, err := openApp(ctx, project, nil, args, exactly(1), "get <id>")
	if err != nil {
		return err
	}

	ch, platformID, err := a.channelFor(args[0])
	if err != nil {
		return err
	}

	msg, err := ch.Get(ctx, platformID)
	if err != nil {
		return err
	}

	return printJSON(msg)
}

func cmdDownload(ctx context.Context, project string, args []string) error {
	a, args, err := openApp(ctx, project, nil, args, exactly(1), "download <id>")
	if err != nil {
		return err
	}

	ch, msg, err := a.lookup(ctx, args[0])
	if err != nil {
		return err
	}

	if len(msg.Attachments) == 0 {
		return fmt.Errorf("message %s has no attachments", msg.ID)
	}

	dir := a.attachmentDir(msg)
	for _, att := range msg.Attachments {
		path, err := ch.Download(ctx, msg, att, dir)
		if err != nil {
			return err
		}

		fmt.Println(path)
	}

	return nil
}

func cmdSend(ctx context.Context, project string, args []string) error {
	a, args, err := openApp(ctx, project, nil, args, atLeast(1), "send <channel>:<chat-id> [text|-]")
	if err != nil {
		return err
	}

	ch, chatID, err := a.channelFor(args[0])
	if err != nil {
		return err
	}

	text, err := textArg(args[1:])
	if err != nil {
		return err
	}

	if err := ch.Send(ctx, chatID, text); err != nil {
		return err
	}

	fmt.Println("sent to", args[0])

	return nil
}
