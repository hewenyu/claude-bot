package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
)

// status is the output of `claude-bot status`.
type status struct {
	Project  string          `json:"project"`
	Channels []string        `json:"channels"`
	Listener listenerStatus  `json:"listener"`
	Pending  int             `json:"pending"`
	Lark     *larkStatus     `json:"lark,omitempty"`
	Discord  *discordStatus  `json:"discord,omitempty"`
	Telegram *telegramStatus `json:"telegram,omitempty"`
	Slack    *slackStatus    `json:"slack,omitempty"`
}

type listenerStatus struct {
	Alive    bool      `json:"alive"`
	PID      int       `json:"pid"`
	LastBeat time.Time `json:"last_beat"`
}

// larkStatus is the Lark bot's identity, or why it could not be fetched.
type larkStatus struct {
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

// telegramStatus is the Telegram bot's identity and chat link, or why they
// could not be fetched.
type telegramStatus struct {
	telegram.Bot

	Link  string `json:"link,omitempty"`
	Error string `json:"error,omitempty"`
}

// slackStatus is the Slack bot's identity, or why it could not be fetched.
type slackStatus struct {
	slack.Bot

	Error string `json:"error,omitempty"`
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
		s.Lark = &larkStatus{Bot: bot}

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

	if ch, ok := a.channels[telegram.Name].(*telegram.Channel); ok {
		bot, err := ch.Bot(ctx)
		s.Telegram = &telegramStatus{Bot: bot}

		if err != nil {
			s.Telegram.Error = err.Error()
		} else {
			s.Telegram.Link = bot.Link()
		}
	}

	if ch, ok := a.channels[slack.Name].(*slack.Channel); ok {
		bot, err := ch.Bot(ctx)
		s.Slack = &slackStatus{Bot: bot}

		if err != nil {
			s.Slack.Error = err.Error()
		}
	}

	return printJSON(s)
}
