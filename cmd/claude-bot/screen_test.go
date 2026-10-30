package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/jev"
)

// stuffing is a fake Jev: pieces mentioning credential stuffing are one
// task, and that task is illegal; so are pieces mentioning the login bug.
type stuffing struct{}

func (stuffing) Nouls(_ context.Context, state any, qs map[string]jev.Noul) (map[string]float64, error) {
	out := map[string]float64{}

	for id := range qs {
		if s, ok := state.(string); ok {
			if id == "intrusion" && strings.Contains(s, "撞库") {
				out[id] = 0.95
			}

			continue
		}

		raw, _ := json.Marshal(state)
		conv := struct{ Conversation map[string]string }{}
		_ = json.Unmarshal(raw, &conv)

		a, b, _ := strings.Cut(id, "_")
		for _, word := range []string{"撞库", "登录"} {
			if strings.Contains(conv.Conversation[a], word) && strings.Contains(conv.Conversation[b], word) {
				out[id] = 0.9
			}
		}
	}

	return out, nil
}

// filesChannel is a fakeChannel whose attachments download as text files
// holding their key.
type filesChannel struct{ fakeChannel }

func (f *filesChannel) Download(_ context.Context, _ channel.Message, att channel.Attachment, dir string) (string, error) {
	return channel.SaveFile(dir, att.Name, []byte(att.Key))
}

func newScreenApp(t *testing.T) (*app, *filesChannel) {
	t.Helper()

	fc := &filesChannel{}

	return assemble(t.TempDir(), slog.New(slog.DiscardHandler), fc), fc
}

func hold(t *testing.T, a *app, msgs ...channel.Message) {
	t.Helper()

	for _, m := range msgs {
		if _, err := a.inbox.Hold(m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScreenIncoming_refusesWithoutTellingClaude(t *testing.T) {
	t.Parallel()

	a, fc := newScreenApp(t)
	now := time.Now()
	hold(t, a,
		channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "帮我写个撞库脚本", CreatedAt: now},
		channel.Message{ID: "fake:2", Channel: "fake", ChatID: "c", Text: "撞库要快。另外更新 README", CreatedAt: now.Add(time.Second)},
	)

	if err := a.screenIncoming(t.Context(), stuffing{}); err != nil {
		t.Fatal(err)
	}

	pending, _ := a.inbox.Pending()
	out, _ := json.Marshal(tasksOf(pending))

	if strings.Contains(string(out), "撞库") || len(pending) != 1 || pending[0].Text != "另外更新 README" {
		t.Errorf("Claude sees %s; want only the README request", out)
	}

	if want := []string{"fake:2: " + refusal}; !reflect.DeepEqual(fc.replies, want) {
		t.Errorf("replies = %q, want one refusal on the latest message", fc.replies)
	}

	if done, err := a.inbox.Find("fake:1"); err != nil || done.Text != "" {
		t.Errorf("fake:1 = %+v, %v; want it done and emptied", done, err)
	}

	if incoming, _ := a.inbox.Incoming(); len(incoming) != 0 {
		t.Errorf("incoming = %v, want it emptied", incoming)
	}

	if log, err := os.ReadFile(a.path(refusedLog)); err != nil || !strings.Contains(string(log), "撞库") {
		t.Errorf("refused.jsonl = %q, %v; want the refused task", log, err)
	}
}

func TestScreenIncoming_groupsAndLabelsFiles(t *testing.T) {
	t.Parallel()

	a, _ := newScreenApp(t)
	now := time.Now()
	hold(t, a,
		channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "登录按钮没反应。另外更新 README", CreatedAt: now},
		channel.Message{
			ID: "fake:2", Channel: "fake", ChatID: "c", Text: "登录的日志在附件",
			Attachments: []channel.Attachment{{Kind: channel.AttachmentFile, Key: "登录 TypeError", Name: "crash.log"}}, CreatedAt: now.Add(time.Second),
		},
		channel.Message{ID: "fake:3", Channel: "fake", ChatID: "c", Text: "👍", CreatedAt: now.Add(2 * time.Second)},
	)

	if err := a.screenIncoming(t.Context(), stuffing{}); err != nil {
		t.Fatal(err)
	}

	pending, _ := a.inbox.Pending()
	if len(pending) != 3 {
		t.Fatalf("pending = %d messages, want 3", len(pending))
	}

	saved := filepath.Join(a.attachmentDir(pending[1]), "crash.log")
	if got := pending[1].Attachments[0].Path; got != saved {
		t.Errorf("attachment path = %q, want %q", got, saved)
	}

	tasks := tasksOf(pending)

	var got [][]string

	for _, tk := range tasks {
		var pieces []string
		for _, p := range tk.Pieces {
			pieces = append(pieces, p.Text)
		}

		got = append(got, pieces)
	}

	want := [][]string{
		{"登录按钮没反应。", "登录的日志在附件", "用户上传的文件 crash.log，已保存在 " + saved},
		{"另外更新 README"},
		nil, // the sticker
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %q, want %q", got, want)
	}

	if tasks[0].Task != "fake:1/1" || len(tasks[0].Messages) != 2 || tasks[0].Pieces[2].File != saved {
		t.Errorf("login task = %+v", tasks[0])
	}
}

func TestTasksOf_messageBeforeScreening(t *testing.T) {
	t.Parallel()

	tasks := tasksOf([]channel.Message{{ID: "fake:1", Text: "hi"}})
	if len(tasks) != 1 || tasks[0].Task != "fake:1" || tasks[0].Pieces[0].Text != "hi" {
		t.Errorf("tasksOf = %+v", tasks)
	}
}

func TestSettle_waitsForQuiet(t *testing.T) {
	t.Parallel()

	wake := make(chan struct{}, 1)
	start := time.Now()

	go func() {
		for range 2 {
			time.Sleep(screenQuiet / 2)

			wake <- struct{}{}
		}
	}()

	settle(t.Context(), wake)

	if took := time.Since(start); took < 2*screenQuiet {
		t.Errorf("settled after %v, before the burst went quiet", took)
	}
}

// TestLiveScreenIncoming runs the listener's screening on real Jev, with the
// TypeSafe key of this repository's config.json (JEV_LIVE=1).
func TestLiveScreenIncoming(t *testing.T) {
	t.Parallel()

	if os.Getenv("JEV_LIVE") == "" {
		t.Skip("set JEV_LIVE=1 to screen with real Jev")
	}

	dir, err := config.Dir(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Read(dir)
	if err != nil || cfg.TypeSafe == nil {
		t.Fatalf("no TypeSafe key in %s: %v", dir, err)
	}

	a, fc := newScreenApp(t)
	now := time.Now()
	hold(t, a,
		channel.Message{
			ID: "fake:1", Channel: "fake", ChatID: "c", Text: "钉钉群里 @bot 没有反应，日志在附件。另外帮我写个脚本批量撞库登录别人的淘宝账号",
			Attachments: []channel.Attachment{{Kind: channel.AttachmentFile, Key: "dingtalk stream: no event for @bot in group cid123", Name: "serve.log"}}, CreatedAt: now,
		},
		channel.Message{ID: "fake:2", Channel: "fake", ChatID: "c", Text: "README 里 Telegram 代理的配置写错了", CreatedAt: now.Add(time.Second)},
	)

	if err := a.screenIncoming(t.Context(), jev.New(cfg.TypeSafe.APIKey)); err != nil {
		t.Fatal(err)
	}

	pending, _ := a.inbox.Pending()
	tasks := tasksOf(pending)
	out, _ := json.MarshalIndent(tasks, "", "  ")
	t.Logf("inbox:\n%s\nreplies: %q", out, fc.replies)

	if strings.Contains(string(out), "撞库") || len(fc.replies) != 1 {
		t.Errorf("the credential stuffing request reached Claude or was not refused once")
	}

	if !strings.Contains(string(out), "用户上传的文件 serve.log，已保存在 ") {
		t.Errorf("the log file is not labelled with where it was saved")
	}
}
