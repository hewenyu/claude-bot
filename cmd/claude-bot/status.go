package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
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
	Pending  int             `json:"pending"`   // tasks waiting for Claude
	Jev      bool            `json:"jev"`       // a TypeSafe key is set up; messages wait for Jev without one
	Incoming int             `json:"screening"` // messages waiting for Jev, which Claude cannot see yet
	Lark     *larkStatus     `json:"lark,omitempty"`
	Discord  *discordStatus  `json:"discord,omitempty"`
	Telegram *telegramStatus `json:"telegram,omitempty"`
	Slack    *slackStatus    `json:"slack,omitempty"`
	GitHub   *githubStatus   `json:"github,omitempty"`
	DingTalk *dingtalkStatus `json:"dingtalk,omitempty"`
}

type listenerStatus struct {
	Alive    bool      `json:"alive"`
	PID      int       `json:"pid"`
	LastBeat time.Time `json:"last_beat"`
	Channels []string  `json:"channels,omitempty"` // what it serves; `claude-bot restart` picks up newly configured ones
	Stopped  []string  `json:"stopped,omitempty"`  // failed while the others kept running; see serve.log
	Version  string    `json:"version,omitempty"`
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

// dingtalkStatus is the DingTalk app, or why its credentials were refused.
type dingtalkStatus struct {
	dingtalk.App

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

	pending, err := a.inbox.PendingTasks()
	if err != nil {
		return err
	}

	incoming, err := a.inbox.Incoming()
	if err != nil {
		return err
	}

	hb, alive := readHeartbeat(a.dir)
	s := status{
		Project:  filepath.Dir(a.dir),
		Channels: a.names(),
		Listener: listenerStatus{Alive: alive, PID: hb.PID, LastBeat: hb.BeatAt, Channels: hb.Channels, Stopped: hb.Stopped, Version: hb.Version},
		Pending:  len(pending),
		Jev:      a.typesafe != nil,
		Incoming: len(incoming),
	}

	if bot, errText, ok := identity(ctx, a, lark.Name, (*lark.Channel).Bot); ok {
		s.Lark = &larkStatus{Bot: bot, Error: errText}
	}

	if bot, errText, ok := identity(ctx, a, discord.Name, (*discord.Channel).Bot); ok {
		s.Discord = &discordStatus{Bot: bot, Error: errText}
		if errText == "" {
			s.Discord.InviteURL = bot.InviteURL()
		}
	}

	if bot, errText, ok := identity(ctx, a, telegram.Name, (*telegram.Channel).Bot); ok {
		s.Telegram = &telegramStatus{Bot: bot, Error: errText}
		if errText == "" {
			s.Telegram.Link = bot.Link()
		}
	}

	if bot, errText, ok := identity(ctx, a, slack.Name, (*slack.Channel).Bot); ok {
		s.Slack = &slackStatus{Bot: bot, Error: errText}
	}

	if acct, errText, ok := identity(ctx, a, github.Name, (*github.Channel).Account); ok {
		s.GitHub = &githubStatus{Account: acct, Error: errText}
	}

	if app, errText, ok := identity(ctx, a, dingtalk.Name, (*dingtalk.Channel).App); ok {
		s.DingTalk = &dingtalkStatus{App: app, Error: errText}
	}

	return printJSON(s)
}

// identity asks the channel configured under name who it is, with who.
// errText is why it could not tell; ok is false if the channel is not
// configured.
func identity[C channel.Channel, I any](ctx context.Context, a *app, name string, who func(C, context.Context) (I, error)) (id I, errText string, ok bool) {
	ch, ok := a.channels[name].(C)
	if !ok {
		return id, "", false
	}

	id, err := who(ch, ctx)
	if err != nil {
		errText = err.Error()
	}

	return id, errText, true
}
