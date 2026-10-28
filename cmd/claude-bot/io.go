package main

import (
	"encoding/json"
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

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	return atomicfile.Write(path, append(raw, '\n'), privatePerm)
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
