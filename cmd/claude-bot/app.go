package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

var errNotConfigured = errors.New("no channel configured: run `claude-bot init` in the project, or set LARK_APP_ID and LARK_APP_SECRET")

func newApp(ctx context.Context) (*app, error) {
	dir, err := config.Dir(ctx)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	a := &app{dir: dir, inbox: inbox.New(filepath.Join(dir, "inbox")), channels: map[string]channel.Channel{}, log: log}

	if cfg.Lark != nil {
		ch, err := lark.New(*cfg.Lark, log)
		if err != nil {
			return nil, err
		}

		a.channels[ch.Name()] = ch
	}

	if len(a.channels) == 0 {
		return nil, errNotConfigured
	}

	return a, nil
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

func (a *app) names() []string {
	out := make([]string, 0, len(a.channels))
	for n := range a.channels {
		out = append(out, n)
	}

	return out
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
