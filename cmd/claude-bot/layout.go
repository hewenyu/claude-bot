package main

import (
	"path/filepath"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/store"
)

// Layout of the project's .claude-bot directory.
const (
	heartbeatFile = "serve.json"
	listenerLock  = "serve.lock"
	listenerLog   = "serve.log"
	inboxDir      = "inbox"
	filesDir      = "files"
)

// File modes: bot state stays private to the owner; files meant for the team
// (.gitignore, the slash command) are world-readable like the rest of the repo.
const (
	dirPerm     = atomicfile.ProjectDir
	privatePerm = atomicfile.PrivateFile
	sharedPerm  = atomicfile.SharedFile
)

// botDir is a registered project's .claude-bot directory.
func botDir(p store.Project) string {
	return config.BotDir(p.Dir)
}

// inboxOf is the inbox in the bot directory dir.
func inboxOf(dir string) *inbox.Inbox {
	return inbox.New(filepath.Join(dir, inboxDir))
}

// projectInbox is a registered project's inbox.
func projectInbox(p store.Project) *inbox.Inbox {
	return inboxOf(botDir(p))
}

// botCommandPath is the /bot command init writes under root, a repository
// (or the user's home, for their own commands).
func botCommandPath(root string) string {
	return filepath.Join(root, ".claude", "commands", "bot.md")
}

// botSkillPath is where a /bot skill would be under root.
func botSkillPath(root string) string {
	return filepath.Join(root, ".claude", "skills", "bot", "SKILL.md")
}
