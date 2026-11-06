package github

import "testing"

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{Token: "t", Repo: "Hewenyu/Claude-Bot"}).AppKey(); got != "github:hewenyu/claude-bot" {
		t.Errorf("AppKey = %s, want github:hewenyu/claude-bot", got)
	}
}
