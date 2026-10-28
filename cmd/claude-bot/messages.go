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
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

func cmdInbox(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "block up to this long until a message arrives (e.g. 9m)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	if err := a.ensureListener(ctx); err != nil {
		return err
	}

	var msgs []channel.Message

	if *wait > 0 {
		wctx, cancel := context.WithTimeout(ctx, *wait)
		msgs, err = a.inbox.Wait(wctx, time.Second)

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

func cmdReply(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	keep := fs.Bool("keep", false, "leave the message pending (e.g. for an interim \"looking into it\" reply)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("%w: reply <id> [text|-]", errUsage)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	id := fs.Arg(0)

	ch, _, err := a.channelFor(id)
	if err != nil {
		return err
	}

	text, err := textArg(fs.Args()[1:])
	if err != nil {
		return err
	}

	msg, err := a.inbox.Find(id)
	if errors.Is(err, inbox.ErrNotFound) {
		msg, err = ch.Get(ctx, id) // replying to something older than the inbox
	}

	if err != nil {
		return err
	}

	if err := ch.Reply(ctx, msg, text); err != nil {
		return err
	}

	if !*keep {
		if err := a.inbox.Done(id); err != nil && !errors.Is(err, inbox.ErrNotFound) {
			return err
		}
	}

	fmt.Println("replied to", id)

	return nil
}

func cmdDone(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: done <id> [<id> ...]", errUsage)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	for _, id := range args {
		if err := a.inbox.Done(id); err != nil {
			return err
		}
	}

	return nil
}

func cmdGet(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: get <id>", errUsage)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	ch, _, err := a.channelFor(args[0])
	if err != nil {
		return err
	}

	msg, err := ch.Get(ctx, args[0])
	if err != nil {
		return err
	}

	return printJSON(msg)
}

func cmdDownload(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: download <id>", errUsage)
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	id := args[0]

	ch, _, err := a.channelFor(id)
	if err != nil {
		return err
	}

	msg, err := a.inbox.Find(id)
	if errors.Is(err, inbox.ErrNotFound) {
		msg, err = ch.Get(ctx, id)
	}

	if err != nil {
		return err
	}

	if len(msg.Attachments) == 0 {
		return fmt.Errorf("message %s has no attachments", id)
	}

	dir := filepath.Join(a.dir, "files")
	for _, att := range msg.Attachments {
		path, err := ch.Download(ctx, msg, att, dir)
		if err != nil {
			return err
		}

		fmt.Println(path)
	}

	return nil
}

func cmdSend(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("%w: send <channel>:<chat-id> [text|-]", errUsage)
	}

	a, err := newApp(ctx)
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

func cmdStatus(ctx context.Context, _ []string) error {
	a, err := newApp(ctx)
	if err != nil {
		return err
	}

	pending, err := a.inbox.Pending()
	if err != nil {
		return err
	}

	hb, alive := readHeartbeat(a.dir)
	status := map[string]any{
		"project":  filepath.Dir(a.dir),
		"channels": a.names(),
		"listener": map[string]any{"alive": alive, "pid": hb.PID, "last_beat": hb.BeatAt},
		"pending":  len(pending),
	}

	if ch, ok := a.channels[lark.Name].(*lark.Channel); ok {
		bot, err := ch.Bot(ctx)
		if err != nil {
			status["lark"] = map[string]any{"error": err.Error()}
		} else {
			status["lark"] = bot
		}
	}

	return printJSON(status)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}
