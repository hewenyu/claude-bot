package term

// maxTitle bounds a title so that a program that never ends its sequence
// cannot grow the parser without limit.
const maxTitle = 1024

// TitleParser finds the window titles a program sets with OSC 0 or OSC 2
// (ESC ] 0 ; text BEL, or ending with ESC \). It keeps state between
// writes, so a sequence split across reads is still found.
type TitleParser struct {
	state int
	code  []byte
	text  []byte
}

const (
	stText = iota
	stEsc
	stOSCCode
	stOSCText
	stOSCEsc
)

// Feed reads p and returns every complete title it set, in order.
func (t *TitleParser) Feed(p []byte) []string {
	var titles []string

	for _, c := range p {
		if t.step(c) {
			titles = t.finish(titles)
		}
	}

	return titles
}

// step reads one byte and reports whether it ended a title sequence.
func (t *TitleParser) step(c byte) bool {
	switch t.state {
	case stText:
		if c == 0x1b {
			t.state = stEsc
		}
	case stEsc:
		t.state = stText

		if c == ']' {
			t.state = stOSCCode
			t.code = t.code[:0]
			t.text = t.text[:0]
		}
	case stOSCCode:
		switch {
		case c >= '0' && c <= '9' && len(t.code) < 8:
			t.code = append(t.code, c)
		case c == ';':
			t.state = stOSCText
		default:
			t.state = stText
		}
	case stOSCText:
		return t.textByte(c)
	case stOSCEsc:
		if c == '\\' {
			return true
		}

		t.state = stText
	}

	return false
}

// textByte reads a byte of a title and reports whether it ended it.
func (t *TitleParser) textByte(c byte) bool {
	switch c {
	case 0x07:
		return true
	case 0x1b:
		t.state = stOSCEsc
	default:
		if len(t.text) < maxTitle {
			t.text = append(t.text, c)
		}
	}

	return false
}

func (t *TitleParser) finish(titles []string) []string {
	t.state = stText

	if code := string(t.code); code == "0" || code == "2" {
		titles = append(titles, string(t.text))
	}

	return titles
}

// Clip is s cut to n runes, and an ellipsis if it was longer: what an
// agent did or said, shown in a line.
func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}

	return s
}
