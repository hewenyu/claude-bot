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

	"github.com/hewenyu/claude-bot/internal/channel/lark"
)

// DirName is the per-project state directory (git-ignored).
const DirName = ".claude-bot"

// Config is .claude-bot/config.json. Each configured channel is enabled.
type Config struct {
	Lark *lark.Config `json:"lark,omitempty"`
}

// ProjectEnv selects the project explicitly (set by `claude-bot -C <dir>`).
const ProjectEnv = "CLAUDE_BOT_PROJECT"

// Dir returns <project root>/.claude-bot. The project root is, in order:
// $CLAUDE_BOT_PROJECT (from -C), the git repository containing the working
// directory, $CLAUDE_PROJECT_DIR (the Claude Code session root), the working
// directory. Each repository therefore carries its own bots and state, and
// several projects can run side by side.
func Dir(ctx context.Context) (string, error) {
	if root := os.Getenv(ProjectEnv); root != "" {
		return projectDir(ctx, root)
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate project: %w", err)
	}

	if top := gitTop(ctx, wd); top != "" {
		return filepath.Join(top, DirName), nil
	}

	if root := os.Getenv("CLAUDE_PROJECT_DIR"); root != "" {
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

	if info, err := os.Stat(abs); err != nil || !info.IsDir() { //nolint:gosec // the user picks the project directory with -C
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

// Load reads dir/config.json and applies LARK_APP_ID, LARK_APP_SECRET and
// LARK_DOMAIN from the environment.
func Load(dir string) (Config, error) {
	var cfg Config

	raw, err := os.ReadFile(filepath.Join(dir, "config.json")) //nolint:gosec // fixed file in the project bot dir
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return cfg, fmt.Errorf("read config: %w", err)
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", filepath.Join(dir, "config.json"), err)
		}
	}

	if id, secret := os.Getenv("LARK_APP_ID"), os.Getenv("LARK_APP_SECRET"); id != "" && secret != "" {
		cfg.Lark = &lark.Config{AppID: id, AppSecret: secret, Domain: os.Getenv("LARK_DOMAIN")}
	}

	return cfg, nil
}

// Save writes cfg to dir/config.json, readable only by the owner.
func Save(dir string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
