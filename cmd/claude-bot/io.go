package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
)

// textArg returns the message text from args, or from stdin when args is
// empty or "-" (handy for multi-line text in a heredoc).
func textArg(args []string) (string, error) {
	fromStdin := len(args) == 0 || len(args) == 1 && args[0] == "-"
	if !fromStdin {
		return strings.Join(args, " "), nil
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}

	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", fmt.Errorf("%w: empty message text", errUsage)
	}

	return text, nil
}

// parseFlags parses args into fs; a bad flag is a usage error.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	return nil
}

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
