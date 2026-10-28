package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// app bundles what every command needs.
type app struct {
	dir      string // <project>/.claude-bot
	inbox    *inbox.Inbox
	channels map[string]channel.Channel
	log      *slog.Logger
}

// Layout of the project's .claude-bot directory.
const (
	heartbeatFile = "serve.json"
	listenerLock  = "serve.lock"
	listenerLog   = "serve.log"
	inboxDir      = "inbox"
	filesDir      = "files"
)

// File modes: bot state stays private to the owner; files meant for the team
// (.gitignore, the slash command) are world-readable like the rest of the repo.
const (
	dirPerm     = 0o750
	privatePerm = 0o600
	sharedPerm  = 0o644
)

var errNotConfigured = errors.New("no channel configured: run `claude-bot init` in the project, or set LARK_APP_ID and LARK_APP_SECRET")

func newApp(ctx context.Context, project string) (*app, error) {
	dir, err := config.Dir(ctx, project)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	log := newLogger()

	var channels []channel.Channel

	if cfg.Lark != nil {
		ch, err := lark.New(*cfg.Lark, log)
		if err != nil {
			return nil, err
		}

		channels = append(channels, ch)
	}

	if len(channels) == 0 {
		return nil, errNotConfigured
	}

	return assemble(dir, log, channels...), nil
}

// assemble builds an app over already constructed channels.
func assemble(dir string, log *slog.Logger, channels ...channel.Channel) *app {
	a := &app{
		dir:      dir,
		inbox:    inbox.New(filepath.Join(dir, inboxDir)),
		channels: make(map[string]channel.Channel, len(channels)),
		log:      log,
	}

	for _, ch := range channels {
		a.channels[ch.Name()] = ch
	}

	return a
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// path returns a file in the .claude-bot directory.
func (a *app) path(name string) string {
	return filepath.Join(a.dir, name)
}

// channelFor returns the adapter that owns a protocol id.
func (a *app) channelFor(id string) (channel.Channel, string, error) {
	name, platformID, err := channel.SplitID(id)
	if err != nil {
		return nil, "", err
	}

	ch, ok := a.channels[name]
	if !ok {
		return nil, "", fmt.Errorf("%w %q (configured: %s)", channel.ErrUnknownChannel, name, strings.Join(a.names(), ", "))
	}

	return ch, platformID, nil
}

// names lists the configured channels in a stable order.
func (a *app) names() []string {
	return slices.Sorted(maps.Keys(a.channels))
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

// textArg returns the message text from args, or stdin when args is empty or "-".
func textArg(args []string) (string, error) {
	if len(args) > 1 || len(args) == 1 && args[0] != "-" {
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
