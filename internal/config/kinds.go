package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
)

// Kind is one kind of channel, with what tells it from the others: how it
// is configured, from the environment and in the gateway, and how it
// opens. Everything else handles every kind alike, in the order of Kinds.
type Kind struct {
	Name  string // the channel: its ids' prefix, and init's --channel
	Label string // for people
	// Env are the environment variables that, all set, replace its stored
	// credentials; OptionalEnv then change its other settings too.
	Env         []string
	OptionalEnv []string

	has    func(Config) bool
	decode func(*Config, json.RawMessage) error
	// encode returns its configuration as the gateway stores it, and its
	// app key.
	encode  func(Config) (json.RawMessage, string, error)
	open    func(Config, *slog.Logger) (channel.Channel, error)
	fromEnv func(*Config, func(string) string)
}

// kinds are in the order init offers them; the first is the default.
var kinds = []Kind{
	kind(lark.Name, "飞书 / Lark", func(c *Config) **lark.Config { return &c.Lark }, lark.Config.AppKey, lark.New,
		env[lark.Config]{names: []string{larkAppIDEnv, larkSecretEnv}, optional: []string{larkDomainEnv}, set: func(c *lark.Config, v []string) {
			c.AppID, c.AppSecret = v[0], v[1]
			if v[2] != "" {
				c.Domain = v[2]
			}
		}}),
	kind(discord.Name, "Discord", func(c *Config) **discord.Config { return &c.Discord }, discord.Config.AppKey, discord.New,
		env[discord.Config]{names: []string{discordTokenEnv}, set: func(c *discord.Config, v []string) { c.Token = v[0] }}),
	kind(telegram.Name, "Telegram", func(c *Config) **telegram.Config { return &c.Telegram }, telegram.Config.AppKey, telegram.New,
		env[telegram.Config]{names: []string{telegramTokenEnv}, set: func(c *telegram.Config, v []string) { c.Token = v[0] }}),
	kind(slack.Name, "Slack", func(c *Config) **slack.Config { return &c.Slack }, slack.Config.AppKey, slack.New,
		env[slack.Config]{names: []string{slackAppTokenEnv, slackBotTokenEnv}, set: func(c *slack.Config, v []string) { c.AppToken, c.BotToken = v[0], v[1] }}),
	githubKind(),
	kind(dingtalk.Name, "钉钉 / DingTalk", func(c *Config) **dingtalk.Config { return &c.DingTalk }, dingtalk.Config.AppKey, dingtalk.New,
		env[dingtalk.Config]{names: []string{dingtalkIDEnv, dingtalkSecretEnv}, set: func(c *dingtalk.Config, v []string) { c.ClientID, c.ClientSecret = v[0], v[1] }}),
}

// env is how a kind of channel is configured from the environment.
type env[C any] struct {
	names    []string
	optional []string
	// set applies the values of names, all set, then of optional, "" if
	// unset.
	set func(c *C, values []string)
}

// kind describes a kind of channel whose configuration is field of
// Config, stored as it is.
func kind[C any, T channel.Channel](name, label string, field func(*Config) **C, appKey func(C) string,
	open func(C, *slog.Logger) (T, error), vars env[C],
) Kind {
	return Kind{
		Name: name, Label: label, Env: vars.names, OptionalEnv: vars.optional,
		has: func(c Config) bool { return *field(&c) != nil },
		decode: func(c *Config, raw json.RawMessage) error {
			v := new(C)
			*field(c) = v

			return json.Unmarshal(raw, v)
		},
		encode: func(c Config) (json.RawMessage, string, error) {
			v := *field(&c)
			raw, err := json.Marshal(v)

			return raw, appKey(*v), err
		},
		open: func(c Config, log *slog.Logger) (channel.Channel, error) { return open(**field(&c), log) },
		fromEnv: func(c *Config, getenv func(string) string) {
			if len(vars.names) == 0 {
				return
			}

			values := make([]string, 0, len(vars.names)+len(vars.optional))

			for _, n := range vars.names {
				v := getenv(n)
				if v == "" {
					return
				}

				values = append(values, v)
			}

			for _, n := range vars.optional {
				values = append(values, getenv(n))
			}

			f := field(c)
			if *f == nil {
				*f = new(C)
			}

			vars.set(*f, values)
		},
	}
}

// githubKind is the GitHub channel, which the gateway stores without its
// token, with the name of the one it uses: the gateway keeps the tokens
// apart, for all the projects that use them.
func githubKind() Kind {
	k := kind(github.Name, "GitHub", func(c *Config) **github.Config { return &c.GitHub }, github.Config.AppKey, github.New, env[github.Config]{})

	k.decode = func(c *Config, raw json.RawMessage) error {
		var gh storedGitHub
		if err := json.Unmarshal(raw, &gh); err != nil {
			return err
		}

		c.GitHub, c.GitHubTokenName = &gh.Config, gh.TokenName

		return nil
	}
	k.encode = func(c Config) (json.RawMessage, string, error) {
		gh := storedGitHub{Config: *c.GitHub, TokenName: c.GitHubTokenName}
		gh.Token = ""
		raw, err := json.Marshal(gh)

		return raw, gh.AppKey(), err
	}

	return k
}

// storedGitHub is the GitHub channel as the gateway stores it: without the
// token, with the name of the one it uses.
type storedGitHub struct {
	github.Config

	TokenName string `json:"token_name,omitempty"`
}

// Kinds returns every kind of channel, in the order init offers them; the
// first is the default.
func Kinds() []Kind { return slices.Clone(kinds) }

// KindOf returns the kind of channel called name.
func KindOf(name string) (Kind, bool) {
	i := slices.IndexFunc(kinds, func(k Kind) bool { return k.Name == name })
	if i < 0 {
		return Kind{}, false
	}

	return kinds[i], true
}

// KindNames lists the kinds of channel in order, joined by sep.
func KindNames(sep string) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.Name
	}

	return strings.Join(names, sep)
}

// ErrNotConfigured reports a project without channels.
var ErrNotConfigured = errors.New("no channel configured: run `claude-bot init` in the project (or `claude-bot init --channel <name>`); or set " + envChoices())

// envChoices lists the environment variables that configure a channel.
func envChoices() string {
	var choices []string

	for _, k := range kinds {
		if len(k.Env) == 0 {
			continue
		}

		c := strings.Join(k.Env, " and ")
		if len(k.OptionalEnv) > 0 {
			c += " (and optionally " + strings.Join(k.OptionalEnv, ", ") + ")"
		}

		choices = append(choices, c)
	}

	if n := len(choices); n > 1 {
		choices[n-1] = "or " + choices[n-1]
	}

	return strings.Join(choices, ", ")
}

// Has reports whether c sets up the channel of that name.
func (c Config) Has(name string) bool {
	k, ok := KindOf(name)

	return ok && k.has(c)
}

// HasAny reports whether c sets up any channel.
func (c Config) HasAny() bool {
	return slices.ContainsFunc(kinds, func(k Kind) bool { return k.has(c) })
}

// Open builds the adapters of the channels c sets up.
func (c Config) Open(log *slog.Logger) ([]channel.Channel, error) {
	var (
		chs  []channel.Channel
		errs []error
	)

	for _, k := range kinds {
		if !k.has(c) {
			continue
		}

		ch, err := k.open(c, log)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		chs = append(chs, ch)
	}

	return chs, errors.Join(errs...)
}

// applyEnv applies the channel credentials the environment sets.
func applyEnv(cfg *Config) {
	for _, k := range kinds {
		k.fromEnv(cfg, os.Getenv)
	}
}

func (c *Config) decodeChannel(name string, raw json.RawMessage) error {
	k, ok := KindOf(name)
	if !ok {
		return fmt.Errorf("unknown channel %q: upgrade claude-bot", name)
	}

	if err := k.decode(c, raw); err != nil {
		return fmt.Errorf("channel %s: %w", name, err)
	}

	return nil
}
