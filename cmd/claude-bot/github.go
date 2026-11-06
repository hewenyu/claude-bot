package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel/github"
)

// errNotMine is the verdict of "github mine" on a PR the bot did not open.
var errNotMine = errors.New("not the bot's own PR: audit it as someone else's")

// githubAction is one of what `claude-bot github` does: its arguments, and
// what it does with them.
type githubAction struct {
	name  string
	usage string // after "github "
	args  arity
	// literal is how many positional arguments come before the text,
	// which is taken as written; 0 if the action takes no text.
	literal int
	// flags defines the action's flags in fs and returns what it does.
	flags func(fs *flag.FlagSet) func(ctx context.Context, ch *github.Channel, pos []string) error
}

// githubActions are what `claude-bot github` does.
var githubActions = []githubAction{
	{name: "close", usage: "close <id> [--not-planned]", args: exactly(1), flags: func(fs *flag.FlagSet) func(context.Context, *github.Channel, []string) error {
		notPlanned := fs.Bool("not-planned", false, "the issue will not be done (rather than completed)")

		return func(ctx context.Context, ch *github.Channel, pos []string) error {
			if err := ch.Close(ctx, pos[0], *notPlanned); err != nil {
				return err
			}

			fmt.Println("closed", pos[0])

			return nil
		}
	}},
	{name: "merge", usage: "merge <id> [--method squash|merge|rebase]", args: exactly(1), flags: func(fs *flag.FlagSet) func(context.Context, *github.Channel, []string) error {
		method := fs.String("method", github.MergeSquash, "squash, merge or rebase")

		return func(ctx context.Context, ch *github.Channel, pos []string) error {
			sha, err := ch.Merge(ctx, pos[0], *method)
			if err != nil {
				return err
			}

			fmt.Println("merged", pos[0], sha)

			return nil
		}
	}},
	{name: "review", usage: "review <id> approve|request-changes|comment [text|-]", args: atLeast(2), literal: 2, flags: func(*flag.FlagSet) func(context.Context, *github.Channel, []string) error {
		return review
	}},
	{name: "mine", usage: "mine <id>", args: exactly(1), flags: func(*flag.FlagSet) func(context.Context, *github.Channel, []string) error {
		return mine
	}},
}

// githubUsage lists the actions.
func githubUsage() string {
	usages := make([]string, len(githubActions))
	for i, a := range githubActions {
		usages[i] = "github " + a.usage
	}

	return strings.Join(usages, " | ")
}

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
		return fmt.Errorf("%w: %s", errUsage, githubUsage())
	}

	i := slices.IndexFunc(githubActions, func(a githubAction) bool { return a.name == args[0] })
	if i < 0 {
		return fmt.Errorf("%w: unknown action %q: %s", errUsage, args[0], githubUsage())
	}

	action := githubActions[i]

	fs := flag.NewFlagSet("github "+action.name, flag.ContinueOnError)
	do := action.flags(fs)

	pos, err := parseInterleaved(fs, args[1:], action.literal)
	if err != nil {
		return err
	}

	if !action.args.allows(pos) {
		return fmt.Errorf("%w: github %s", errUsage, action.usage)
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	ch, ok := a.channels[github.Name].(*github.Channel)
	if !ok {
		return errors.New("github channel not configured: run `claude-bot init --channel github`")
	}

	return do(ctx, ch, pos)
}

// review reviews the PR pos[0] with the verdict pos[1] and the text after.
func review(ctx context.Context, ch *github.Channel, pos []string) error {
	id, verdict, text := pos[0], pos[1], pos[2:]

	event, ok := reviewEvents[verdict]
	if !ok {
		return fmt.Errorf("%w: unknown verdict %q: github review <id> approve|request-changes|comment [text|-]", errUsage, verdict)
	}

	var body string

	if len(text) > 0 || event != github.ReviewApprove {
		t, err := textArg(text)
		if err != nil {
			return err
		}

		body = strings.TrimSpace(t)
	}

	if err := ch.Review(ctx, id, event, body); err != nil {
		return err
	}

	fmt.Println("reviewed", id, verdict)

	return nil
}

// mine prints who opened the PR and fails unless the bot did: its GitHub
// account (the token's, "github.login" in status) and with the marker.
func mine(ctx context.Context, ch *github.Channel, pos []string) error {
	id := pos[0]

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
