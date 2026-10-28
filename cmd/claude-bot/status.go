package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
)

// status is the output of `claude-bot status`.
type status struct {
	Project  string         `json:"project"`
	Channels []string       `json:"channels"`
	Listener listenerStatus `json:"listener"`
	Pending  int            `json:"pending"`
	Lark     *botStatus     `json:"lark,omitempty"`
	Discord  *discordStatus `json:"discord,omitempty"`
}

type listenerStatus struct {
	Alive    bool      `json:"alive"`
	PID      int       `json:"pid"`
	LastBeat time.Time `json:"last_beat"`
}

// botStatus is the bot's identity, or why it could not be fetched.
type botStatus struct {
	lark.Bot

	Error string `json:"error,omitempty"`
}

// discordStatus is the Discord bot's identity and invite link, or why they
// could not be fetched.
type discordStatus struct {
	discord.Bot

	InviteURL string `json:"invite_url,omitempty"`
	Error     string `json:"error,omitempty"`
}

func cmdStatus(ctx context.Context, project string, args []string) error {
	if _, err := parseArgs("status", args); err != nil {
		return err
	}

	a, err := newApp(ctx, project)
	if err != nil {
		return err
	}

	pending, err := a.inbox.Pending()
	if err != nil {
		return err
	}

	hb, alive := readHeartbeat(a.dir)
	s := status{
		Project:  filepath.Dir(a.dir),
		Channels: a.names(),
		Listener: listenerStatus{Alive: alive, PID: hb.PID, LastBeat: hb.BeatAt},
		Pending:  len(pending),
	}

	if ch, ok := a.channels[lark.Name].(*lark.Channel); ok {
		bot, err := ch.Bot(ctx)
		s.Lark = &botStatus{Bot: bot}

		if err != nil {
			s.Lark.Error = err.Error()
		}
	}

	if ch, ok := a.channels[discord.Name].(*discord.Channel); ok {
		bot, err := ch.Bot(ctx)
		s.Discord = &discordStatus{Bot: bot}

		if err != nil {
			s.Discord.Error = err.Error()
		} else {
			s.Discord.InviteURL = bot.InviteURL()
		}
	}

	return printJSON(s)
}
