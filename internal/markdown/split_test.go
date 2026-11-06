package markdown

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{name: "short text is one message", text: "hello\nworld", limit: 20, want: []string{"hello\nworld"}},
		{name: "cuts at line breaks", text: "aaaa\nbbbb\ncccc", limit: 14, want: []string{"aaaa\nbbbb", "cccc"}},
		{name: "cuts a long line", text: "abcdefghij", limit: 8, want: []string{"abcd", "efgh", "ij"}},
		{name: "counts characters, not bytes", text: "你好世界你好", limit: 7, want: []string{"你好世", "界你好"}},
		{
			name:  "a cut code block is closed and reopened",
			text:  "intro\n```go\nline1\nline2\n```\nend",
			limit: 23,
			want:  []string{"intro\n```go\nline1\n```", "```go\nline2\n```\nend"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Split(tt.text, tt.limit)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Split(%q, %d)\n got  %q\n want %q", tt.text, tt.limit, got, tt.want)
			}

			for _, part := range got {
				if n := utf8.RuneCountInString(part); n > tt.limit {
					t.Errorf("part %q has %d characters, limit %d", part, n, tt.limit)
				}
			}
		})
	}
}

func TestSplit_longFenceLineTerminates(t *testing.T) {
	t.Parallel()

	text := "```" + strings.Repeat("x", 300) + "\n" + strings.Repeat("code\n", 100) + "```"
	for _, part := range Split(text, 120) {
		if n := utf8.RuneCountInString(part); n > 120 {
			t.Fatalf("part has %d characters, limit 120", n)
		}
	}
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func TestSplitFunc_utf16(t *testing.T) {
	t.Parallel()

	// Each emoji is one rune but two UTF-16 units.
	text := strings.Repeat("😀", 10) + "\n" + strings.Repeat("a", 5)

	got := SplitFunc(text, 12, utf16Len)
	want := []string{"😀😀😀😀", "😀😀😀😀", "😀😀", "aaaaa"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitFunc() = %q, want %q", got, want)
	}

	for _, part := range got {
		if n := utf16Len(part); n > 12 {
			t.Errorf("part %q is %d UTF-16 units, limit 12", part, n)
		}
	}
}

func TestSplitFunc_runeLongerThanTheLimitEnds(t *testing.T) {
	t.Parallel()

	// Room for one unit after the fence margin, but every rune takes two.
	got := SplitFunc("😀😀", 5, utf16Len)
	if want := []string{"😀", "😀"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SplitFunc() = %q, want %q", got, want)
	}
}

func TestSendParts(t *testing.T) {
	t.Parallel()

	errDown := errors.New("down")

	var sent []string

	err := SendParts([]string{"a", "b", "c"}, func(i int, part string) error {
		if i == 2 {
			return errDown
		}

		sent = append(sent, part)

		return nil
	})
	if !errors.Is(err, errDown) || !strings.Contains(err.Error(), "sent 2 of 3 parts") {
		t.Errorf("SendParts() = %v, want the failure after 2 of 3 parts", err)
	}

	if !reflect.DeepEqual(sent, []string{"a", "b"}) {
		t.Errorf("sent %q, want a and b", sent)
	}

	if err := SendParts([]string{"a"}, func(int, string) error { return errDown }); err == nil || err.Error() != "down" {
		t.Errorf("SendParts() of one part = %v, want the error as is", err)
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		s    string
		n    int
		want string
	}{
		{s: "short", n: 5, want: "short"},
		{s: "longer", n: 5, want: "long…"},
		{s: "你好世界你好", n: 3, want: "你好…"},
	} {
		if got := Truncate(tt.s, tt.n); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
