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

	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/config"
)

// Setup guides in docs/channels.md.
const (
	slackGuide    = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#slack"
	githubGuide   = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#github"
	dingtalkGuide = "https://github.com/hewenyu/claude-bot/blob/master/docs/channels.md#钉钉"
)

var errNoInput = errors.New("no answer (stdin closed)")

// prompter asks the person running init; secret reads without echo.
type prompter struct {
	in     *bufio.Reader
	out    printer
	secret func() (string, error)
}

// terminalPrompter asks on the terminal, telling out; nil when stdin is not
// one (a script, Claude Code's Bash tool), where init must not wait for
// answers.
func terminalPrompter(out printer) *prompter {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}

	return &prompter{
		in:  bufio.NewReader(os.Stdin),
		out: out,
		secret: func() (string, error) {
			b, err := term.ReadPassword(fd)

			out.println() // the Enter was not echoed

			return string(b), err
		},
	}
}

// say writes to the person running init; a broken terminal shows up when
// reading the answer.
func (p *prompter) say(format string, args ...any) {
	p.out.printf(format, args...)
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
func (p *prompter) chooseChannel(cfg config.Config, onGitHub bool) (string, error) {
	p.say("选择要接入的渠道（之后可以再运行 init --channel <渠道> 接入其他渠道）：\n")

	choices := config.Kinds()

	for i, k := range choices {
		mark := ""

		switch {
		case cfg.Has(k.Name):
			mark = "  [已配置]"
		case k.Name == github.Name && !onGitHub:
			mark = "  [这个仓库没有 GitHub 上的 remote，不可选]"
		}

		p.say("  %d) %s（%s）%s\n", i+1, k.Label, channelInits[k.Name].hint, mark)
	}

	for {
		p.say("请输入编号 [1]：")

		answer, err := p.line()
		if err != nil {
			return "", err
		}

		if answer == "" {
			return choices[0].Name, nil
		}

		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(choices) {
			if choices[n-1].Name == github.Name && !onGitHub {
				p.say("这个仓库没有 GitHub 上的 remote，没有可监听的 issue 和 PR；请选别的渠道（要监听别的仓库，用 --github-token 加 --github-repo）。\n")

				continue
			}

			return choices[n-1].Name, nil
		}

		p.say("请输入 1 到 %d 之间的编号。\n", len(choices))
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
	if ask := channelInits[o.channel].ask; ask != nil {
		return ask(o, p)
	}

	return nil
}

// askSlackTokens asks for both tokens of a Slack app.
func askSlackTokens(o *initOptions, p *prompter) error {
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
}

// askDingTalkCredentials asks for a DingTalk app's id and secret.
func askDingTalkCredentials(o *initOptions, p *prompter) error {
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

// askGitHubToken takes the GitHub token every project shares, or asks for
// one if there is none yet.
func askGitHubToken(o *initOptions, p *prompter) error {
	if o.githubToken == "" && o.sharedGitHubToken != "" {
		o.githubToken = o.sharedGitHubToken

		if o.githubTokenName != "" {
			o.out.printf("沿用 gateway 里的 GitHub token「%s」（要换就加 --github-token）\n", o.githubTokenName)
		} else {
			o.out.println("沿用 gateway 里的默认 GitHub token（要换就加 --github-token；另一个账号的项目加 --github-token <token> --github-token-name <名字>）")
		}

		return nil
	}

	return askToken(p, o.channel, "--github-token", "token 的获取和权限见："+githubGuide,
		"GitHub token（Settings → Developer settings → Personal access tokens）", &o.githubToken)
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
