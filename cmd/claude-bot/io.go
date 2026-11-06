package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
)

// printer writes what a command tells the person running it, to w; nil
// writes nothing. A broken terminal is no reason to fail the command, so
// it ignores write errors.
type printer struct{ w io.Writer }

func (p printer) printf(format string, args ...any) {
	if p.w != nil {
		_, _ = fmt.Fprintf(p.w, format, args...)
	}
}

func (p printer) println(args ...any) {
	if p.w != nil {
		_, _ = fmt.Fprintln(p.w, args...)
	}
}

// stdout prints to standard output.
var stdout = printer{os.Stdout}

// stdoutJSON is an encoder of JSON for people and Claude to read: no HTML
// escapes in messages.
func stdoutJSON() *json.Encoder {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	return enc
}

func printJSON(v any) error {
	enc := stdoutJSON()
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// writeLines prints values as JSON lines.
func writeLines[T any](values []T) error {
	enc := stdoutJSON()

	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}

func writeJSON(path string, v any) error {
	return atomicfile.WriteJSON(path, v, privatePerm)
}

// tailFile returns the last n lines of a file, for error reports.
func tailFile(path string, n int) string {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed file in the bot dir
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
