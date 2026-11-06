package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
)

// app bundles what every command needs.
type app struct {
	dir      string // <project>/.claude-bot
	inbox    *inbox.Inbox
	channels map[string]channel.Channel
	log      *slog.Logger
	typesafe *jev.Config // nil until init sets it up
	// gateway is the project's registration with the gateway, which then
	// holds its configuration and serves it; nil for a project configured
	// by its own config.json.
	gateway *store.Project
}

func newApp(ctx context.Context, project string) (*app, error) {
	dir, err := config.Dir(ctx, project)
	if err != nil {
		return nil, err
	}

	cfg, reg, err := config.LoadProject(ctx, dir)
	if err != nil {
		return nil, err
	}

	a, err := appFor(dir, cfg, newLogger())
	if err != nil {
		return nil, err
	}

	a.gateway = reg

	return a, nil
}

// appFor builds the app of the project whose bot directory is dir.
func appFor(dir string, cfg config.Config, log *slog.Logger) (*app, error) {
	channels, err := cfg.Open(log)
	if err != nil {
		return nil, err
	}

	if len(channels) == 0 {
		return nil, config.ErrNotConfigured
	}

	a := assemble(dir, log, channels...)
	a.typesafe = cfg.TypeSafe

	return a, nil
}

// assemble builds an app over already constructed channels.
func assemble(dir string, log *slog.Logger, channels ...channel.Channel) *app {
	a := &app{
		dir:      dir,
		inbox:    inboxOf(dir),
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
