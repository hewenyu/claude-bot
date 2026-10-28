package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
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

var errNotConfigured = errors.New("no channel configured: run `claude-bot init` in the project (or `claude-bot init --channel <name>`); " +
	"or set LARK_APP_ID and LARK_APP_SECRET, DISCORD_BOT_TOKEN, TELEGRAM_BOT_TOKEN, or SLACK_APP_TOKEN and SLACK_BOT_TOKEN")

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

	channels, err := openChannels(cfg, log)
	if err != nil {
		return nil, err
	}

	if len(channels) == 0 {
		return nil, errNotConfigured
	}

	return assemble(dir, log, channels...), nil
}

// openChannels builds the adapters of the configured channels.
func openChannels(cfg config.Config, log *slog.Logger) ([]channel.Channel, error) {
	var chs []channel.Channel

	err := errors.Join(
		addChannel(&chs, cfg.Lark, lark.New, log),
		addChannel(&chs, cfg.Discord, discord.New, log),
		addChannel(&chs, cfg.Telegram, telegram.New, log),
		addChannel(&chs, cfg.Slack, slack.New, log),
		addChannel(&chs, cfg.GitHub, github.New, log),
	)

	return chs, err
}

// addChannel appends the adapter newChannel builds for cfg, if it is set.
func addChannel[C any, T channel.Channel](chs *[]channel.Channel, cfg *C, newChannel func(C, *slog.Logger) (T, error), log *slog.Logger) error {
	if cfg == nil {
		return nil
	}

	ch, err := newChannel(*cfg, log)
	if err != nil {
		return err
	}

	*chs = append(*chs, ch)

	return nil
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
