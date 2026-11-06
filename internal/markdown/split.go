// Package markdown holds the Markdown helpers the channel adapters share.
package markdown

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	fence       = "```"
	maxFenceLen = 100 // longest fence line repeated when a code block is cut
)

// Split cuts Markdown into messages of at most limit characters (runes),
// preferring line breaks. A code block cut in two is closed at the end of one
// message and reopened at the start of the next, so both still render as
// code.
func Split(text string, limit int) []string {
	return SplitFunc(text, limit, utf8.RuneCountInString)
}

// SplitFunc is Split for a platform that measures text its own way, e.g. in
// UTF-16 code units or encoded bytes: no message is longer than limit by
// length. length must be additive: the length of a concatenation is the sum
// of the lengths of its pieces.
func SplitFunc(text string, limit int, length func(string) int) []string {
	budget := limit - length("\n"+fence) // room kept for closing a cut code block

	var (
		parts []string
		cur   strings.Builder
		n     int    // length of cur
		base  int    // length of cur that only reopens a code block
		open  string // fence line of the code block cur ends inside, if any
	)

	flush := func() {
		part := strings.TrimSuffix(cur.String(), "\n")
		if open != "" {
			part += "\n" + fence
		}

		parts = append(parts, part)

		cur.Reset()

		n, base = 0, 0

		if open != "" {
			cur.WriteString(open + "\n")
			n = length(open + "\n")
			base = n
		}
	}

	for line := range strings.SplitAfterSeq(text, "\n") {
		fenceLine := strings.TrimSpace(line) // taken before a long line is cut

		for n+length(line) > budget {
			if n > base {
				flush()

				continue
			}

			// A single line longer than a message: cut it, by at least a
			// rune, so a limit below one rune's length still ends.
			head, tail := cut(line, budget-n, length)
			if head == "" {
				_, size := utf8.DecodeRuneInString(line)
				head, tail = line[:size], line[size:]
			}

			cur.WriteString(head)
			n += length(head)
			line = tail

			flush()
		}

		cur.WriteString(line)
		n += length(line)

		if strings.HasPrefix(fenceLine, fence) {
			switch {
			case open != "":
				open = ""
			case length(fenceLine) > maxFenceLen:
				open = fence // reopen without the info string rather than fill the message
			default:
				open = fenceLine
			}
		}
	}

	if n > base || len(parts) == 0 {
		open = "" // a block left open by the author stays as written

		flush()
	}

	return parts
}

// SendParts calls send with each part in turn, stopping at the first that
// fails. When there are several, the error tells how many went out before.
func SendParts(parts []string, send func(i int, part string) error) error {
	for i, part := range parts {
		if err := send(i, part); err != nil {
			if len(parts) == 1 {
				return err
			}

			return fmt.Errorf("sent %d of %d parts: %w", i, len(parts), err)
		}
	}

	return nil
}

// Truncate cuts s to at most n characters (n > 0), ending in "…" when it
// had to cut. The "…" counts towards n, so the result fits a platform's hard
// limit; term.Clip, for display, keeps n runes and adds the "…" after them.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	head, _ := cut(s, n-1, utf8.RuneCountInString)

	return head + "…"
}

// cut splits s after the most leading runes whose length fits in n.
func cut(s string, n int, length func(string) int) (head, tail string) {
	for i, r := range s {
		if n -= length(string(r)); n < 0 {
			return s[:i], s[i:]
		}
	}

	return s, ""
}
