package main

import (
	"errors"
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestParseInterleaved(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	notPlanned := fs.Bool("not-planned", false, "")
	method := fs.String("method", "squash", "")

	pos, err := parseInterleaved(fs, []string{"github:o/r/1", "--not-planned", "-", "--method", "rebase", "x"}, 0)
	if err != nil || !*notPlanned || *method != "rebase" || !reflect.DeepEqual(pos, []string{"github:o/r/1", "-", "x"}) {
		t.Errorf("parseInterleaved = %q, %v; not-planned %v, method %q", pos, err, *notPlanned, *method)
	}

	if _, err := parseInterleaved(fs, []string{"id", "--bogus"}, 0); !errors.Is(err, errUsage) {
		t.Errorf("parseInterleaved with an unknown flag = %v, want a usage error", err)
	}
}

// A review's text is taken as written, flags and all.
func TestParseInterleaved_literalText(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	args := []string{"github:o/r/1", "comment", "--not-a-flag", "- item"}

	pos, err := parseInterleaved(fs, args, 2)
	if err != nil || !reflect.DeepEqual(pos, args) {
		t.Errorf("parseInterleaved = %q, %v", pos, err)
	}
}

// Each action takes its own flags and its number of arguments: a second id
// or another action's flag is a mistake, not something to ignore.
func TestGitHub_rejectsWhatTheActionDoesNotTake(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"github"},
		{"github", "reopen", "github:o/r/42"},
		{"github", "close", "github:o/r/42", "github:o/r/43"},
		{"github", "close", "github:o/r/42", "--method", "rebase"},
		{"github", "merge", "github:o/r/42", "--not-planned"},
		{"github", "merge"},
		{"github", "mine", "github:o/r/42", "x"},
		{"github", "review", "github:o/r/42"},
		{"github", "review", "--bogus", "github:o/r/42", "approve"},
	} {
		if code := run(t.Context(), args); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
}
