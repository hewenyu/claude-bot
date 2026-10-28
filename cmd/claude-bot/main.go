// Command claude-bot connects a project's chat bot (Lark / Feishu today) to a
// Claude Code session running /loop in that project's repository.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hewenyu/claude-bot/internal/config"
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

For Claude (inside /loop):
  claude-bot inbox [--wait 9m]        pending messages as JSON lines (starts the listener if needed)
  claude-bot reply <id> [text|-]      reply in place (Markdown; @-mentions the sender in groups); marks done
  claude-bot done <id>...             mark handled without replying
  claude-bot get <id>                 fetch any message, e.g. the one in "reply_to"
  claude-bot download <id>            save a message's images/files, print paths
  claude-bot send <chat-id> [text|-]  post to a chat, e.g. lark:oc_xxx

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

type command func(ctx context.Context, args []string) error

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
		"version": func(context.Context, []string) error {
			fmt.Println(Version)

			return nil
		},
	}

	if len(args) >= 2 && args[0] == "-C" {
		if err := os.Setenv(config.ProjectEnv, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "claude-bot: %v\n", err)

			return exitFailure
		}

		args = args[2:]
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

	if err := cmd(ctx, args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "claude-bot %s: %v\n", args[0], err)

		if errors.Is(err, errUsage) {
			return exitUsage
		}

		return exitFailure
	}

	return exitOK
}
