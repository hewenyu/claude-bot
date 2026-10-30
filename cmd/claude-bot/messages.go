package main

import (
	"context"
	"encoding/json"
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

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	if err := a.checkListener(); err != nil {
		return err
	}

	if a.typesafe == nil {
		return errNoJev
	}

	var tasks []inbox.Task

	if *wait > 0 {
		wctx, cancel := context.WithTimeout(ctx, *wait)
		tasks, err = a.inbox.WaitTasks(wctx, inboxPoll)

		cancel()
	} else {
		tasks, err = a.inbox.PendingTasks()
	}

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

// show records how much of each task Claude is shown, so that closing it
// leaves pending whatever arrives meanwhile, and returns the tasks as shown.
func (a *app) show(tasks []inbox.Task) ([]taskLine, error) {
	lines := make([]taskLine, 0, len(tasks))

	err := a.inbox.Locked(func() error {
		for _, t := range tasks {
			t, pending, err := a.inbox.Task(t.ID)
			if errors.Is(err, inbox.ErrNoTask) || err == nil && !pending {
				continue // closed since it was listed
			}

			if err != nil {
				return err
			}

			t.Shown = len(t.Entries)
			if err := a.inbox.SaveTask(t, true); err != nil {
				return err
			}

			lines = append(lines, lineOf(t))
		}

		return nil
	})

	return lines, err
}

// writeLines prints values as JSON lines.
func writeLines[T any](values []T) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("write inbox: %w", err)
		}
	}

	return nil
}

// replyArgs is a parsed `reply [--keep] <id> [text|-]`.
type replyArgs struct {
	id   string
	text []string
	keep bool
}

func parseReplyArgs(args []string) (replyArgs, error) {
	var r replyArgs

	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.BoolVar(&r.keep, "keep", false, "leave the message pending (e.g. for an interim \"looking into it\" reply)")

	if err := fs.Parse(args); err != nil {
		return r, fmt.Errorf("%w: %w", errUsage, err)
	}

	if fs.NArg() < 1 {
		return r, fmt.Errorf("%w: reply <id> [text|-]", errUsage)
	}

	r.id, r.text = fs.Arg(0), fs.Args()[1:]

	// The flag package stops at the id; don't send a misplaced flag as text.
	if len(r.text) > 0 && (r.text[0] == "--keep" || r.text[0] == "-keep") {
		r.keep, r.text = true, r.text[1:]
	}

	return r, nil
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

	err := a.inbox.Locked(func() error {
		t, pending, err := a.inbox.Task(id)
		if err != nil {
			return err
		}

		if bot != nil {
			t.Entries = append(t.Entries, *bot)
		}

		switch {
		case !finish:
			state = "still pending"
		case t.Close():
			pending, state = false, "done"
		default:
			pending, state = true, "still pending: people wrote more while you worked on it; run `claude-bot inbox`"
		}

		return a.inbox.SaveTask(t, pending)
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
	ids, err := parseArgs("done", args)
	if err != nil {
		return err
	}

	if len(ids) == 0 {
		return fmt.Errorf("%w: done <id> [<id> ...]", errUsage)
	}

	a, err := newApp(ctx, project)
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
	args, err := parseArgs("get", args)
	if err != nil {
		return err
	}

	if len(args) != 1 {
		return fmt.Errorf("%w: get <id>", errUsage)
	}

	a, err := newApp(ctx, project)
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
	args, err := parseArgs("download", args)
	if err != nil {
		return err
	}

	if len(args) != 1 {
		return fmt.Errorf("%w: download <id>", errUsage)
	}

	a, err := newApp(ctx, project)
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
	args, err := parseArgs("send", args)
	if err != nil {
		return err
	}

	if len(args) < 1 {
		return fmt.Errorf("%w: send <channel>:<chat-id> [text|-]", errUsage)
	}

	a, err := newApp(ctx, project)
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
