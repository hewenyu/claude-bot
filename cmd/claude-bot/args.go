package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Command lines. Flags may come before the positional arguments, or, for
// commands that parse them interleaved (github), anywhere among them. The
// text of a message (reply, send, review) is taken as written, never parsed
// for flags: a Markdown list item ("- fixed") or a "--flag" quoted in it
// must reach the chat as it is.

// arity is how many positional arguments a command takes.
type arity struct {
	least, most int // most < 0: no limit
}

func exactly(n int) arity { return arity{n, n} }
func atLeast(n int) arity { return arity{n, -1} }

func (n arity) allows(args []string) bool {
	return len(args) >= n.least && (n.most < 0 || len(args) <= n.most)
}

// parseFlags parses args into fs; a bad flag is a usage error.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	return nil
}

// parseCommand parses the arguments of a command into fs, nil for a command
// without flags (so that -h prints help and a stray flag is a usage error
// rather than an id), and returns the positional ones, which n must allow;
// usage, starting with the command's name, is the error otherwise.
func parseCommand(fs *flag.FlagSet, args []string, n arity, usage string) ([]string, error) {
	if fs == nil {
		name, _, _ := strings.Cut(usage, " <")
		fs = flag.NewFlagSet(name, flag.ContinueOnError)
	}

	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}

	if !n.allows(fs.Args()) {
		return nil, fmt.Errorf("%w: %s", errUsage, usage)
	}

	return fs.Args(), nil
}

// openApp parses a command's arguments as parseCommand does, then opens
// the project's app.
func openApp(ctx context.Context, project string, fs *flag.FlagSet, args []string, n arity, usage string) (*app, []string, error) {
	pos, err := parseCommand(fs, args, n, usage)
	if err != nil {
		return nil, nil, err
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return nil, nil, err
	}

	return a, pos, nil
}

// parseInterleaved parses flags given before, between or after the
// positional arguments, which it returns in order. A lone "-" (stdin) is
// positional. Once there are literal positional arguments, the rest are
// taken as they are, flags or not: message text; literal 0 parses all.
func parseInterleaved(fs *flag.FlagSet, args []string, literal int) ([]string, error) {
	var pos []string

	for {
		if err := parseFlags(fs, args); err != nil {
			return nil, err
		}

		if fs.NArg() == 0 {
			return pos, nil
		}

		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]

		if literal > 0 && len(pos) == literal {
			return append(pos, args...), nil
		}
	}
}

// replyArgs is a parsed `reply [--keep] <id> [text|-]`.
type replyArgs struct {
	id   string
	text []string
	keep bool
}

const replyUsage = "reply <id> [text|-]"

// parseReplyArgs parses reply's arguments. Its text is literal (see above);
// only a --keep right after the id, where people tend to put it, is still
// taken as the flag rather than sent.
func parseReplyArgs(args []string) (replyArgs, error) {
	var r replyArgs

	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.BoolVar(&r.keep, "keep", false, "leave the message pending (e.g. for an interim \"looking into it\" reply)")

	pos, err := parseCommand(fs, args, atLeast(1), replyUsage)
	if err != nil {
		return r, err
	}

	r.id, r.text = pos[0], pos[1:]

	if len(r.text) > 0 && (r.text[0] == "--keep" || r.text[0] == "-keep") {
		r.keep, r.text = true, r.text[1:]
	}

	return r, nil
}

// textArg returns the message text from args, or from stdin when args is
// empty or "-" (handy for multi-line text in a heredoc).
func textArg(args []string) (string, error) {
	fromStdin := len(args) == 0 || len(args) == 1 && args[0] == "-"
	if !fromStdin {
		return strings.Join(args, " "), nil
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}

	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("%w: empty message text", errUsage)
	}

	return text, nil
}
