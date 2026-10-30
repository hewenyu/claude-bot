package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
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
		conv := struct{ Conversation, Tasks map[string]string }{}
		_ = json.Unmarshal(raw, &conv)

		a, b, _ := strings.Cut(id, "_")

		other := conv.Conversation[b]
		if strings.HasPrefix(b, "t") {
			other = conv.Tasks[b]
		}

		for _, word := range []string{"撞库", "登录"} {
			if strings.Contains(conv.Conversation[a], word) && strings.Contains(other, word) {
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

// pendingText is every pending task's user entries, one string per task.
func pendingText(t *testing.T, a *app) []string {
	t.Helper()

	tasks, err := a.inbox.PendingTasks()
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, tk := range tasks {
		var texts []string

		for _, e := range tk.Entries {
			if e.Kind == inbox.EntryUser {
				texts = append(texts, e.Text)
			}
		}

		out = append(out, strings.Join(texts, " "))
	}

	return out
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

	if got, want := pendingText(t, a), []string{"另外更新 README"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Claude sees %q, want %q", got, want)
	}

	if want := []string{"fake:2: " + refusal}; !reflect.DeepEqual(fc.replies, want) {
		t.Errorf("replies = %q, want one refusal on the latest message", fc.replies)
	}

	if m, err := a.inbox.Find("fake:1"); err != nil || m.Text != "" || len(m.Pieces) != 0 {
		t.Errorf("fake:1 = %+v, %v; want it archived, emptied, in no task", m, err)
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

	saved := filepath.Join(a.attachmentDir(channel.Message{ID: "fake:2"}), "crash.log")
	want := []string{
		"登录按钮没反应。 登录的日志在附件 用户上传的文件 crash.log，已保存在 " + saved,
		"另外更新 README",
		"", // the sticker
	}

	if got := pendingText(t, a); !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %q, want %q", got, want)
	}

	m, err := a.inbox.Find("fake:2")
	if err != nil || m.Attachments[0].Path != saved || len(m.Pieces) != 2 || m.Pieces[1].File != saved {
		t.Errorf("archived fake:2 = %+v, %v; want its file's path and its pieces labelled", m, err)
	}
}

// screenOne holds one message and screens it.
func screenOne(t *testing.T, a *app, m channel.Message) {
	t.Helper()

	hold(t, a, m)

	if err := a.screenIncoming(t.Context(), stuffing{}); err != nil {
		t.Fatal(err)
	}
}

func TestScreenIncoming_followUpReopensTask(t *testing.T) {
	t.Parallel()

	a, _ := newScreenApp(t)
	now := time.Now()

	screenOne(t, a, channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "登录按钮没反应", CreatedAt: now})

	tasks, _ := a.inbox.PendingTasks()
	id := tasks[0].ID

	if _, err := a.show(tasks); err != nil {
		t.Fatal(err)
	}

	if err := a.closeTask(id, &inbox.Entry{Kind: inbox.EntryBot, At: now, Text: "修好了，PR #16"}, true); err != nil {
		t.Fatal(err)
	}

	// Someone else follows up an hour later in the group; another asks something new.
	hold(t, a,
		channel.Message{ID: "fake:2", Channel: "fake", ChatID: "c", Text: "登录还是不行", Sender: channel.User{ID: "u2"}, CreatedAt: now.Add(time.Hour)},
		channel.Message{ID: "fake:3", Channel: "fake", ChatID: "c", Text: "README 写错了", CreatedAt: now.Add(time.Hour + time.Second)},
	)

	if err := a.screenIncoming(t.Context(), stuffing{}); err != nil {
		t.Fatal(err)
	}

	tasks, _ = a.inbox.PendingTasks()
	if len(tasks) != 2 || tasks[0].ID != id {
		t.Fatalf("pending = %+v; want the login task reopened, then a new one", tasks)
	}

	line := lineOf(tasks[0])
	if len(line.Entries) != 3 || line.Entries[1].Kind != inbox.EntryBot || line.Entries[0].New || !line.Entries[2].New || line.LastUserMessage() != "fake:2" {
		t.Errorf("reopened task = %+v; want the old turn, the bot's reply, then the new follow-up", line)
	}
}

func TestScreenIncoming_oldTasksDoNotReopen(t *testing.T) {
	t.Parallel()

	a, _ := newScreenApp(t)
	long := time.Now().Add(-reopenWindow - time.Hour)

	screenOne(t, a, channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "登录按钮没反应", CreatedAt: long})

	tasks, _ := a.inbox.PendingTasks()
	if err := a.closeTask(tasks[0].ID, nil, true); err != nil {
		t.Fatal(err)
	}

	screenOne(t, a, channel.Message{ID: "fake:2", Channel: "fake", ChatID: "c", Text: "登录还是不行", CreatedAt: time.Now()})

	if tasks, _ = a.inbox.PendingTasks(); len(tasks) != 1 || tasks[0].ID == "" || len(tasks[0].Entries) != 1 {
		t.Errorf("pending = %+v; past the reopen window a follow-up is a new task", tasks)
	}
}

func TestCloseTask_keepsWhatArrivedMeanwhile(t *testing.T) {
	t.Parallel()

	a, _ := newScreenApp(t)
	now := time.Now()

	screenOne(t, a, channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "登录按钮没反应", CreatedAt: now})

	tasks, _ := a.inbox.PendingTasks()
	if _, err := a.show(tasks); err != nil { // Claude reads it…
		t.Fatal(err)
	}

	// …and while it works, a follow-up arrives.
	screenOne(t, a, channel.Message{ID: "fake:2", Channel: "fake", ChatID: "c", Text: "登录页也白屏了", CreatedAt: now.Add(time.Minute)})

	if err := a.closeTask(tasks[0].ID, &inbox.Entry{Kind: inbox.EntryBot, At: now, Text: "修好了"}, true); err != nil {
		t.Fatal(err)
	}

	pending, _ := a.inbox.PendingTasks()
	if len(pending) != 1 {
		t.Fatalf("pending = %d tasks; the follow-up must keep the task pending", len(pending))
	}

	line := lineOf(pending[0])
	if line.Entries[0].New || !line.Entries[1].New {
		t.Errorf("entries = %+v; want only the follow-up new", line.Entries)
	}
}

func TestTaskOf(t *testing.T) {
	t.Parallel()

	a, _ := newScreenApp(t)
	screenOne(t, a, channel.Message{ID: "fake:1", Channel: "fake", ChatID: "c", Text: "登录按钮没反应。另外更新 README", CreatedAt: time.Now()})

	if _, err := a.taskOf("fake:1"); !errors.Is(err, errUsage) {
		t.Errorf("taskOf a message in two tasks = %v; want a usage error naming them", err)
	}

	tasks, _ := a.inbox.PendingTasks()
	if got, err := a.taskOf(tasks[0].ID); got != tasks[0].ID || err != nil {
		t.Errorf("taskOf(%s) = %q, %v", tasks[0].ID, got, err)
	}

	if got, err := a.taskOf("fake:unknown"); got != "" || err != nil {
		t.Errorf("taskOf an unknown message = %q, %v; want none, to reply without a task", got, err)
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

	tasks, _ := a.inbox.PendingTasks()
	lines := make([]taskLine, len(tasks))

	for i, tk := range tasks {
		lines[i] = lineOf(tk)
	}

	out, _ := json.MarshalIndent(lines, "", "  ")
	t.Logf("inbox:\n%s\nreplies: %q", out, fc.replies)

	if strings.Contains(string(out), "撞库") || len(fc.replies) != 1 {
		t.Errorf("the credential stuffing request reached Claude or was not refused once")
	}

	if !strings.Contains(string(out), "用户上传的文件 serve.log，已保存在 ") {
		t.Errorf("the log file is not labelled with where it was saved")
	}
}
