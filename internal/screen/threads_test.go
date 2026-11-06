package screen

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/inbox"
)

// The shape of task:mvv6adfh1: the user's ask, then a bot reply of 2000
// runes. The end of the transcript alone is the end of that reply; Jev must
// also read what the task is about.
func TestThreadOf_keepsTheOpeningAsk(t *testing.T) {
	t.Parallel()

	ask := "你评估一下要不要做一个类似聚合的功能：Cloud BOT Gateway"
	reply := strings.Repeat("评估", 1000) + "回个比如「1 可以，2A」，我就按这个开 PR。"
	task := inbox.Task{ID: "task:1", Entries: []inbox.Entry{
		{Kind: inbox.EntryUser, Text: ask},
		{Kind: inbox.EntryBot, Text: reply},
		{Kind: inbox.EntryUser, Sender: &channel.User{Name: "Ann"}, Text: "好"},
	}}

	th := ThreadOf(task)

	if !strings.HasPrefix(th.Transcript, "user: "+ask+"\n…") || !strings.HasSuffix(th.Transcript, "开 PR。\nAnn: 好") ||
		utf8.RuneCountInString(th.Transcript) > askRunes+transcriptRunes+10 {
		t.Errorf("transcript = %q; want the opening ask, then the end of the rest", th.Transcript)
	}

	if th.ID != task.ID || th.Asked != ask+"\n好" {
		t.Errorf("thread = %+v; want only what people asked", th)
	}
}

func TestThreads_atMostMaxThreads(t *testing.T) {
	t.Parallel()

	tasks := make([]inbox.Task, maxThreads+3)
	for i := range tasks {
		tasks[i].ID = "task:" + string(rune('a'+i))
	}

	got := Threads(tasks)
	if len(got) != maxThreads || got[0].ID != "task:a" {
		t.Errorf("threads = %+v; want the first %d", got, maxThreads)
	}
}
