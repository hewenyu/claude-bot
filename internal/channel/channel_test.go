package channel

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewenyu/claude-bot/internal/transport"
)

func TestSplitID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id, channel, platformID string
		wantErr                 bool
	}{
		{id: "lark:om_1", channel: "lark", platformID: "om_1"},
		{id: "discord:123/456", channel: "discord", platformID: "123/456"},
		{id: "lark:oc_x:y", channel: "lark", platformID: "oc_x:y"},
		{id: "om_1", wantErr: true},
		{id: ":om_1", wantErr: true},
		{id: "lark:", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()

			ch, pid, err := SplitID(tt.id)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidID) {
					t.Fatalf("SplitID(%q) err = %v, want ErrInvalidID", tt.id, err)
				}

				return
			}

			if err != nil || ch != tt.channel || pid != tt.platformID {
				t.Errorf("SplitID(%q) = %q, %q, %v", tt.id, ch, pid, err)
			}

			if back := ID(ch, pid); back != tt.id {
				t.Errorf("ID round trip = %q, want %q", back, tt.id)
			}
		})
	}
}

func TestPathSafe(t *testing.T) {
	t.Parallel()

	if got := PathSafe("lark:om_../../x"); got != "lark_om_.._.._x" {
		t.Errorf("PathSafe() = %q; separators must not survive", got)
	}
}

func TestBaseName(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"crash.log":         "crash.log",
		"../../etc/passwd":  "passwd",
		"/abs/dir/shot.png": "shot.png",
		"":                  "",
		".":                 "",
		"..":                "",
		"/":                 "",
	}

	for in, want := range tests {
		if got := BaseName(in); got != want {
			t.Errorf("BaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopedID(t *testing.T) {
	t.Parallel()

	if chat, msg, err := LocateScoped("telegram:" + ScopedID("-1001", "9001")); err != nil || chat != "-1001" || msg != "9001" {
		t.Errorf("LocateScoped(ScopedID()) = %q, %q, %v", chat, msg, err)
	}

	for _, bad := range []string{"discord:9001", "discord:/9001", "discord:42/", "42/9001"} {
		if _, _, err := LocateScoped(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("LocateScoped(%q) error = %v, want ErrInvalidID", bad, err)
		}
	}
}

func TestSaveFile(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "new", "dir")

	path, err := SaveFile(dir, "crash.log", []byte("boom"))
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(dir, "crash.log") {
		t.Errorf("SaveFile() = %s", path)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("saved file mode = %v, %v; want 600", info.Mode().Perm(), err)
	}
}

func TestSaveStream(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 1000)...)

	path, err := saveStream(dir, "k/1", bytes.NewReader(png), int64(len(png)))
	if err != nil || path != filepath.Join(dir, "k_1.png") {
		t.Fatalf("saveStream() = %s, %v; want k_1.png, the extension sniffed", path, err)
	}

	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, png) {
		t.Errorf("saved %d bytes, %v; want all %d", len(data), err, len(png))
	}

	if path, err := saveStream(dir, "k", strings.NewReader("hi"), 10, "notes"); err != nil || filepath.Base(path) != "notes.txt" {
		t.Errorf("saveStream() of a short text = %s, %v; want notes.txt", path, err)
	}
}

func TestSaveStream_refusesOversizedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := saveStream(dir, "k", bytes.NewReader(make([]byte, 101)), 100, "big.bin"); !errors.Is(err, transport.ErrTooLarge) {
		t.Fatalf("saveStream() over the limit = %v; want ErrTooLarge", err)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind %v; want nothing", entries)
	}
}

func TestMemo(t *testing.T) {
	t.Parallel()

	var m Memo[int]

	calls := 0
	fetch := func(context.Context) (int, error) {
		if calls++; calls == 1 {
			return 0, errors.New("blip")
		}

		return 42, nil
	}

	if _, err := m.Get(t.Context(), fetch); err == nil {
		t.Fatal("Get() hid the first failure")
	}

	for range 2 {
		if v, err := m.Get(t.Context(), fetch); v != 42 || err != nil {
			t.Errorf("Get() = %d, %v; want 42", v, err)
		}
	}

	if calls != 2 {
		t.Errorf("fetched %d times, want 2: the failure retried, the success kept", calls)
	}
}

func TestMentionsSender(t *testing.T) {
	t.Parallel()

	for typ, want := range map[ChatType]bool{ChatDirect: false, ChatGroup: true, "": true} {
		if got := MentionsSender(Message{ChatType: typ}); got != want {
			t.Errorf("MentionsSender(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "png", data: []byte("\x89PNG\r\n\x1a\n0000"), want: ".png"},
		{name: "jpeg", data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0}, want: ".jpg"},
		{name: "text", data: []byte("panic: runtime error\n"), want: ".txt"},
		{name: "pdf", data: []byte("%PDF-1.7"), want: ".pdf"},
		{name: "unknown binary", data: []byte{0, 1, 2, 3}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Extension(tt.data); got != tt.want {
				t.Errorf("Extension() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUniqueNames(t *testing.T) {
	t.Parallel()

	atts := []Attachment{
		{Name: "image.png"},
		{Name: "a/image.png"},
		{},
		{Name: "IMAGE.png"},
		{Name: "image-2.png"},
		{Name: "notes"},
		{Name: "notes"},
	}
	UniqueNames(atts)

	want := []string{"image.png", "image-2.png", "", "IMAGE-3.png", "image-2-2.png", "notes", "notes-2"}
	for i, a := range atts {
		if a.Name != want[i] {
			t.Errorf("name %d = %q, want %q", i, a.Name, want[i])
		}
	}
}

func TestFileName(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG\r\n\x1a\n0000")

	tests := []struct {
		names []string
		want  string
	}{
		{names: []string{"own.txt", "served.txt"}, want: "own.txt"},
		{names: []string{"", "dir/served"}, want: "served.png"},
		{names: []string{"..", ""}, want: "k_1.png"},
		{want: "k_1.png"},
	}

	for _, tt := range tests {
		if got := FileName("k/1", png, tt.names...); got != tt.want {
			t.Errorf("FileName(%q) = %q, want %q", tt.names, got, tt.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()

	if KindOf("image/png") != AttachmentImage || KindOf("application/pdf") != AttachmentFile || KindOf("") != AttachmentFile {
		t.Error("KindOf tells images from files wrongly")
	}
}
