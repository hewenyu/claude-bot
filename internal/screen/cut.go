package screen

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Cut levels, finest first: Screen uses the finest one that keeps a
// conversation within maxPieces.
type level int

const (
	byClause   level = iota // sentences and comma-separated clauses
	bySentence              // sentences
	byMessage               // whole messages
)

// span is a byte range of a message's text.
type span struct{ start, end int }

// hardStops end a sentence; softStops end a clause. People often run
// several requests together with commas ("…可读性 ,然后更新readme").
const (
	hardStops = "。！？!?；;\n"
	softStops = "，,"
)

// minClause is the letters a clause needs before a comma ends it: shorter
// ones ("还有，", "对了，", a lone list item) say too little to be grouped
// on their own, so they join the clause that follows.
const minClause = 5

// cut splits text into spans at stops, keeping fenced code blocks whole.
// Every span has a letter in it; stretches without one (list markers,
// punctuation, blank lines) stay attached to the span that follows.
func cut(text string, lv level) []span {
	if lv == byMessage {
		if hasLetter(text) {
			return []span{{0, len(text)}}
		}

		return nil
	}

	c := cutter{text: text, clauses: lv == byClause}

	for lineStart := 0; lineStart < len(text); {
		lineEnd := len(text)
		if i := strings.IndexByte(text[lineStart:], '\n'); i >= 0 {
			lineEnd = lineStart + i + 1
		}

		c.line(lineStart, lineEnd)
		lineStart = lineEnd
	}

	return c.finish()
}

// cutter is the state of cut.
type cutter struct {
	text    string
	clauses bool // cut at commas too
	out     []span
	start   int // of the span being built
	inFence bool
}

// line cuts text[from:to], one line.
func (c *cutter) line(from, to int) {
	if strings.HasPrefix(strings.TrimSpace(c.text[from:to]), "```") {
		if c.inFence {
			c.flush(to)
		} else {
			c.flush(from)
		}

		c.inFence = !c.inFence

		return
	}

	if c.inFence {
		return
	}

	for i, r := range c.text[from:to] {
		at := from + i

		switch {
		case strings.ContainsRune(hardStops, r) || r == '.' && endsSentence(c.text[at+1:]):
			c.flush(at + utf8.RuneLen(r))
		case c.clauses && strings.ContainsRune(softStops, r) && letters(c.text[c.start:at]) >= minClause:
			c.flush(at + utf8.RuneLen(r))
		}
	}
}

// flush ends the span being built at end, if it has a letter yet.
func (c *cutter) flush(end int) {
	if end > c.start && hasLetter(c.text[c.start:end]) {
		c.out = append(c.out, span{c.start, end})
		c.start = end
	}
}

func (c *cutter) finish() []span {
	if c.start < len(c.text) {
		if hasLetter(c.text[c.start:]) {
			c.out = append(c.out, span{c.start, len(c.text)})
		} else if len(c.out) > 0 {
			c.out[len(c.out)-1].end = len(c.text) // trailing punctuation
		}
	}

	return c.out
}

// endsSentence reports whether a '.' followed by rest ends a sentence, not
// a number, file name or URL.
func endsSentence(rest string) bool {
	if rest == "" {
		return true
	}

	r, _ := utf8.DecodeRuneInString(rest)

	return unicode.IsSpace(r)
}

func letters(s string) int {
	n := 0

	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}

	return n
}

func hasLetter(s string) bool {
	return strings.ContainsFunc(s, unicode.IsLetter)
}
