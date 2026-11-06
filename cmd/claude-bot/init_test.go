package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

func TestEnsureLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("bin/"), 0o600); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := ensureLine(path, ".claude-bot/"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if want := "bin/\n.claude-bot/\n"; string(got) != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
}

func TestWriteIfAbsent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".claude", "commands", "bot.md")

	if err := writeIfAbsent(path, []byte("v1"), false); err != nil {
		t.Fatal(err)
	}

	if err := writeIfAbsent(path, []byte("v2"), false); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(path); string(got) != "v1" {
		t.Errorf("existing file overwritten without --force: %q", got)
	}

	if err := writeIfAbsent(path, []byte("v3"), true); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(path); string(got) != "v3" {
		t.Errorf("--force did not overwrite: %q", got)
	}
}

func TestBotCommandEmbedded(t *testing.T) {
	t.Parallel()

	if len(botCommand) == 0 {
		t.Fatal("bot.md slash command not embedded")
	}
}

func TestParseInitFlags_settlesChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args    []string
		channel string // "" with ok: chosen later (menu or Lark)
		ok      bool
	}{
		{args: nil, ok: true},
		{args: []string{"--channel", "slack"}, channel: "slack", ok: true},
		{args: []string{"--telegram-token", "1:t"}, channel: "telegram", ok: true},
		{args: []string{"--slack-app-token", "xapp-1", "--slack-bot-token", "xoxb-1"}, channel: "slack", ok: true},
		{args: []string{"--app-id", "cli_1", "--app-secret", "s"}, channel: "lark", ok: true},
		{args: []string{"--channel", "telegram", "--telegram-token", "1:t"}, channel: "telegram", ok: true},
		{args: []string{"--bot-md"}, ok: true},
		{args: []string{"--github-token", "g", "--github-repo", "o/r"}, channel: "github", ok: true},
		{args: []string{"--channel", "github"}, channel: "github", ok: true},
		{args: []string{"--dingtalk-client-id", "ding1", "--dingtalk-client-secret", "s"}, channel: "dingtalk", ok: true},
		{args: []string{"--dingtalk-client-id", "ding1"}},
		{args: []string{"--dingtalk-client-id", "ding1", "--dingtalk-client-secret", "s", "--github-token", "g"}},
		{args: []string{"--channel", "wechat"}},
		{args: []string{"--channel", "discord", "--telegram-token", "1:t"}},
		{args: []string{"--slack-bot-token", "xoxb-1"}},
		{args: []string{"--telegram-token", "1:t", "--discord-token", "d"}},
		{args: []string{"--app-id", "cli_1", "--app-secret", "s", "--slack-app-token", "xapp-1", "--slack-bot-token", "xoxb-1"}},
		{args: []string{"--bot-md", "--channel", "lark"}},
		{args: []string{"--github-token", "g", "--discord-token", "d"}},
	}

	for _, tt := range tests {
		o, err := parseInitFlags(tt.args)
		if (err == nil) != tt.ok {
			t.Errorf("parseInitFlags(%q) error = %v, want ok %v", tt.args, err, tt.ok)

			continue
		}

		if err != nil && !errors.Is(err, errUsage) {
			t.Errorf("parseInitFlags(%q) error = %v, want a usage error", tt.args, err)
		}

		if err == nil && o.channel != tt.channel {
			t.Errorf("parseInitFlags(%q) channel = %q, want %q", tt.args, o.channel, tt.channel)
		}
	}
}

func testPrompter(input string, secrets ...string) (*prompter, *strings.Builder) {
	out := &strings.Builder{}

	return &prompter{
		in:  bufio.NewReader(strings.NewReader(input)),
		out: printer{out},
		secret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}

			s := secrets[0]
			secrets = secrets[1:]

			return s, nil
		},
	}, out
}

func TestChooseChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input, want string
	}{
		{input: "\n", want: "lark"},
		{input: "3\n", want: "telegram"},
		{input: "4", want: "slack"}, // no trailing newline
		{input: "x\n9\n2\n", want: "discord"},
		{input: "5\n", want: "github"},
		{input: "6\n", want: "dingtalk"},
	}

	for _, tt := range tests {
		p, _ := testPrompter(tt.input)

		got, err := p.chooseChannel(config.Config{}, true)
		if err != nil || got != tt.want {
			t.Errorf("chooseChannel(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
		}
	}

	p, _ := testPrompter("x\n")
	if _, err := p.chooseChannel(config.Config{}, true); !errors.Is(err, errNoInput) {
		t.Errorf("chooseChannel at end of input = %v, want errNoInput", err)
	}
}

func TestChooseChannel_marksConfigured(t *testing.T) {
	t.Parallel()

	p, out := testPrompter("\n")
	if _, err := p.chooseChannel(config.Config{Telegram: &telegram.Config{Token: "t"}}, true); err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(line, "Telegram") != strings.Contains(line, "已配置") {
			t.Errorf("menu line %q: only Telegram should be marked configured", line)
		}
	}
}

func TestChooseChannel_noGitHubRemote(t *testing.T) {
	t.Parallel()

	// GitHub cannot be picked in a repository that is not on GitHub.
	p, out := testPrompter("5\n2\n")

	got, err := p.chooseChannel(config.Config{}, false)
	if err != nil || got != "discord" {
		t.Errorf("chooseChannel = %q, %v; want discord after refusing github", got, err)
	}

	if !strings.Contains(out.String(), "没有 GitHub 上的 remote") {
		t.Errorf("menu does not say why GitHub cannot be picked:\n%s", out)
	}
}

func TestRepoOfRemotes(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"": "",
		"origin\tgit@gitlab.com:a/b.git (fetch)\norigin\tgit@gitlab.com:a/b.git (push)\n": "",
		"origin\thttps://github.com/a/b.git (fetch)\n":                                    "a/b",
		"fork\tgit@github.com:me/b.git (fetch)\norigin\tgit@github.com:a/b.git (fetch)\n": "a/b",
		"origin\t/srv/git/b.git (fetch)\nupstream\tgit@github.com:a/b.git (fetch)\n":      "a/b",
	}

	for in, want := range cases {
		if got := repoOfRemotes(in); got != want {
			t.Errorf("repoOfRemotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAskTokens(t *testing.T) {
	t.Parallel()

	p, _ := testPrompter("", " xapp-1 ", "xoxb-1")

	o := initOptions{channel: "slack"}
	if err := askTokens(&o, p); err != nil {
		t.Fatal(err)
	}

	if o.slackAppToken != "xapp-1" || o.slackBotToken != "xoxb-1" {
		t.Errorf("tokens = %q, %q", o.slackAppToken, o.slackBotToken)
	}

	given := initOptions{channel: "telegram", telegramToken: "1:t"}
	if err := askTokens(&given, nil); err != nil || given.telegramToken != "1:t" {
		t.Errorf("askTokens with the flag given = %v, %q", err, given.telegramToken)
	}

	if err := askTokens(&initOptions{channel: "discord"}, nil); !errors.Is(err, errUsage) {
		t.Errorf("askTokens without terminal or flag = %v, want a usage error naming the flag", err)
	}

	gh, out := testPrompter("", "ghp_1")
	o = initOptions{channel: "github"}

	if err := askTokens(&o, gh); err != nil || o.githubToken != "ghp_1" || !strings.Contains(out.String(), githubGuide) {
		t.Errorf("askTokens(github) = %v, token %q, said %q", err, o.githubToken, out.String())
	}

	if err := askTokens(&initOptions{channel: "github"}, nil); !errors.Is(err, errUsage) {
		t.Errorf("askTokens(github) without terminal or flag = %v, want a usage error", err)
	}

	empty, _ := testPrompter("", "  ")
	if err := askTokens(&initOptions{channel: "telegram"}, empty); !errors.Is(err, errUsage) {
		t.Errorf("askTokens with an empty answer = %v, want a usage error", err)
	}
}

func TestRewriteBotCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := botCommandPath(root)

	if err := writeIfAbsent(path, []byte("edited by hand"), false); err != nil {
		t.Fatal(err)
	}

	if err := rewriteBotCommand(root, printer{}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, botCommand) {
		t.Errorf("bot.md after rewrite = %.40q…, %v; want the embedded command", got, err)
	}
}

// Every kind of channel the config knows, init sets up and status asks.
func TestChannelInits_everyKind(t *testing.T) {
	t.Parallel()

	for _, k := range config.Kinds() {
		c, ok := channelInits[k.Name]
		if !ok || c.hint == "" || c.given == nil || c.setup == nil {
			t.Errorf("init cannot set up %s", k.Name)
		}

		if _, ok := statusProbes[k.Name]; !ok {
			t.Errorf("status does not ask %s", k.Name)
		}
	}

	if len(channelInits) != len(config.Kinds()) {
		t.Errorf("init sets up %d kinds, the config knows %d", len(channelInits), len(config.Kinds()))
	}
}
