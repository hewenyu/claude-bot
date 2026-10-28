// Package config locates the project's .claude-bot directory and loads the
// bot credentials from .claude-bot/config.json, with environment overrides.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
)

// DirName is the per-project state directory (git-ignored).
const DirName = ".claude-bot"

const (
	configFile  = "config.json"
	dirPerm     = 0o750
	privatePerm = 0o600 // the file holds app secrets
)

// Environment variables.
const (
	claudeProjectEnv = "CLAUDE_PROJECT_DIR" // set by Claude Code for its session
	larkAppIDEnv     = "LARK_APP_ID"
	larkSecretEnv    = "LARK_APP_SECRET" //nolint:gosec // the variable name, not a secret
	larkDomainEnv    = "LARK_DOMAIN"
	discordTokenEnv  = "DISCORD_BOT_TOKEN"  //nolint:gosec // the variable name, not a secret
	telegramTokenEnv = "TELEGRAM_BOT_TOKEN" //nolint:gosec // the variable name, not a secret
	slackAppTokenEnv = "SLACK_APP_TOKEN"    //nolint:gosec // the variable name, not a secret
	slackBotTokenEnv = "SLACK_BOT_TOKEN"    //nolint:gosec // the variable name, not a secret
)

// Config is .claude-bot/config.json. Each configured channel is enabled.
type Config struct {
	Lark     *lark.Config     `json:"lark,omitempty"`
	Discord  *discord.Config  `json:"discord,omitempty"`
	Telegram *telegram.Config `json:"telegram,omitempty"`
	Slack    *slack.Config    `json:"slack,omitempty"`
}

// Dir returns <project root>/.claude-bot. The project root is, in order: the
// repository of project (from -C) if given, the git repository containing the
// working directory, $CLAUDE_PROJECT_DIR (the Claude Code session root), the
// working directory. Each repository therefore carries its own bots and
// state, and several projects can run side by side.
func Dir(ctx context.Context, project string) (string, error) {
	if project != "" {
		return projectDir(ctx, project)
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate project: %w", err)
	}

	if top := gitTop(ctx, wd); top != "" {
		return filepath.Join(top, DirName), nil
	}

	if root := os.Getenv(claudeProjectEnv); root != "" {
		return filepath.Join(root, DirName), nil
	}

	return filepath.Join(wd, DirName), nil
}

// projectDir resolves an explicitly chosen project to its repository root.
func projectDir(ctx context.Context, root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project %s: %w", root, err)
	}

	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("project %s is not a directory", root)
	}

	if top := gitTop(ctx, abs); top != "" {
		abs = top
	}

	return filepath.Join(abs, DirName), nil
}

func gitTop(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// Load reads dir/config.json and applies LARK_APP_ID, LARK_APP_SECRET,
// LARK_DOMAIN, DISCORD_BOT_TOKEN, TELEGRAM_BOT_TOKEN, SLACK_APP_TOKEN and
// SLACK_BOT_TOKEN from the environment.
func Load(dir string) (Config, error) {
	var cfg Config

	raw, err := os.ReadFile(filepath.Join(dir, configFile)) //nolint:gosec // fixed file in the project bot dir
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return cfg, fmt.Errorf("read config: %w", err)
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", filepath.Join(dir, configFile), err)
		}
	}

	if id, secret := os.Getenv(larkAppIDEnv), os.Getenv(larkSecretEnv); id != "" && secret != "" {
		cfg.Lark = &lark.Config{AppID: id, AppSecret: secret, Domain: os.Getenv(larkDomainEnv)}
	}

	if token := os.Getenv(discordTokenEnv); token != "" {
		cfg.Discord = &discord.Config{Token: token}
	}

	if token := os.Getenv(telegramTokenEnv); token != "" {
		cfg.Telegram = &telegram.Config{Token: token}
	}

	if app, bot := os.Getenv(slackAppTokenEnv), os.Getenv(slackBotTokenEnv); app != "" && bot != "" {
		cfg.Slack = &slack.Config{AppToken: app, BotToken: bot}
	}

	return cfg, nil
}

// Save writes cfg to dir/config.json, readable only by the owner. The file is
// replaced atomically, so a crash never leaves half-written credentials.
func Save(dir string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	if err := atomicfile.Write(filepath.Join(dir, configFile), append(raw, '\n'), privatePerm); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}
