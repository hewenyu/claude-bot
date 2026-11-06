package telegram

import (
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// AppKey names the bot c connects to, by the bot id its token starts with
// (<bot id>:<secret>); a token it cannot parse is hashed.
func (c Config) AppKey() string {
	if bot, _, ok := strings.Cut(c.Token, ":"); ok && bot != "" {
		return Name + ":" + bot
	}

	return channel.HashedAppKey(Name, c.Token)
}
