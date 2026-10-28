package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
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
	GitHub   *githubStatus   `json:"github,omitempty"`
}

type listenerStatus struct {
	Alive    bool      `json:"alive"`
	PID      int       `json:"pid"`
	LastBeat time.Time `json:"last_beat"`
	Channels []string  `json:"channels,omitempty"` // what it serves; the next inbox restarts it if they differ from the configured ones
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

// githubStatus is the account the GitHub token acts as, or why it could not
// be checked.
type githubStatus struct {
	github.Account

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
		Listener: listenerStatus{Alive: alive, PID: hb.PID, LastBeat: hb.BeatAt, Channels: hb.Channels},
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

	s.Slack = slackIdentity(ctx, a)
	s.GitHub = githubIdentity(ctx, a)

	return printJSON(s)
}

func slackIdentity(ctx context.Context, a *app) *slackStatus {
	ch, ok := a.channels[slack.Name].(*slack.Channel)
	if !ok {
		return nil
	}

	bot, err := ch.Bot(ctx)
	s := &slackStatus{Bot: bot}

	if err != nil {
		s.Error = err.Error()
	}

	return s
}

func githubIdentity(ctx context.Context, a *app) *githubStatus {
	ch, ok := a.channels[github.Name].(*github.Channel)
	if !ok {
		return nil
	}

	acct, err := ch.Account(ctx)
	s := &githubStatus{Account: acct}

	if err != nil {
		s.Error = err.Error()
	}

	return s
}
