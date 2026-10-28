// Command claude-bot connects a project's chat bot (Lark / Feishu, Discord) to a
// Claude Code session running /loop in that project's repository.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Version is set at build time with -ldflags "-X main.Version=...".
var Version = "dev"

const usage = `claude-bot — chat channel for a Claude Code session

Usage: claude-bot [-C <repo>] <command> [args]
  -C <repo>   act on that repository (default: the git repo of the current directory)

Setup (once per project; config stays in <repo>/.claude-bot/, git-ignored):
  claude-bot init                     create the bot app in the browser (just confirm), save credentials,
                                      generate .claude/commands/bot.md, verify with a test message
  claude-bot init --app-id cli_x --app-secret s   use an existing app instead
  claude-bot init --discord-token t   set up a Discord bot (token from the Developer Portal) instead

For Claude (inside /loop):
  claude-bot inbox [--wait 9m]        pending messages as JSON lines (starts the listener if needed)
  claude-bot reply <id> [text|-]      reply in place (Markdown; @-mentions the sender in groups); marks done
  claude-bot done <id>...             mark handled without replying
  claude-bot get <id>                 fetch any message, e.g. the one in "reply_to"
  claude-bot download <id>            save a message's images/files, print paths
  claude-bot send <chat-id> [text|-]  post to a chat, e.g. lark:oc_xxx or discord:123

Operations:
  claude-bot serve                    run the listener in the foreground
  claude-bot status                   bot identity, listener health, pending count
  claude-bot version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])

	stop()
	os.Exit(code)
}

// Exit codes, as documented in PROTOCOL.md.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// command runs a subcommand. project is the repository chosen with -C, or ""
// to locate it from the working directory.
type command func(ctx context.Context, project string, args []string) error

var errUsage = errors.New("usage")

func run(ctx context.Context, args []string) int {
	commands := map[string]command{
		"init":     cmdInit,
		"serve":    cmdServe,
		"inbox":    cmdInbox,
		"reply":    cmdReply,
		"done":     cmdDone,
		"get":      cmdGet,
		"download": cmdDownload,
		"send":     cmdSend,
		"status":   cmdStatus,
		"version": func(context.Context, string, []string) error {
			fmt.Println(Version)

			return nil
		},
	}

	var project string
	if len(args) >= 2 && args[0] == "-C" {
		project, args = args[1], args[2:]
	}

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)

		return exitOK
	}

	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "claude-bot: unknown command %q\n\n%s", args[0], usage)

		return exitUsage
	}

	if err := cmd(ctx, project, args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "claude-bot %s: %v\n", args[0], err)

		if errors.Is(err, errUsage) {
			return exitUsage
		}

		return exitFailure
	}

	return exitOK
}

// parseArgs parses the arguments of a command that takes no flags, so -h
// prints help and a stray flag is a usage error rather than an id.
func parseArgs(name string, args []string) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	return fs.Args(), nil
}
