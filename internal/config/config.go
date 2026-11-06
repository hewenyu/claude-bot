// Package config locates the project's .claude-bot directory and loads the
// project's bot configuration: from the gateway database once the project is
// registered there, else from .claude-bot/config.json; either way with
// environment overrides.
package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
)

// DirName is the per-project state directory (git-ignored).
const DirName = ".claude-bot"

// BotDir is the .claude-bot directory of the repository at root.
func BotDir(root string) string { return filepath.Join(root, DirName) }

// FilePath is the config.json of the bot directory dir, which ReadFile reads
// and WriteFile writes.
func FilePath(dir string) string { return filepath.Join(dir, configFile) }

const configFile = "config.json"

// Environment variables.
const (
	claudeProjectEnv  = "CLAUDE_PROJECT_DIR" // set by Claude Code for its session
	larkAppIDEnv      = "LARK_APP_ID"
	larkSecretEnv     = "LARK_APP_SECRET" //nolint:gosec // the variable name, not a secret
	larkDomainEnv     = "LARK_DOMAIN"
	discordTokenEnv   = "DISCORD_BOT_TOKEN"  //nolint:gosec // the variable name, not a secret
	telegramTokenEnv  = "TELEGRAM_BOT_TOKEN" //nolint:gosec // the variable name, not a secret
	slackAppTokenEnv  = "SLACK_APP_TOKEN"    //nolint:gosec // the variable name, not a secret
	slackBotTokenEnv  = "SLACK_BOT_TOKEN"    //nolint:gosec // the variable name, not a secret
	dingtalkIDEnv     = "DINGTALK_CLIENT_ID"
	dingtalkSecretEnv = "DINGTALK_CLIENT_SECRET"
)

// Config is .claude-bot/config.json. Each configured channel is enabled;
// Kinds says what each is.
type Config struct {
	Lark     *lark.Config     `json:"lark,omitempty"`
	Discord  *discord.Config  `json:"discord,omitempty"`
	Telegram *telegram.Config `json:"telegram,omitempty"`
	Slack    *slack.Config    `json:"slack,omitempty"`
	GitHub   *github.Config   `json:"github,omitempty"`
	DingTalk *dingtalk.Config `json:"dingtalk,omitempty"`
	// TypeSafe, if set, has the listener screen messages with Jev before
	// they reach the inbox. Only config.json sets it, never the environment.
	TypeSafe *jev.Config `json:"typesafe,omitempty"`
	// GitHubTokenName names the gateway's GitHub token the project uses;
	// "" is the default token.
	GitHubTokenName string `json:"-"`
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
		return BotDir(top), nil
	}

	if root := os.Getenv(claudeProjectEnv); root != "" {
		return BotDir(root), nil
	}

	return BotDir(wd), nil
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

	return BotDir(abs), nil
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

// Read returns the project's configuration without the environment: what
// init rewrites, so credentials given through the environment are not saved.
// A project registered with the gateway reads it from the gateway database,
// any other from dir/config.json (as before the gateway). The settings every
// project shares, the TypeSafe API key, come from the gateway either way.
func Read(ctx context.Context, dir string) (Config, error) {
	cfg, _, err := read(ctx, dir)

	return cfg, err
}

// read is Read, with the project's registration with the gateway; nil if
// it is not registered.
func read(ctx context.Context, dir string) (Config, *store.Project, error) {
	s, ok, err := openGateway(ctx)
	if err != nil {
		return Config{}, nil, err
	}

	if !ok {
		cfg, err := ReadFile(dir)

		return cfg, nil, err
	}

	defer s.Close() //nolint:errcheck // read-only use

	p, err := s.ProjectByDir(ctx, filepath.Dir(dir))

	switch {
	case err == nil:
		cfg, err := FromStore(ctx, s, p)

		return cfg, &p, err
	case !errors.Is(err, store.ErrNotFound):
		return Config{}, nil, err
	}

	cfg, err := ReadFile(dir)
	if err != nil {
		return cfg, nil, err
	}

	if cfg.TypeSafe == nil {
		key, err := s.Setting(ctx, store.SettingTypeSafeKey)
		if err != nil {
			return cfg, nil, err
		}

		if key != "" {
			cfg.TypeSafe = &jev.Config{APIKey: key}
		}
	}

	return cfg, nil, nil
}

// ReadFile reads dir/config.json alone, the configuration of a project that
// is not registered with the gateway.
func ReadFile(dir string) (Config, error) {
	var cfg Config

	if err := atomicfile.ReadJSON(FilePath(dir), &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, err
	}

	return cfg, nil
}

// Load reads the project's configuration (see Read) and applies
// LARK_APP_ID, LARK_APP_SECRET, LARK_DOMAIN, DISCORD_BOT_TOKEN,
// TELEGRAM_BOT_TOKEN, SLACK_APP_TOKEN, SLACK_BOT_TOKEN, DINGTALK_CLIENT_ID
// and DINGTALK_CLIENT_SECRET from the environment. They replace the stored
// credentials; the other settings (proxy, domain) stay.
func Load(ctx context.Context, dir string) (Config, error) {
	cfg, _, err := LoadProject(ctx, dir)

	return cfg, err
}

// LoadProject is Load, with the project's registration with the gateway,
// which then holds its configuration and serves it; nil for a project
// configured by its own config.json.
func LoadProject(ctx context.Context, dir string) (Config, *store.Project, error) {
	cfg, p, err := read(ctx, dir)
	if err != nil {
		return cfg, p, err
	}

	applyEnv(&cfg)

	return cfg, p, nil
}

// WriteFile writes cfg to dir/config.json, readable only by the owner, the
// way projects were configured before the gateway. The file is replaced
// atomically, so a crash never leaves half-written credentials.
func WriteFile(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, atomicfile.ProjectDir); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	if err := atomicfile.WriteJSON(FilePath(dir), cfg, atomicfile.PrivateFile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}
