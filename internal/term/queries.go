package term

import "regexp"

// queries are what programs ask the terminal: its kind and version, the
// cursor position, modes, colors, the window size. A query in the history
// was asked long ago; replayed to a new viewer, its terminal would answer
// again and the answer would reach the program as typed keys.
var queries = regexp.MustCompile(
	`\x1b\[[>=]?[0-9;]*c` + // device attributes
		`|\x1b\[>[0-9;]*q` + // version
		`|\x1b\[\??[0-9;]*n` + // status, cursor position
		`|\x1b\[\??[0-9;]*\$p` + // mode
		`|\x1b\[\?u` + // keyboard protocol
		`|\x1b\[(?:1[134689]|2[01])t` + // window size and title
		`|\x1b\](?:1[0-9]|4;[0-9]+);\?(?:\x07|\x1b\\)` + // colors
		`|\x1bP[$+]q[^\x1b]*\x1b\\`) // settings, capabilities

// StripQueries returns p without terminal queries.
func StripQueries(p []byte) []byte {
	return queries.ReplaceAll(p, nil)
}

// responses are a terminal's answers to queries, and the focus reports it
// sends by itself: never keys a person typed.
var responses = regexp.MustCompile(`^(?:` +
	`\x1b\[[?>][0-9;]*c` + // device attributes
	`|\x1b\[[0-9;]*R` + // cursor position
	`|\x1bP[>!|][^\x1b]*\x1b\\` + // version, unit id
	`|\x1bP[01][$+]r[^\x1b]*\x1b\\` + // settings, capabilities
	`|\x1b\][0-9;]+;rgb:[^\x07\x1b]*(?:\x07|\x1b\\)` + // colors
	`|\x1b\[\??[0-9;]*\$y` + // mode
	`|\x1b\[\?[0-9;]*u` + // keyboard protocol
	`|\x1b\[[0-9;]+t` + // window size
	`|\x1b\[[IO]` + // focus in, out
	`)+$`)

// IsResponse reports whether p is only the terminal answering, not keys.
func IsResponse(p []byte) bool {
	return responses.Match(p)
}
