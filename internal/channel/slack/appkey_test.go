package slack

import (
	"strings"
	"testing"
)

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{AppToken: "xapp-1-A0123-456-secret"}).AppKey(); got != "slack:A0123" {
		t.Errorf("AppKey = %s, want slack:A0123", got)
	}

	// An unparsable token is hashed, never stored as is.
	for _, token := range []string{"xoxb-1-A0123", "xapp-1-", "garbage"} {
		if got := (Config{AppToken: token}).AppKey(); !strings.HasPrefix(got, "slack:sha256-") {
			t.Errorf("AppKey(%s) = %s, want a hash", token, got)
		}
	}
}
