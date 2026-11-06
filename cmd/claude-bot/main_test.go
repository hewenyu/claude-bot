package main

import (
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/hewenyu/claude-bot/internal/inbox"
)

func TestTextArg(t *testing.T) {
	t.Parallel()

	got, err := textArg([]string{"fixed", "in", "v1.2.3"})
	if err != nil || got != "fixed in v1.2.3" {
		t.Errorf("textArg(args) = %q, %v", got, err)
	}
}

// The usage and the commands come from one table: every command is listed
// but the hook, which is for the agents the gateway runs.
func TestUsage_listsEveryCommand(t *testing.T) {
	t.Parallel()

	text := usage()

	for _, s := range sections() {
		for _, c := range s.commands {
			listed := strings.Contains(text, "claude-bot "+c.name) || strings.Contains(text, "| "+c.name+" ")
			if listed == (c.name == "hook") {
				t.Errorf("command %s listed: %v", c.name, listed)
			}

			if run, ok := commandNamed(c.name); !ok || run == nil {
				t.Errorf("command %s does not run", c.name)
			}
		}
	}

	for _, want := range []string{"gateway open", "gateway serve", "github mine"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

func TestCommands_rejectStrayArguments(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"inbox", "now"},
		{"status", "x"},
		{"get"},
		{"get", "a", "b"},
		{"serve", "x"},
		{"gateway", "status", "x"},
		{"gateway", "remove"},
		{"init", "lark"},
	} {
		if code := run(t.Context(), args); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestRun_usage(t *testing.T) {
	t.Parallel()

	if code := run(t.Context(), []string{"no-such-command"}); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}

	if code := run(t.Context(), []string{"done"}); code != 2 {
		t.Errorf("done without ids exit = %d, want 2", code)
	}
}

func TestParseReplyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want replyArgs
	}{
		{name: "flag first", args: []string{"--keep", "lark:om_1", "on it"}, want: replyArgs{id: "lark:om_1", text: []string{"on it"}, keep: true}},
		{name: "flag after id", args: []string{"lark:om_1", "--keep", "on it"}, want: replyArgs{id: "lark:om_1", text: []string{"on it"}, keep: true}},
		{name: "markdown list is text", args: []string{"lark:om_1", "- fixed"}, want: replyArgs{id: "lark:om_1", text: []string{"- fixed"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseReplyArgs(tt.args)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseReplyArgs(%q) = %+v, %v; want %+v", tt.args, got, err, tt.want)
			}
		})
	}
}

func TestDone_continuesPastUnknownID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LARK_APP_ID", "cli_test")
	t.Setenv("LARK_APP_SECRET", "secret")

	b := inbox.New(filepath.Join(root, ".claude-bot", "inbox"))
	tk := inbox.Task{ID: "task:b", Channel: "lark", Entries: []inbox.Entry{{Kind: inbox.EntryUser, Message: "lark:om_b", At: time.Now()}}}

	if err := b.Locked(func(tx *inbox.Tx) error { return tx.SaveTask(tk, true) }); err != nil {
		t.Fatal(err)
	}

	if code := run(t.Context(), []string{"-C", root, "done", "task:missing", "task:b"}); code != 1 {
		t.Errorf("exit = %d, want 1 for the unknown id", code)
	}

	if pending, err := b.PendingTasks(); err != nil || len(pending) != 0 {
		t.Errorf("pending = %d, %v; the known task must still be closed", len(pending), err)
	}
}

func TestBuildVersion(t *testing.T) {
	t.Parallel()

	const revision = "56f0db8a1b2c3d4e5f60718293a4b5c6d7e8f901"

	rev := []debug.BuildSetting{{Key: "vcs.revision", Value: revision}}
	dirty := []debug.BuildSetting{{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "true"}}

	for _, tc := range []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"go install @tag", debug.BuildInfo{Main: debug.Module{Version: "v0.2.6"}}, "v0.2.6"},
		{"pseudo-version", debug.BuildInfo{Main: debug.Module{Version: "v0.2.7-0.20261030160000-56f0db8a1b2c+dirty"}, Settings: rev}, "v0.2.7-0.20261030160000-56f0db8a1b2c+dirty"},
		{"devel with vcs", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: rev}, "dev-56f0db8a1b2c"},
		{"devel, modified", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: dirty}, "dev-56f0db8a1b2c-dirty"},
		{"nothing", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, ""},
	} {
		if got := buildVersion(&tc.info); got != tc.want {
			t.Errorf("%s: buildVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}
