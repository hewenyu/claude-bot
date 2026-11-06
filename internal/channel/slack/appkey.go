package slack

import (
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// AppKey names the app c connects to, by the app id in its app-level token
// (xapp-<n>-<app id>-…); a token it cannot parse is hashed.
func (c Config) AppKey() string {
	if parts := strings.Split(c.AppToken, "-"); len(parts) >= 3 && parts[0] == "xapp" && parts[2] != "" {
		return Name + ":" + parts[2]
	}

	return channel.HashedAppKey(Name, c.AppToken)
}
