package telegram

import (
	"strings"
	"testing"
)

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{Token: "123456:AAE-secret"}).AppKey(); got != "telegram:123456" {
		t.Errorf("AppKey = %s, want telegram:123456", got)
	}

	// An unparsable token is hashed, never stored as is.
	if got := (Config{Token: "garbage-secret"}).AppKey(); strings.Contains(got, "garbage") || !strings.HasPrefix(got, "telegram:sha256-") {
		t.Errorf("AppKey(unparsable) = %s", got)
	}

	if (Config{Token: "a"}).AppKey() == (Config{Token: "b"}).AppKey() {
		t.Error("different tokens share a key")
	}
}
