package term

import (
	"bytes"
	"slices"
	"testing"
)

func TestTitleParserFindsTitlesAcrossWrites(t *testing.T) {
	t.Parallel()

	var p TitleParser

	in := []byte("hi\x1b]0;✳ Claude Code\x07more\x1b]2;◐ Fix tests\x1b\\\x1b]8;;http://x\x07\x1b[31m")

	var got []string
	for i := range in { // one byte at a time: every split point
		got = append(got, p.Feed(in[i:i+1])...)
	}

	want := []string{"✳ Claude Code", "◐ Fix tests"}
	if !slices.Equal(got, want) {
		t.Fatalf("titles = %q, want %q", got, want)
	}
}

func TestRingKeepsTheTailFromALineStart(t *testing.T) {
	t.Parallel()

	r := NewRing(16)
	_, _ = r.Write([]byte("0123456789\nabcdef"))
	_, _ = r.Write([]byte("ghij"))

	got := r.Bytes()
	if !bytes.Equal(got, []byte("abcdefghij")) {
		t.Fatalf("ring = %q", got)
	}

	_, _ = r.Write(bytes.Repeat([]byte("x"), 40))
	if n := len(r.Bytes()); n != 16 {
		t.Fatalf("ring holds %d bytes, want 16", n)
	}
}

func TestStripQueriesKeepsTheScreen(t *testing.T) {
	t.Parallel()

	in := "\x1b[>0q\x1b[c\x1b[?2026$p\x1b]11;?\x07\x1b[6n\x1b[38;2;1;2;3mhi\x1b[39m\x1b[2C\x1b[?25l\x1b]0;✳ x\x07\x1b[18t"
	want := "\x1b[38;2;1;2;3mhi\x1b[39m\x1b[2C\x1b[?25l\x1b]0;✳ x\x07"

	if got := string(StripQueries([]byte(in))); got != want {
		t.Fatalf("stripped = %q\nwant      %q", got, want)
	}
}

func TestIsResponse(t *testing.T) {
	t.Parallel()

	for _, r := range []string{"\x1b[?1;2c", "\x1b[>0;276;0c\x1bP>|xterm.js(6.0.0)\x1b\\", "\x1b[12;40R", "\x1b]11;rgb:1414/1313/1111\x1b\\", "\x1b[I", "\x1b[?2026;2$y"} {
		if !IsResponse([]byte(r)) {
			t.Errorf("%q is a response", r)
		}
	}

	for _, k := range []string{"\r", "\x1b", "\x1b[B", "1", "y", "\x1b[B\r", "\x1b[?1;2cy"} {
		if IsResponse([]byte(k)) {
			t.Errorf("%q is keys", k)
		}
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()

	in := "\x1b[38;2;1;2;3mDo\x1b[39m\x1b[2Cyou\x1b]0;✳ x\x07 trust\x1bM"
	if got := StripANSI(in); got != "Doyou trust" {
		t.Fatalf("stripped = %q", got)
	}
}

func TestClip(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ s, left, right string }{
		{"你好世界", "你好…", "…世界"},
		{"ab", "ab", "ab"},
	} {
		if got := Clip(c.s, 2); got != c.left {
			t.Errorf("Clip(%q) = %q, want %q", c.s, got, c.left)
		}

		if got := ClipLeft(c.s, 2); got != c.right {
			t.Errorf("ClipLeft(%q) = %q, want %q", c.s, got, c.right)
		}
	}
}
