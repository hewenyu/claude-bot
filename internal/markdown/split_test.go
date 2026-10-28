package markdown

import (
	"reflect"
	"strings"
	"testing"
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
