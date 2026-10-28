package telegram

import (
	"html"
	"regexp"
	"strings"
)

// Markdown the renderer understands beyond plain text.
var (
	heading = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	link    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	bold    = regexp.MustCompile(`\*\*(\S(?:.*?\S)?)\*\*`)
	strike  = regexp.MustCompile(`~~(\S(?:.*?\S)?)~~`)
)

const (
	codeFence = "```"
	backtick  = "`"
	quote     = ">"
)

// toHTML renders the Markdown Claude writes as Telegram HTML: code blocks,
// inline code, bold, strikethrough, links, headings (as bold) and quotes.
// Everything else stays literal text, escaped, so the result always parses.
// Single * and _ are left alone: they are too often part of names and lists.
func toHTML(md string) string {
	var (
		out    []string
		code   []string // lines of the code block being read
		lang   string
		inCode bool
		quotes []string // consecutive quoted lines
	)

	flushQuote := func() {
		if len(quotes) > 0 {
			out = append(out, "<blockquote>"+strings.Join(quotes, "\n")+"</blockquote>")
			quotes = nil
		}
	}

	flushCode := func() {
		body := html.EscapeString(strings.Join(code, "\n"))
		if lang != "" {
			out = append(out, `<pre><code class="language-`+html.EscapeString(lang)+`">`+body+"</code></pre>")
		} else {
			out = append(out, "<pre>"+body+"</pre>")
		}

		code, inCode = nil, false
	}

	for line := range strings.SplitSeq(md, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case inCode && strings.HasPrefix(trimmed, codeFence):
			flushCode()
		case inCode:
			code = append(code, line)
		case strings.HasPrefix(trimmed, codeFence):
			flushQuote()

			inCode, lang = true, strings.TrimSpace(strings.TrimPrefix(trimmed, codeFence))
		case strings.HasPrefix(trimmed, quote):
			quotes = append(quotes, inline(strings.TrimSpace(strings.TrimPrefix(trimmed, quote))))
		default:
			flushQuote()

			if m := heading.FindStringSubmatch(trimmed); m != nil {
				out = append(out, "<b>"+inline(m[1])+"</b>")
			} else {
				out = append(out, inline(line))
			}
		}
	}

	flushQuote()

	if inCode {
		flushCode() // a block left open still renders as code
	}

	return strings.Join(out, "\n")
}

// inline renders one line: `code` spans verbatim, the rest escaped with
// links, bold and strikethrough applied.
func inline(s string) string {
	parts := strings.Split(s, backtick)

	var b strings.Builder

	for i, p := range parts {
		switch {
		case i%2 == 1 && i < len(parts)-1:
			b.WriteString("<code>" + html.EscapeString(p) + "</code>")
		case i%2 == 1: // an unmatched backtick stays literal
			b.WriteString(backtick + styled(p))
		default:
			b.WriteString(styled(p))
		}
	}

	return b.String()
}

func styled(s string) string {
	s = html.EscapeString(s)
	s = link.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = bold.ReplaceAllString(s, "<b>$1</b>")

	return strike.ReplaceAllString(s, "<s>$1</s>")
}
