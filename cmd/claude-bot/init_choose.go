package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

// Setup guides in docs/channels.md.
const (
	slackGuide    = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#slack"
	githubGuide   = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#github"
	dingtalkGuide = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#钉钉"
)

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
	{name: github.Name, label: "GitHub（仓库的 issue、PR 和评论，需要 token）"},
	{name: dingtalk.Name, label: "钉钉 / DingTalk（企业内部应用的机器人，需要 Client ID 和 Client Secret）"},
}

func knownChannel(name string) bool {
	return slices.ContainsFunc(channelChoices, func(c channelChoice) bool { return c.name == name })
}

// channelNames lists the channels in menu order, joined by sep.
func channelNames(sep string) string {
	names := make([]string, len(channelChoices))
	for i, c := range channelChoices {
		names[i] = c.name
	}

	return strings.Join(names, sep)
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
		if cfg.Has(c.name) {
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
	switch o.channel {
	case discord.Name:
		return askToken(p, o.channel, "--discord-token", "", "Discord bot token（Developer Portal → Bot → Reset Token）", &o.discordToken)
	case telegram.Name:
		return askToken(p, o.channel, "--telegram-token", "", "Telegram bot token（@BotFather → /newbot）", &o.telegramToken)
	case github.Name:
		return askToken(p, o.channel, "--github-token", "token 的获取和权限见："+githubGuide,
			"GitHub token（Settings → Developer settings → Personal access tokens）", &o.githubToken)
	case slack.Name:
		if o.slackBotToken != "" {
			return nil
		}

		if p == nil {
			return fmt.Errorf("%w: --channel slack needs --slack-app-token and --slack-bot-token", errUsage)
		}

		p.say("先按文档里的 manifest 创建 Slack 应用：%s\n", slackGuide)

		var err error
		if o.slackAppToken, err = p.ask("App-Level Token（xapp-…）"); err == nil {
			o.slackBotToken, err = p.ask("Bot User OAuth Token（xoxb-…）")
		}

		return err
	case dingtalk.Name:
		if o.dingtalkID != "" {
			return nil
		}

		if p == nil {
			return fmt.Errorf("%w: --channel dingtalk needs --dingtalk-client-id and --dingtalk-client-secret", errUsage)
		}

		p.say("先按文档创建钉钉应用并开启 Stream 模式的机器人：%s\n", dingtalkGuide)

		var err error
		if o.dingtalkID, err = p.ask("Client ID（AppKey）"); err == nil {
			o.dingtalkSecret, err = p.ask("Client Secret（AppSecret）")
		}

		return err
	}

	return nil
}

// askToken asks for one token unless its flag gave it; hint, if any, is
// shown first.
func askToken(p *prompter, name, flagName, hint, what string, token *string) error {
	if *token != "" {
		return nil
	}

	if p == nil {
		return fmt.Errorf("%w: --channel %s needs %s", errUsage, name, flagName)
	}

	if hint != "" {
		p.say("%s\n", hint)
	}

	var err error

	*token, err = p.ask(what)

	return err
}
