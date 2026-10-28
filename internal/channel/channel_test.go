package channel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
