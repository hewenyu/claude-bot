package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

const slackGuide = "https://github.com/hewenyu/claude-bot#接-slack可选"

// channelChoice is one entry of the init menu.
type channelChoice struct {
	name  string // channel id prefix, also the --channel value
	label string
}

// channelChoices are offered in this order; the first is the default.
var channelChoices = []channelChoice{
	{name: lark.Name, label: "飞书 / Lark（在浏览器里确认即可创建应用）"},
	{name: discord.Name, label: "Discord（需要 bot token）"},
	{name: telegram.Name, label: "Telegram（需要 @BotFather 给的 token）"},
	{name: slack.Name, label: "Slack（需要 app token 和 bot token）"},
}

func knownChannel(name string) bool {
	for _, c := range channelChoices {
		if c.name == name {
			return true
		}
	}

	return false
}

// configured reports whether cfg already sets up the channel.
func configured(cfg config.Config, name string) bool {
	switch name {
	case lark.Name:
		return cfg.Lark != nil
	case discord.Name:
		return cfg.Discord != nil
	case telegram.Name:
		return cfg.Telegram != nil
	case slack.Name:
		return cfg.Slack != nil
	}

	return false
}

var errNoInput = errors.New("no answer (stdin closed)")

// prompter asks the person running init; secret reads without echo.
type prompter struct {
	in     *bufio.Reader
	out    io.Writer
	secret func() (string, error)
}

// terminalPrompter asks on the terminal; ok is false when stdin is not one
// (a script, Claude Code's Bash tool), where init must not wait for answers.
func terminalPrompter() (p *prompter, ok bool) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, false
	}

	in := bufio.NewReader(os.Stdin)

	return &prompter{
		in:  in,
		out: os.Stdout,
		secret: func() (string, error) {
			b, err := term.ReadPassword(fd)

			fmt.Println() // the Enter was not echoed

			return string(b), err
		},
	}, true
}

// say writes to the person running init; a broken terminal shows up when
// reading the answer.
func (p *prompter) say(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, format, args...)
}

func (p *prompter) line() (string, error) {
	s, err := p.in.ReadString('\n')

	switch {
	case err == nil:
	case errors.Is(err, io.EOF) && s != "": // a last line without newline
	case errors.Is(err, io.EOF):
		return "", errNoInput
	default:
		return "", fmt.Errorf("read answer: %w", err)
	}

	return strings.TrimSpace(s), nil
}

// chooseChannel shows the menu until a valid number (or Enter for the
// default) is given.
func (p *prompter) chooseChannel(cfg config.Config) (string, error) {
	p.say("选择要接入的渠道（之后可以再运行 init --channel <渠道> 接入其他渠道）：\n")

	for i, c := range channelChoices {
		mark := ""
		if configured(cfg, c.name) {
			mark = "  [已配置]"
		}

		p.say("  %d) %s%s\n", i+1, c.label, mark)
	}

	for {
		p.say("请输入编号 [1]：")

		answer, err := p.line()
		if err != nil {
			return "", err
		}

		if answer == "" {
			return channelChoices[0].name, nil
		}

		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(channelChoices) {
			return channelChoices[n-1].name, nil
		}

		p.say("请输入 1 到 %d 之间的编号。\n", len(channelChoices))
	}
}

// ask reads a secret after showing what it is; empty answers are refused.
func (p *prompter) ask(what string) (string, error) {
	p.say("%s，输入不显示：", what)

	s, err := p.secret()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", what, err)
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%w: %s is empty", errUsage, what)
	}

	return s, nil
}

// askTokens fills in the tokens the chosen channel needs and the flags did
// not give. Without a prompter it names the missing flag instead.
func askTokens(o *initOptions, p *prompter) error {
	var err error

	switch o.channel {
	case discord.Name:
		if o.discordToken == "" {
			if p == nil {
				return fmt.Errorf("%w: --channel discord needs --discord-token", errUsage)
			}

			o.discordToken, err = p.ask("Discord bot token（Developer Portal → Bot → Reset Token）")
		}
	case telegram.Name:
		if o.telegramToken == "" {
			if p == nil {
				return fmt.Errorf("%w: --channel telegram needs --telegram-token", errUsage)
			}

			o.telegramToken, err = p.ask("Telegram bot token（@BotFather → /newbot）")
		}
	case slack.Name:
		if o.slackBotToken == "" {
			if p == nil {
				return fmt.Errorf("%w: --channel slack needs --slack-app-token and --slack-bot-token", errUsage)
			}

			p.say("先按 README 里的 manifest 创建 Slack 应用：%s\n", slackGuide)

			if o.slackAppToken, err = p.ask("App-Level Token（xapp-…）"); err == nil {
				o.slackBotToken, err = p.ask("Bot User OAuth Token（xoxb-…）")
			}
		}
	}

	return err
}
