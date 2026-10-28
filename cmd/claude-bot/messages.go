package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// inboxPoll is how often `inbox --wait` looks for new messages.
const inboxPoll = time.Second

func cmdInbox(ctx context.Context, project string, args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "block up to this long until a message arrives (e.g. 9m)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	if err := a.ensureListener(ctx); err != nil {
		return err
	}

	var msgs []channel.Message

	if *wait > 0 {
		wctx, cancel := context.WithTimeout(ctx, *wait)
		msgs, err = a.inbox.Wait(wctx, inboxPoll)

		cancel()
	} else {
		msgs, err = a.inbox.Pending()
	}

	if err != nil {
		return err
	}

	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "claude-bot: inbox empty")

		return nil
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return fmt.Errorf("write message: %w", err)
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

	ch, msg, err := a.lookup(ctx, r.id)
	if err != nil {
		return err
	}

	if err := ch.Reply(ctx, msg, text); err != nil {
		return err
	}

	if !r.keep {
		if err := a.inbox.Done(r.id); err != nil && !errors.Is(err, inbox.ErrNotFound) {
			return err
		}
	}

	fmt.Println("replied to", r.id)

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
		if err := a.inbox.Done(id); err != nil {
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

	// One directory per message: attachments of different messages often
	// share a name (image.png, crash.log).
	dir := filepath.Join(a.path(filesDir), channel.PathSafe(msg.ID))
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
