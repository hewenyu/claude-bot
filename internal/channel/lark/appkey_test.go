package lark

import "testing"

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{AppID: "cli_a1", AppSecret: "s"}).AppKey(); got != "lark:cli_a1" {
		t.Errorf("AppKey = %s, want lark:cli_a1", got)
	}
}
