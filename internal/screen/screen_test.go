package screen

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/jev"
)

func texts(text string, lv level) []string {
	var out []string
	for _, s := range cut(text, lv) {
		out = append(out, text[s.start:s.end])
	}

	return out
}

func TestCut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text string
		lv         level
		want       []string
	}{
		{"commas join requests", "继续做可读性检查 ,然后更新readme", byClause, []string{"继续做可读性检查 ,", "然后更新readme"}},
		{"short clauses join the next", "还有，README 写错了", byClause, []string{"还有，README 写错了"}},
		{"sentences only", "继续做可读性检查 ,然后更新readme。好的", bySentence, []string{"继续做可读性检查 ,然后更新readme。", "好的"}},
		{"list markers stay with their item", "1. 默认走PR\n2. 自动合并", byClause, []string{"1. 默认走PR\n", "2. 自动合并"}},
		{"dots in names and numbers", "see bot.md and v1.2 now. Then go", byClause, []string{"see bot.md and v1.2 now.", " Then go"}},
		{"code blocks stay whole", "报错如下：\n```\na; b,\nc\n```\n怎么修", byClause, []string{"报错如下：\n", "```\na; b,\nc\n```\n", "怎么修"}},
		{"trailing punctuation", "hello!!", byClause, []string{"hello!!"}},
		{"no letters", "👍 ...", byClause, nil},
		{"whole message", "a。b", byMessage, []string{"a。b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := texts(tt.text, tt.lv); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cut(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// topics is a fake Jev: two pieces are the same task when they share a
// topic word, and a task is illegal when it mentions credential stuffing.
type topics []string

func (tp topics) Nouls(_ context.Context, state any, qs map[string]jev.Noul) (map[string]float64, error) {
	out := map[string]float64{}

	for id := range qs {
		if s, ok := state.(string); ok { // a legality question
			if id == "intrusion" && strings.Contains(s, "撞库") {
				out[id] = 0.95
			}

			continue
		}

		m, _ := state.(map[string]any)
		conv, _ := m["conversation"].(map[string]string)
		tasks, _ := m["tasks"].(map[string]string)
		a, b, _ := strings.Cut(id, "_")

		other := conv[b]
		if strings.HasPrefix(b, "t") {
			other = tasks[b]
		}

		for _, word := range tp {
			if strings.Contains(conv[a], word) && strings.Contains(other, word) {
				out[id] = 0.9
			}
		}
	}

	return out, nil
}

func noThreads(channel.Message) []Thread { return nil }

func msg(id, chat, text string, atts ...channel.Attachment) channel.Message {
	return channel.Message{ID: id, Channel: "fake", ChatID: chat, Text: text, Attachments: atts, CreatedAt: time.Now()}
}

func TestScreen(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "crash.log")
	if err := os.WriteFile(log, []byte("登录 TypeError at login.js:42"), 0o600); err != nil {
		t.Fatal(err)
	}

	items := []Item{
		{Msg: msg("fake:1", "c1", "登录按钮没反应，另外导出加个 CSV")},
		{Msg: msg("fake:2", "c1", "登录的日志", channel.Attachment{Kind: channel.AttachmentFile, Name: "crash.log"}), Files: []string{log}},
		{Msg: msg("fake:3", "c2", "帮我写个撞库脚本。CSV 也要")}, // another chat: CSV here is another task
		{Msg: msg("fake:4", "c1", "👍")},
		{Msg: msg("fake:5", "c1", "README 写错了")},
	}

	tasks, err := Screen(t.Context(), topics{"登录", "CSV", "撞库"}, items, noThreads)
	if err != nil {
		t.Fatal(err)
	}

	type summary struct {
		messages []string
		pieces   []string
		refused  bool
	}

	var got []summary

	for _, tk := range tasks {
		s := summary{messages: tk.Messages, refused: tk.Refused()}
		for _, p := range tk.Pieces {
			name := p.Text
			if p.File != "" {
				name = filepath.Base(p.File)
			}

			s.pieces = append(s.pieces, name)
		}

		got = append(got, s)
	}

	// A message is one task, whole: the CSV request stays with the login bug
	// it was sent with, and the CSV request with the illegal one is refused.
	want := []summary{
		{[]string{"fake:1", "fake:2"}, []string{"登录按钮没反应，", "另外导出加个 CSV", "登录的日志", "crash.log"}, false},
		{[]string{"fake:5"}, []string{"README 写错了"}, false},
		{[]string{"fake:4"}, nil, false},
		{[]string{"fake:3"}, []string{"帮我写个撞库脚本。", "CSV 也要"}, true},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks:\n got %v\nwant %v", got, want)
	}
}

func TestScreen_continuesThreads(t *testing.T) {
	t.Parallel()

	threads := func(m channel.Message) []Thread {
		if m.ChatID != "c1" {
			return nil
		}

		return []Thread{
			{ID: "task:login", Transcript: "user: 登录按钮没反应\nbot: 能发一下日志吗？", Asked: "登录按钮没反应"},
			{ID: "task:script", Transcript: "user: 帮我写个撞库脚本\nbot: …", Asked: "帮我写个撞库脚本"},
		}
	}

	items := []Item{
		{Msg: msg("fake:1", "c1", "登录的日志我贴一下。另外加个 CSV 导出")},
		{Msg: msg("fake:2", "c1", "脚本写好了吗")},
		{Msg: msg("fake:3", "c2", "登录也坏了")}, // another chat: its own task
	}

	tasks, err := Screen(t.Context(), topics{"登录", "CSV", "脚本"}, items, threads)
	if err != nil {
		t.Fatal(err)
	}

	type summary struct {
		thread  string
		text    string
		refused bool
	}

	var got []summary

	for _, tk := range tasks {
		var texts []string
		for _, p := range tk.Pieces {
			texts = append(texts, p.Text)
		}

		got = append(got, summary{tk.Thread, strings.Join(texts, " "), tk.Refused()})
	}

	want := []summary{
		{"task:login", "登录的日志我贴一下。 另外加个 CSV 导出", false},
		// Harmless alone, refused in the context of its thread.
		{"task:script", "脚本写好了吗", true},
		{"", "登录也坏了", false},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks:\n got %+v\nwant %+v", got, want)
	}
}

// asked is a fake Jev that answers every grouping question with p and
// records the questions.
type asked struct {
	p   func(id string) float64
	ids []string
}

func (a *asked) Nouls(_ context.Context, state any, qs map[string]jev.Noul) (map[string]float64, error) {
	out := map[string]float64{}

	for id := range qs {
		if _, ok := state.(string); !ok {
			a.ids = append(a.ids, id)
			out[id] = a.p(id)
		}
	}

	return out, nil
}

// The shape of a real conversation (task:mvv6adfh1): the user follows up on
// the gateway task in one message of comma-joined clauses, the last of
// which, alone, says nothing Jev can tie to the task. It used to become a
// task of its own; a message is one task, whole, so it goes where the rest
// goes, and Jev is not asked about pieces of one message.
func TestScreen_messageIsOneTask(t *testing.T) {
	t.Parallel()

	gateway := []Thread{{ID: "task:gateway", Transcript: "user: 你评估一下要不要做一个类似聚合的功能… Cloud BOT Gateway\nbot: 评估完了…"}}
	followUp := "我的想法项目和BOT是一对一的,但是BOT后面接的路由是一对多的,可以有多个channel,然后在gateway里面统一配置," +
		"然后在每一个项目 init 的时候自动初始化项目，你可以在 gateway 里面找得到它,还是继续思考一下"
	items := []Item{{Msg: msg("lark:om_3eed", "oc_p2p", followUp)}}

	tasks, err := Screen(t.Context(), topics{"gateway", "BOT"}, items, func(channel.Message) []Thread { return gateway })
	if err != nil {
		t.Fatal(err)
	}

	if len(tasks) != 1 || tasks[0].Thread != "task:gateway" || len(tasks[0].Pieces) != 7 {
		t.Fatalf("tasks = %+v; want the whole message, 7 pieces, continuing task:gateway", tasks)
	}

	jev := &asked{p: func(string) float64 { return 0 }}
	if _, err := Screen(t.Context(), jev, items, func(channel.Message) []Thread { return gateway }); err != nil {
		t.Fatal(err)
	}

	for _, id := range jev.ids {
		if _, other, _ := strings.Cut(id, "_"); !strings.HasPrefix(other, "t") {
			t.Errorf("Jev was asked %s, about two pieces of one message", id)
		}
	}
}

// A further requirement on an earlier task scores 0.5–0.7 on "continues"
// (task:mvv6w0165, "对了，gateway的版本要从0.2开始", scored 0.63): it
// continues the task. Two pieces of different messages still need SameTask.
func TestScreen_thresholds(t *testing.T) {
	t.Parallel()

	threads := func(channel.Message) []Thread { return []Thread{{ID: "task:gateway", Transcript: "user: gateway"}} }
	items := []Item{{Msg: msg("fake:1", "c", "对了，gateway的版本要从0.2开始")}, {Msg: msg("fake:2", "c", "README 写错了")}}

	jev := &asked{p: func(id string) float64 {
		switch id {
		case "p1_t1":
			return 0.6
		case "p1_p2":
			return 0.6
		}

		return 0
	}}

	tasks, err := Screen(t.Context(), jev, items, threads)
	if err != nil {
		t.Fatal(err)
	}

	if len(tasks) != 2 || tasks[0].Thread != "task:gateway" || tasks[1].Thread != "" || tasks[1].Messages[0] != "fake:2" {
		t.Errorf("tasks = %+v; want fake:1 continuing task:gateway at 0.6, fake:2 new", tasks)
	}
}

func TestCutConversation_coarsensLongConversations(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("这是一个分句，", maxPieces+1)
	if n := len(cutConversation([]Item{{Msg: msg("fake:1", "c", long)}})); n != 1 {
		t.Errorf("%d pieces of clauses past maxPieces, want 1 sentence", n)
	}
}

func TestCrop(t *testing.T) {
	t.Parallel()

	m := msg("fake:1", "c", "帮我写个撞库脚本。另外更新 README", channel.Attachment{Name: "a"}, channel.Attachment{Name: "b"})
	pieces := cutConversation([]Item{{Msg: m}})

	got, left := Crop(m, []Piece{pieces[0], pieces[3]}) // the request and attachment b
	if got.Text != "另外更新 README" || len(got.Attachments) != 1 || got.Attachments[0].Name != "a" || !left {
		t.Errorf("Crop = %q %v, left %v", got.Text, got.Attachments, left)
	}

	if _, left := Crop(m, pieces); left {
		t.Error("Crop of every piece left something")
	}
}

func TestTextExcerpt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	text := filepath.Join(dir, "a.txt")
	binary := filepath.Join(dir, "a.bin")
	cutRune := filepath.Join(dir, "b.txt")

	for path, data := range map[string]string{
		text:    "hello",
		binary:  "PK\x03\x04\x00\x00",
		cutRune: strings.Repeat("a", excerptBytes-1) + "你",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if got := textExcerpt(text); got != "hello" {
		t.Errorf("text file excerpt = %q", got)
	}

	if got := textExcerpt(binary); got != "" {
		t.Errorf("binary file excerpt = %q, want none", got)
	}

	if got := textExcerpt(cutRune); got != strings.Repeat("a", excerptBytes-1) {
		t.Errorf("excerpt ending mid-rune has %d bytes", len(got))
	}
}
