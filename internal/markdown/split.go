// Package markdown holds the Markdown helpers the channel adapters share.
package markdown

import (
	"strings"
	"unicode/utf8"
)

const (
	fence       = "```"
	maxFenceLen = 100 // longest fence line repeated when a code block is cut
)

// Split cuts Markdown into messages of at most limit characters, preferring
// line breaks. A code block cut in two is closed at the end of one message
// and reopened at the start of the next, so both still render as code.
func Split(text string, limit int) []string {
	budget := limit - len("\n"+fence) // room kept for closing a cut code block

	var (
		parts []string
		cur   strings.Builder
		n     int    // characters in cur
		base  int    // characters in cur that only reopen a code block
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
			n = utf8.RuneCountInString(open) + 1
			base = n
		}
	}

	for line := range strings.SplitAfterSeq(text, "\n") {
		fenceLine := strings.TrimSpace(line) // taken before a long line is cut

		for n+utf8.RuneCountInString(line) > budget {
			if n > base {
				flush()

				continue
			}

			// A single line longer than a message: cut it.
			head, tail := cutRunes(line, budget-n)
			cur.WriteString(head)
			n += utf8.RuneCountInString(head)
			line = tail

			flush()
		}

		cur.WriteString(line)
		n += utf8.RuneCountInString(line)

		if strings.HasPrefix(fenceLine, fence) {
			switch {
			case open != "":
				open = ""
			case utf8.RuneCountInString(fenceLine) > maxFenceLen:
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

// cutRunes splits s after its first n runes.
func cutRunes(s string, n int) (head, tail string) {
	for i := range s {
		if n == 0 {
			return s[:i], s[i:]
		}

		n--
	}

	return s, ""
}
