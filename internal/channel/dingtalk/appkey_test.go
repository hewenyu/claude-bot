package dingtalk

import "testing"

func TestAppKey(t *testing.T) {
	t.Parallel()

	if got := (Config{ClientID: "dingabc", ClientSecret: "s"}).AppKey(); got != "dingtalk:dingabc" {
		t.Errorf("AppKey = %s, want dingtalk:dingabc", got)
	}
}
