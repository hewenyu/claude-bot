package discord

import (
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// AppKey names the bot c connects to, by the bot user its token starts with
// (<user id, base64>.<…>); a token it cannot parse is hashed.
func (c Config) AppKey() string {
	if user, _, ok := strings.Cut(c.Token, "."); ok && user != "" {
		return Name + ":" + user
	}

	return channel.HashedAppKey(Name, c.Token)
}
