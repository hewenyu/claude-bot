package discord

import (
	"strings"
	"testing"
)

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{Token: "MTIzNDU2.Gx.secret"}).AppKey(); got != "discord:MTIzNDU2" {
		t.Errorf("AppKey = %s, want discord:MTIzNDU2", got)
	}

	// An unparsable token is hashed, never stored as is.
	if got := (Config{Token: "garbage"}).AppKey(); strings.Contains(got, "garbage") || !strings.HasPrefix(got, "discord:sha256-") {
		t.Errorf("AppKey(unparsable) = %s", got)
	}

	if (Config{Token: "a"}).AppKey() == (Config{Token: "b"}).AppKey() {
		t.Error("different tokens share a key")
	}
}
