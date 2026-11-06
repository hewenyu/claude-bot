package term

// Clip is s cut to n runes, and an ellipsis if it was longer: what an
// agent did or said shown in a line, the start of a conversation, a title.
func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}

	return s
}

// ClipLeft is the last n runes of s, after an ellipsis if it was longer:
// the end of a conversation, the latest of it.
func ClipLeft(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}

	return s
}
