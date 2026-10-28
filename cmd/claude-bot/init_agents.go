package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed bot.md
var botCommand []byte

//go:embed pi/claude-bot-loop.ts
var piLoop []byte

// botCommandPath is the workflow, as Claude Code's /bot command. It is the
// only copy: other agents reach it through botSkillPath.
func botCommandPath(root string) string {
	return filepath.Join(root, ".claude", "commands", "bot.md")
}

// botSkillPath is where pi, Codex and Grok Build look for Agent Skills; a
// symlink to botCommandPath.
func botSkillPath(root string) string {
	return filepath.Join(root, ".agents", "skills", "bot", "SKILL.md")
}

// piLoopPath is where pi loads project extensions from.
func piLoopPath(root string) string {
	return filepath.Join(root, ".pi", "extensions", "claude-bot-loop.ts")
}

// writeWorkflow writes the /bot command (replacing it if force) and links
// the bot skill to it.
func writeWorkflow(root string, force bool) error {
	if err := writeIfAbsent(botCommandPath(root), botCommand, force); err != nil {
		return err
	}

	return linkSkill(root)
}

// linkSkill points botSkillPath at the /bot command, unless something is
// there already.
func linkSkill(root string) error {
	path := botSkillPath(root)
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil // whatever is there stays
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	target, err := filepath.Rel(filepath.Dir(path), botCommandPath(root))
	if err != nil {
		return fmt.Errorf("link %s: %w", path, err)
	}

	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("link %s: %w", path, err)
	}

	return nil
}

// agentFilesOnly reports init --bot-md or --pi, which leave the bot
// configuration alone.
func (o initOptions) agentFilesOnly() bool {
	return o.botMD || o.pi
}

// writeAgentFiles is init --bot-md and --pi: the workflow and agent
// integrations, without touching the bot configuration.
func writeAgentFiles(root string, o initOptions) error {
	if err := writeWorkflow(root, o.botMD); err != nil {
		return err
	}

	if o.botMD {
		fmt.Printf("✓ 已用 %s 版本的值守流程覆盖 %s\n", Version, botCommandPath(root))
	}

	if !o.pi {
		return nil
	}

	if err := writeIfAbsent(piLoopPath(root), piLoop, true); err != nil {
		return err
	}

	fmt.Printf("✓ 已安装 pi 的 /loop 扩展：%s\n", piLoopPath(root))
	fmt.Println("在这个仓库里打开 pi（第一次会问是否信任这个项目，选信任），运行  /loop /skill:bot")

	return nil
}
