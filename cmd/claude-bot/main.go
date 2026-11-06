// Command claude-bot connects a project's chat bot (Lark / Feishu, Discord,
// Telegram, Slack, DingTalk) and its GitHub issues and PRs to a Claude Code
// session running /loop in that project's repository.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/hewenyu/claude-bot/internal/config"
)

// Version is set at build time with -ldflags "-X main.Version=...". Without
// it, init fills it in from what the Go toolchain recorded in the binary.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if v := buildVersion(info); v != "" {
			Version = v
		}
	}
}

// buildVersion names the build from its build info: the module version for
// `go install …@v0.2.6` (and, since Go 1.24, for `go build` in a checkout:
// the tag, or a pseudo-version after it), else the VCS revision, "" if neither.
func buildVersion(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var rev, dirty string

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}

	if rev == "" {
		return ""
	}

	if len(rev) > 12 {
		rev = rev[:12]
	}

	return "dev-" + rev + dirty
}

// usageHead starts the usage; the commands follow, by section.
const usageHead = `claude-bot — chat channel for a Claude Code session

Usage: claude-bot [-C <repo>] <command> [args]
  -C <repo>   act on that repository (default: the git repo of the current directory)
`

// commandSpec is a command, with its lines in the usage.
type commandSpec struct {
	name string
	run  command
	// help is its lines in the usage; "" leaves it out, as another
	// command's lines cover it or it is not for people.
	help string
}

// section is a part of the usage and its commands, in order.
type section struct {
	title    string
	commands []commandSpec
}

// sections are every command and the usage, from one table, so that the
// two cannot drift apart.
func sections() []section {
	return []section{
		{"Setup (once per project; the configuration goes into the gateway, ~/.claude-bot/gateway.db,\nand messages into <repo>/.claude-bot/, git-ignored):", []commandSpec{
			{"init", cmdInit, `  claude-bot init                     pick a channel (on a terminal; Lark otherwise), set it up, save credentials,
                                      generate .claude/commands/bot.md, verify with a test message
  claude-bot init --channel <name>    set up ` + channelList() + `, asking for its tokens
  claude-bot init --app-id cli_x --app-secret s   use an existing Lark app
  claude-bot init --discord-token t   set up a Discord bot (token from the Developer Portal)
  claude-bot init --telegram-token t  set up a Telegram bot (token from @BotFather)
  claude-bot init --slack-app-token xapp-… --slack-bot-token xoxb-…   set up a Slack app
  claude-bot init --dingtalk-client-id id --dingtalk-client-secret s   set up a DingTalk app's robot
  claude-bot init --github-token t [--github-repo o/r]   receive the repository's issues, PRs, comments and reviews
  claude-bot init --bot-md            only overwrite .claude/commands/bot.md with this version's`},
		}},
		{"Gateway (run these yourself, in your own terminal, so it keeps your environment):", []commandSpec{
			{"gateway", cmdGateway, `  claude-bot gateway start            run the gateway in the background: one process serving every registered project
  claude-bot gateway stop             shut it down
  claude-bot gateway restart          replace it, e.g. after an upgrade
  claude-bot gateway status           the gateway and every registered project
  claude-bot gateway open [--print]   show the gateway's page in the browser (--print: only print its address)
  claude-bot gateway import [<repo>...]   move a project set up before the gateway into it
  claude-bot gateway remove <project> unregister a project (its configuration goes back to its config.json)
  claude-bot gateway serve            run the gateway in the foreground`},
		}},
		{"Listener of a project not registered with the gateway (set up before it):", []commandSpec{
			{"start", cmdStart, `  claude-bot start | stop | restart   run, shut down or replace the project's own listener (log: .claude-bot/serve.log)`},
			{"stop", cmdStop, ""},
			{"restart", cmdRestart, ""},
			{"serve", cmdServe, `  claude-bot serve                    run the project's own listener in the foreground`},
		}},
		{"For Claude (inside /loop):", []commandSpec{
			{"inbox", cmdInbox, `  claude-bot inbox [--wait 55m]       pending messages as JSON lines (fails if the listener is not running)`},
			{"reply", cmdReply, `  claude-bot reply <id> [text|-]      reply in place (Markdown; @-mentions the sender in groups); marks done`},
			{"done", cmdDone, `  claude-bot done <id>...             mark handled without replying`},
			{"get", cmdGet, `  claude-bot get <id>                 fetch any message, e.g. the one in "reply_to"`},
			{"download", cmdDownload, `  claude-bot download <id>            save a message's images/files, print paths`},
			{"send", cmdSend, `  claude-bot send <chat-id> [text|-]  post to a chat, e.g. lark:oc_xxx, discord:123, telegram:-100123, dingtalk:cid…, github:o/r/42`},
			{"finish", cmdFinish, `  claude-bot finish                   in an agent the gateway runs: this round's batch is done (refused while a task
                                      of it has no reply); the gateway ends the session once the turn ends`},
			{"github", cmdGitHub, `  claude-bot github close <id> [--not-planned]                 close the issue or PR of a github: id
  claude-bot github merge <id> [--method squash|merge|rebase]  merge the PR
  claude-bot github review <id> approve|request-changes|comment [text|-]   review the PR
  claude-bot github mine <id>         is the PR the bot's own (opened by its account, marked)? exit 0 if so, 1 if not`},
			// The agents the gateway runs report their hooks with it.
			{"hook", cmdHook, ""},
		}},
		{"Operations:", []commandSpec{
			{"status", cmdStatus, `  claude-bot status                   bot identity, listener health, pending count`},
			{"version", cmdVersion, `  claude-bot version`},
		}},
	}
}

// usage is the help claude-bot prints.
func usage() string {
	var b strings.Builder

	b.WriteString(usageHead)

	for _, s := range sections() {
		b.WriteString("\n" + s.title + "\n")

		for _, c := range s.commands {
			if c.help != "" {
				b.WriteString(c.help + "\n")
			}
		}
	}

	return b.String()
}

// commandNamed returns the command called name.
func commandNamed(name string) (command, bool) {
	for _, s := range sections() {
		for _, c := range s.commands {
			if c.name == name {
				return c.run, true
			}
		}
	}

	return nil, false
}

// channelList names the channels init sets up, for the usage.
func channelList() string {
	names := strings.Split(config.KindNames(","), ",")

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func cmdVersion(context.Context, string, []string) error {
	stdout.println(Version)

	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// Once the first signal starts the shutdown, a second one kills the
	// process the default way, in case the shutdown hangs.
	go func() {
		<-ctx.Done()
		stop()
	}()

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

// Commands the messages ask people to run: as the Chinese ones show them,
// between spaces, and quoted as the English ones do.
const (
	lineGatewayStart = "claude-bot gateway start"
	lineStop         = "claude-bot stop"

	hintInit           = "`claude-bot init`"
	hintStart          = "`claude-bot start`"
	hintStop           = "`" + lineStop + "`"
	hintRestart        = "`claude-bot restart`"
	hintGatewayStart   = "`" + lineGatewayStart + "`"
	hintGatewayStop    = "`claude-bot gateway stop`"
	hintGatewayStatus  = "`claude-bot gateway status`"
	hintGatewayRestart = "`claude-bot gateway restart`"
)

func run(ctx context.Context, args []string) int {
	var project string
	if len(args) >= 2 && args[0] == "-C" {
		project, args = args[1], args[2:]
	}

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage())

		return exitOK
	}

	cmd, ok := commandNamed(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "claude-bot: unknown command %q\n\n%s", args[0], usage())

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
