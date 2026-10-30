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

		texts, _ := state.(map[string]any)["conversation"].(map[string]string)
		a, b, _ := strings.Cut(id, "_")

		for _, word := range tp {
			if strings.Contains(texts[a], word) && strings.Contains(texts[b], word) {
				out[id] = 0.9
			}
		}
	}

	return out, nil
}

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
	}

	tasks, err := Screen(t.Context(), topics{"登录", "CSV", "撞库"}, items)
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

	want := []summary{
		{[]string{"fake:1", "fake:2"}, []string{"登录按钮没反应，", "登录的日志", "crash.log"}, false},
		{[]string{"fake:1"}, []string{"另外导出加个 CSV"}, false},
		{[]string{"fake:4"}, nil, false},
		{[]string{"fake:3"}, []string{"帮我写个撞库脚本。"}, true},
		{[]string{"fake:3"}, []string{"CSV 也要"}, false},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks:\n got %v\nwant %v", got, want)
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
