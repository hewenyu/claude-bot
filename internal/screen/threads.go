package screen

import (
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/term"
)

// ReopenWindow is how long after it was handled a task may be continued:
// a new message may join the conversation's pending tasks and those
// updated in the last ReopenWindow.
const ReopenWindow = 24 * time.Hour

// What Jev reads of the threads a message may continue: at most maxThreads,
// and of each the first askRunes of what opened it, what it is about, and
// the last transcriptRunes of the rest. Bot replies run to 2000 runes, and
// the end of one alone does not say what the task is.
const (
	maxThreads      = 10
	askRunes        = 400
	transcriptRunes = 800
)

// Threads are the tasks a new message may continue, the latest first as
// tasks are, as Jev reads them.
func Threads(tasks []inbox.Task) []Thread {
	tasks = tasks[:min(len(tasks), maxThreads)]

	out := make([]Thread, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, ThreadOf(t))
	}

	return out
}

// ThreadOf is a task as Jev reads it: what opened it, then the end of the
// rest of its conversation, and apart what people asked in it.
func ThreadOf(t inbox.Task) Thread {
	var opening, rest, asked []string

	for _, e := range t.Entries {
		if e.Kind == inbox.EntryBot {
			rest = append(rest, "bot: "+e.Text)

			continue
		}

		who := "user"
		if e.Sender != nil && e.Sender.Name != "" {
			who = e.Sender.Name
		}

		// What people said before the first reply opened the task.
		if len(rest) == 0 {
			opening = append(opening, who+": "+e.Text)
		} else {
			rest = append(rest, who+": "+e.Text)
		}

		asked = append(asked, e.Text)
	}

	transcript := term.Clip(strings.Join(opening, "\n"), askRunes)
	if len(rest) > 0 {
		transcript += "\n" + term.ClipLeft(strings.Join(rest, "\n"), transcriptRunes)
	}

	return Thread{
		ID:         t.ID,
		Transcript: transcript,
		Asked:      term.ClipLeft(strings.Join(asked, "\n"), transcriptRunes),
	}
}
