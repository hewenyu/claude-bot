package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/config"
)

// checkCredentials builds the channel newChannel makes of cfg and asks it
// who its credentials belong to, with who, under apiTimeout. A refusal is
// an error naming what was checked: nothing is written then.
func checkCredentials[C any, T channel.Channel, I any](ctx context.Context, cfg C, newChannel func(C, *slog.Logger) (T, error),
	who func(T, context.Context) (I, error), what string,
) (T, I, error) {
	var id I

	ch, err := newChannel(cfg, quiet)
	if err != nil {
		return ch, id, err
	}

	if id, err = withTimeout(ctx, func(ctx context.Context) (I, error) { return who(ch, ctx) }); err != nil {
		return ch, id, fmt.Errorf("%s check failed (nothing written): %w", what, err)
	}

	return ch, id, nil
}

// setupDiscord checks a Discord bot token and records it in cfg.
func setupDiscord(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	dcfg := discord.Config{Token: o.discordToken, Proxy: o.proxy}

	ch, bot, err := checkCredentials(ctx, dcfg, discord.New, (*discord.Channel).Bot, "discord token")
	if err != nil {
		return setup{}, err
	}

	cfg.Discord = &dcfg

	return setup{
		ch:     ch,
		name:   bot.Name,
		prompt: fmt.Sprintf("请用上面的链接把 bot 邀请进服务器，然后在 Discord 里给「%s」发一条私聊消息", bot.Name),
		report: func(context.Context) {
			o.out.printf("把 bot 邀请进 Discord 服务器（需要服务器的管理权限）：\n\n  %s\n\n", bot.InviteURL())
		},
	}, nil
}

// setupTelegram checks a Telegram bot token and records it in cfg.
func setupTelegram(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	tcfg := telegram.Config{Token: o.telegramToken, Proxy: o.proxy}

	ch, bot, err := checkCredentials(ctx, tcfg, telegram.New, (*telegram.Channel).Bot, "telegram token")
	if err != nil {
		return setup{}, err
	}

	cfg.Telegram = &tcfg

	return setup{
		ch:     ch,
		name:   bot.Name,
		prompt: fmt.Sprintf("请在 Telegram 里打开 %s，给 bot 发一条消息", bot.Link()),
		report: func(context.Context) { reportPrivacy(o.out, bot) },
	}, nil
}

// reportPrivacy warns that, with privacy mode on, Telegram may withhold
// group messages that @-mention the bot.
func reportPrivacy(out printer, bot telegram.Bot) {
	if bot.ReadsAllGroupMessages {
		return
	}

	out.printf("! bot 开着隐私模式（privacy mode），群里 @%s 的消息可能收不到。\n", bot.Username)
	out.println("  在 @BotFather 里发 /setprivacy，选这个 bot，再选 Disable；已经在群里的话，要把 bot 移出群再拉回来才生效。")
	out.println("  关掉后 bot 仍然只处理 @ 它或回复它的消息。")
}

// setupSlack checks both Slack tokens and records them in cfg.
func setupSlack(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	scfg := slack.Config{AppToken: o.slackAppToken, BotToken: o.slackBotToken, Proxy: o.proxy}

	ch, bot, err := checkCredentials(ctx, scfg, slack.New, (*slack.Channel).Bot, "slack bot token")
	if err != nil {
		return setup{}, err
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
			o.out.printf("在要用的频道里输入 /invite @%s 把 bot 拉进去。\n", bot.Name)
		},
	}, nil
}

// setupDingTalk checks a DingTalk app's credentials and records them in cfg.
func setupDingTalk(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	dcfg := dingtalk.Config{ClientID: o.dingtalkID, ClientSecret: o.dingtalkSecret, Proxy: o.proxy}

	ch, app, err := checkCredentials(ctx, dcfg, dingtalk.New, (*dingtalk.Channel).App, "dingtalk credentials")
	if err != nil {
		return setup{}, err
	}

	cfg.DingTalk = &dcfg

	return setup{
		ch:     ch,
		name:   "钉钉应用 " + app.ClientID,
		prompt: "请在钉钉里搜索这个应用的机器人，给它发一条私聊消息",
		report: func(context.Context) {
			o.out.println("把机器人加进要用的群：群设置 → 机器人 → 添加机器人，选这个应用的机器人。")
		},
	}, nil
}

// setupGitHub checks a GitHub token against the repository and records it
// in cfg. GitHub has no direct messages, so there is no round-trip check.
func setupGitHub(ctx context.Context, cfg *config.Config, o initOptions) (setup, error) {
	repo := o.githubRepo
	if repo == "" {
		repo = o.githubRemote
	}

	if repo == "" {
		return setup{}, fmt.Errorf("%w: %s has no GitHub remote, so there is nothing on GitHub to watch; pass --github-repo <owner>/<name> to watch another repository", errUsage, o.root)
	}

	gcfg := github.Config{Token: o.githubToken, Repo: repo, Proxy: o.proxy}

	ch, acct, err := checkCredentials(ctx, gcfg, github.New, (*github.Channel).Account, "github token")
	if err != nil {
		return setup{}, err
	}

	cfg.GitHub, cfg.GitHubTokenName = &gcfg, o.githubTokenName

	return setup{
		ch:     ch,
		name:   acct.Login + " @ " + acct.Repo,
		report: func(context.Context) { reportGitHub(o.out, acct) },
	}, nil
}

// githubRemote returns <owner>/<name> of the repository's remote on
// github.com, origin first; "" if it has none, as a repository that was
// never pushed: then there is nothing on GitHub to watch.
func githubRemote(ctx context.Context, root string) string {
	cmd := exec.CommandContext(ctx, "git", "remote", "-v") //nolint:gosec // fixed arguments; root only sets the directory
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return repoOfRemotes(string(out))
}

// repoOfRemotes picks the GitHub repository from `git remote -v` output.
func repoOfRemotes(out string) string {
	var first string

	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		repo, ok := github.RepoFromRemote(fields[1])
		if !ok {
			continue
		}

		if fields[0] == "origin" {
			return repo
		}

		if first == "" {
			first = repo
		}
	}

	return first
}

func reportGitHub(out printer, acct github.Account) {
	out.printf("bot 用 %s 的身份回复；它发的评论末尾带隐藏标记 %s，靠这个和你手写的评论区分。\n", acct.Login, github.Marker)

	if !acct.CanPush {
		out.println("! 这个 token 在仓库里没有写权限：能收消息、发评论，但 close / merge 会失败。")
	}

	out.println("监听进程第一次启动时，会把还开着、最后一条不是 bot 回复的 issue 和 PR 放进 inbox。")
}
