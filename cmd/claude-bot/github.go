package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel/github"
)

const (
	reviewUsage = "github review <id> approve|request-changes|comment [text|-]"
	githubUsage = "github close <id> [--not-planned] | merge <id> [--method squash|merge|rebase] | " + reviewUsage +
		" | mine <id>"
)

// errNotMine is the verdict of "github mine" on a PR the bot did not open.
var errNotMine = errors.New("not the bot's own PR: audit it as someone else's")

// Review verdicts as typed on the command line.
var reviewEvents = map[string]string{
	"approve":         github.ReviewApprove,
	"request-changes": github.ReviewRequestChanges,
	"comment":         github.ReviewComment,
}

// cmdGitHub runs the GitHub actions a reply cannot express. None of them
// touches the inbox: reply (or done) settles the message.
func cmdGitHub(ctx context.Context, project string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", errUsage, githubUsage)
	}

	fs := flag.NewFlagSet("github "+args[0], flag.ContinueOnError)
	notPlanned := fs.Bool("not-planned", false, "close: the issue will not be done (rather than completed)")
	method := fs.String("method", github.MergeSquash, "merge: squash, merge or rebase")

	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return err
	}

	if len(pos) == 0 {
		return fmt.Errorf("%w: %s", errUsage, githubUsage)
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	ch, ok := a.channels[github.Name].(*github.Channel)
	if !ok {
		return errors.New("github channel not configured: run `claude-bot init --channel github`")
	}

	id := pos[0]

	switch args[0] {
	case "close":
		if err := ch.Close(ctx, id, *notPlanned); err != nil {
			return err
		}

		fmt.Println("closed", id)
	case "merge":
		sha, err := ch.Merge(ctx, id, *method)
		if err != nil {
			return err
		}

		fmt.Println("merged", id, sha)
	case "review":
		return review(ctx, ch, id, pos[1:])
	case "mine":
		return mine(ctx, ch, id)
	default:
		return fmt.Errorf("%w: unknown action %q: %s", errUsage, args[0], githubUsage)
	}

	return nil
}

func review(ctx context.Context, ch *github.Channel, id string, args []string) error {
	if len(args) == 0 || reviewEvents[args[0]] == "" {
		return fmt.Errorf("%w: %s", errUsage, reviewUsage)
	}

	event := reviewEvents[args[0]]

	var body string

	if len(args) > 1 || event != github.ReviewApprove {
		text, err := textArg(args[1:])
		if err != nil {
			return err
		}

		body = strings.TrimSpace(text)
	}

	if err := ch.Review(ctx, id, event, body); err != nil {
		return err
	}

	fmt.Println("reviewed", id, args[0])

	return nil
}

// mine prints who opened the PR and fails unless the bot did: its GitHub
// account (the token's, "github.login" in status) and with the marker.
func mine(ctx context.Context, ch *github.Channel, id string) error {
	a, err := ch.Authorship(ctx, id)
	if err != nil {
		return err
	}

	fmt.Println("author ", a.Author)
	fmt.Println("account", a.Account)
	fmt.Println("marker ", yesNo(a.Marked))
	fmt.Println("mine   ", yesNo(a.Own))

	if !a.Own {
		return fmt.Errorf("%s: %w", id, errNotMine)
	}

	return nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

// parseInterleaved parses flags given before, between or after the
// positional arguments, which it returns in order. A lone "-" (stdin) is
// positional.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string

	for {
		if err := fs.Parse(args); err != nil {
			return nil, fmt.Errorf("%w: %w", errUsage, err)
		}

		if fs.NArg() == 0 {
			return pos, nil
		}

		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
