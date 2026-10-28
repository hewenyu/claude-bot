package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

// setupDiscord checks a Discord bot token and records it in cfg.
func setupDiscord(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	dcfg := discord.Config{Token: o.discordToken, Proxy: o.proxy}

	ch, err := discord.New(dcfg, quiet)
	if err != nil {
		return setup{}, err
	}

	bot, err := withTimeout(ctx, ch.Bot)
	if err != nil {
		return setup{}, fmt.Errorf("discord token check failed (nothing written): %w", err)
	}

	cfg.Discord = &dcfg

	return setup{
		ch:     ch,
		name:   bot.Name,
		prompt: fmt.Sprintf("请用上面的链接把 bot 邀请进服务器，然后在 Discord 里给「%s」发一条私聊消息", bot.Name),
		report: func(context.Context) { reportInvite(bot) },
	}, nil
}

// reportInvite prints the link that adds the Discord bot to a server.
func reportInvite(bot discord.Bot) {
	fmt.Printf("把 bot 邀请进 Discord 服务器（需要服务器的管理权限）：\n\n  %s\n\n", bot.InviteURL())
}

// setupTelegram checks a Telegram bot token and records it in cfg.
func setupTelegram(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	tcfg := telegram.Config{Token: o.telegramToken, Proxy: o.proxy}

	ch, err := telegram.New(tcfg, quiet)
	if err != nil {
		return setup{}, err
	}

	bot, err := withTimeout(ctx, ch.Bot)
	if err != nil {
		return setup{}, fmt.Errorf("telegram token check failed (nothing written): %w", err)
	}

	cfg.Telegram = &tcfg

	return setup{
		ch:     ch,
		name:   bot.Name,
		prompt: fmt.Sprintf("请在 Telegram 里打开 %s，给 bot 发一条消息", bot.Link()),
		report: func(context.Context) { reportPrivacy(bot) },
	}, nil
}

// reportPrivacy warns that, with privacy mode on, Telegram may withhold
// group messages that @-mention the bot.
func reportPrivacy(bot telegram.Bot) {
	if bot.ReadsAllGroupMessages {
		return
	}

	fmt.Printf("! bot 开着隐私模式（privacy mode），群里 @%s 的消息可能收不到。\n", bot.Username)
	fmt.Println("  在 @BotFather 里发 /setprivacy，选这个 bot，再选 Disable；已经在群里的话，要把 bot 移出群再拉回来才生效。")
	fmt.Println("  关掉后 bot 仍然只处理 @ 它或回复它的消息。")
}

// setupSlack checks both Slack tokens and records them in cfg.
func setupSlack(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	scfg := slack.Config{AppToken: o.slackAppToken, BotToken: o.slackBotToken, Proxy: o.proxy}

	ch, err := slack.New(scfg, quiet)
	if err != nil {
		return setup{}, err
	}

	bot, err := withTimeout(ctx, ch.Bot)
	if err != nil {
		return setup{}, fmt.Errorf("slack bot token check failed (nothing written): %w", err)
	}

	if _, err := withTimeout(ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, ch.CheckAppToken(ctx) }); err != nil {
		return setup{}, fmt.Errorf("slack app token check failed (nothing written): %w", err)
	}

	cfg.Slack = &scfg

	return setup{
		ch:     ch,
		name:   bot.Name,
		prompt: fmt.Sprintf("请在 Slack（%s）里给「%s」发一条私信", bot.URL, bot.Name),
		report: func(context.Context) {
			fmt.Printf("在要用的频道里输入 /invite @%s 把 bot 拉进去。\n", bot.Name)
		},
	}, nil
}

// setupGitHub checks a GitHub token against the repository and records it
// in cfg. GitHub has no direct messages, so there is no round-trip check.
func setupGitHub(ctx context.Context, cfg *config.Config, o initOptions, root string) (setup, error) {
	repo := o.githubRepo
	if repo == "" {
		var err error
		if repo, err = originRepo(ctx, root); err != nil {
			return setup{}, err
		}
	}

	gcfg := github.Config{Token: o.githubToken, Repo: repo, Proxy: o.proxy}

	ch, err := github.New(gcfg, quiet)
	if err != nil {
		return setup{}, err
	}

	acct, err := withTimeout(ctx, ch.Account)
	if err != nil {
		return setup{}, fmt.Errorf("github token check failed (nothing written): %w", err)
	}

	cfg.GitHub = &gcfg

	return setup{
		ch:     ch,
		name:   acct.Login + " @ " + acct.Repo,
		report: func(context.Context) { reportGitHub(acct) },
	}, nil
}

// originRepo reads <owner>/<name> from the repository's origin remote.
func originRepo(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: no origin remote to take the repository from; pass --github-repo <owner>/<name>", errUsage)
	}

	repo, ok := github.RepoFromRemote(string(out))
	if !ok {
		return "", fmt.Errorf("%w: origin %s is not on github.com; pass --github-repo <owner>/<name>", errUsage, strings.TrimSpace(string(out)))
	}

	return repo, nil
}

func reportGitHub(acct github.Account) {
	fmt.Printf("bot 用 %s 的身份回复；它发的评论末尾带隐藏标记 %s，靠这个和你手写的评论区分。\n", acct.Login, github.Marker)

	if !acct.CanPush {
		fmt.Println("! 这个 token 在仓库里没有写权限：能收消息、发评论，但 close / merge 会失败。")
	}

	fmt.Println("监听进程第一次启动时，会把还开着、最后一条不是 bot 回复的 issue 和 PR 放进 inbox。")
}
