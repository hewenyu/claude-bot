package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/hewenyu/claude-bot/internal/term"
)

// Errors.
var (
	ErrBusy    = errors.New("agent is not idle")
	ErrNotLive = errors.New("agent is not running")
	ErrLive    = errors.New("agent is still running")
)

const (
	// pasteGap is the pause between pasting text and pressing Enter, so the
	// program sees a paste followed by a submit, not one long paste.
	pasteGap = 150 * time.Millisecond
	// settleSlack is how long after settleDelay the fallbacks are applied,
	// so that settleDelay has surely passed by then.
	settleSlack = 50 * time.Millisecond
	// killGrace is how long a program gets to end after each signal.
	killGrace = 3 * time.Second
	// drainWait is how long an ended program's last output gets to be
	// read once what it left is ended.
	drainWait = time.Second
	// defaultRows and defaultCols size a terminal no viewer has sized yet.
	defaultRows, defaultCols = 40, 120
	// maxSize bounds the rows and columns a viewer may ask for.
	maxSize = 1000
	// readSize is how much terminal output is read at once.
	readSize = 32 << 10
	// viewerBuffer is how many reads a viewer may fall behind before it is
	// dropped and has to replay.
	viewerBuffer = 512
)

// Bracketed paste markers. Text pasted into the terminal must not carry
// them: an end marker inside it would turn the rest into typed keys.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// Info is what the outside sees of an agent. It is also what the gateway
// saves, to show and to end what it left after a crash.
type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Cwd     string `json:"cwd"`
	Project int64  `json:"project,omitempty"`
	Activity
	// Procs is its program and what that started, while it runs: what
	// to end after a crash of the owner.
	Procs     []Proc    `json:"procs,omitempty"`
	PID       int       `json:"pid,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Activity is the part of an agent that hooks, titles and input change:
// its session and what it is doing.
type Activity struct {
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript,omitempty"`
	Status     Status `json:"status"`
	Detail     string `json:"detail,omitempty"`
	Title      string `json:"title,omitempty"`
	Turns      int    `json:"turns"`
	// Ended is how the latest turn ended.
	Ended       End    `json:"ended,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
	// Subagents is how many subagents it started that have not ended: a
	// turn that ends while one runs in the background goes on when it
	// reports.
	Subagents int `json:"subagents,omitempty"`
}

// Agent is one coding agent in a terminal. The terminal stays with the
// gateway: viewers attach and detach without stopping it.
type Agent struct {
	mu       sync.Mutex
	info     Info
	st       state
	cmd      *exec.Cmd
	pty      *os.File
	ring     *term.Ring
	titles   term.TitleParser
	viewers  map[chan []byte]struct{}
	onChange func(id string)
	done     chan struct{}
}

func newAgent(info Info, ringSize int, onChange func(id string)) *Agent {
	return &Agent{
		info:     info,
		st:       state{Activity: info.Activity},
		ring:     term.NewRing(ringSize),
		viewers:  map[chan []byte]struct{}{},
		onChange: onChange,
	}
}

// start runs cmd in a new terminal and follows it until it exits.
func (a *Agent) start(cmd *exec.Cmd) error {
	a.mu.Lock()

	if a.st.Status.Live() && a.cmd != nil {
		a.mu.Unlock()

		return ErrLive
	}

	rows, cols := a.info.Rows, a.info.Cols
	if rows <= 0 || cols <= 0 || rows > maxSize || cols > maxSize {
		rows, cols = defaultRows, defaultCols
	}

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		a.mu.Unlock()

		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}

	a.cmd, a.pty = cmd, f
	a.done = make(chan struct{})
	a.st = state{Activity: a.st.Activity}
	a.st.Status, a.st.Detail, a.st.Subagents = Starting, "", 0
	a.info.PID = cmd.Process.Pid
	a.info.ExitCode = nil
	a.info.Rows, a.info.Cols = rows, cols
	a.sync()
	a.mu.Unlock()

	go a.pump(newFamily(a.info.ID, cmd.Process.Pid), cmd, f)

	return nil
}

// pump copies the terminal's output to the ring and the viewers, and ends
// the agent when its program ends. The terminal does not tell: it stays
// open while anything the program started holds it, e.g. a shell left in
// the background. So the program's exit does, and what it left is ended
// with it, which closes the terminal.
func (a *Agent) pump(fam *family, cmd *exec.Cmd, f *os.File) {
	read := make(chan struct{})

	go func() {
		defer close(read)

		buf := make([]byte, readSize)

		for {
			n, err := f.Read(buf)
			if n > 0 {
				a.output(f, buf[:n])
			}

			if err != nil {
				return
			}
		}
	}()

	exited, tracked := make(chan struct{}), make(chan struct{})

	go func() {
		defer close(tracked)

		a.track(fam, exited)
	}()

	err := cmd.Wait()

	close(exited)
	<-tracked

	if n := fam.reap(); n > 0 {
		a.mu.Lock()
		a.note(fmt.Sprintf("ended %d process(es) the agent left running", n))
		a.mu.Unlock()
	}

	// The rest of its output; a process that hid from reap holds the
	// terminal and its reader, but not the agent.
	select {
	case <-read:
	case <-time.After(drainWait):
	}

	code := 0
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}

	_ = f.Close()

	a.mu.Lock()
	a.info.ExitCode = &code
	a.info.PID = 0
	a.info.Procs = nil
	a.st.Status, a.st.Detail = Exited, ""
	a.pty = nil
	close(a.done)

	for ch := range a.viewers {
		close(ch)
		delete(a.viewers, ch)
	}

	a.sync()
	a.mu.Unlock()
}

// track follows the program's family until it exits.
func (a *Agent) track(fam *family, exited <-chan struct{}) {
	tick := time.NewTicker(familyPoll)
	defer tick.Stop()

	for {
		if fam.scan() {
			a.mu.Lock()
			a.info.Procs = fam.procs()
			a.sync()
			a.mu.Unlock()
		}

		select {
		case <-exited:
			return
		case <-tick.C:
		}
	}
}

// output takes what the program printed on its terminal f. A terminal the
// agent no longer has, e.g. one a process that hid from reap still holds
// after the program ended, says nothing about the agent.
func (a *Agent) output(f *os.File, p []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pty != f {
		return
	}

	// Under the lock, so a viewer attaching now gets p either in its
	// replay or on its channel, never both.
	_, _ = a.ring.Write(p)

	now := time.Now()
	before := a.st.shown()

	for _, t := range a.titles.Feed(p) {
		a.st.title(t, now)

		if !a.st.stillAt.IsZero() {
			a.settleLater()
		}
	}

	a.st.output(p)
	changed := a.st.shown() != before

	for ch := range a.viewers {
		select {
		case ch <- append([]byte(nil), p...):
		default:
			// A viewer this far behind reattaches and replays the ring.
			close(ch)
			delete(a.viewers, ch)
		}
	}

	if changed {
		a.sync()
	}
}

// sync copies the state into the info and tells the owner, who reads the
// latest info itself. Call it with the lock held.
func (a *Agent) sync() {
	a.info.Activity = a.st.Activity
	a.info.UpdatedAt = time.Now()

	if a.onChange != nil {
		go a.onChange(a.info.ID)
	}
}

func (a *Agent) settleLater() {
	time.AfterFunc(settleDelay+settleSlack, func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		if a.st.settle(time.Now()) {
			a.sync()
		}
	})
}

// Hook applies a hook event the agent's program reported.
func (a *Agent) Hook(h Hook) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.st.Status.Live() {
		return
	}

	if a.st.hook(h, time.Now()) {
		a.sync()
	}
}

// Info returns what the agent is doing now.
func (a *Agent) Info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.info
}

// Attach returns what the terminal printed so far and a channel of what it
// prints next. The channel closes when the program ends or the viewer falls
// behind; call detach when done.
func (a *Agent) Attach() (replay []byte, out <-chan []byte, detach func()) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Queries in the history were asked long ago; the viewer's terminal
	// must not answer them again into the program.
	replay = term.StripQueries(a.ring.Bytes())
	ch := make(chan []byte, viewerBuffer)

	if a.pty == nil {
		close(ch)

		return replay, ch, func() {}
	}

	a.viewers[ch] = struct{}{}

	return replay, ch, func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		if _, ok := a.viewers[ch]; ok {
			delete(a.viewers, ch)
			close(ch)
		}
	}
}

// Write types what a person typed into the terminal.
func (a *Agent) Write(p []byte) error {
	a.mu.Lock()
	f := a.pty
	// A viewer's terminal answering a query, or reporting focus, is not
	// a person answering the prompt.
	if a.st.Status == Blocked && !term.IsResponse(p) {
		a.st.input(time.Now())
		a.settleLater()
	}
	a.mu.Unlock()

	if f == nil {
		return ErrNotLive
	}

	_, err := f.Write(p)

	return err
}

// Resize sets the terminal size.
func (a *Agent) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 || rows > maxSize || cols > maxSize {
		return fmt.Errorf("bad size %dx%d", cols, rows)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.info.Rows, a.info.Cols = rows, cols

	if a.pty == nil {
		return nil
	}

	return pty.Setsize(a.pty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Send pastes text and submits it, as a person would. It refuses unless
// the agent is idle, or force is set: text typed into a running turn or a
// permission prompt would be read as an answer.
func (a *Agent) Send(text string, force bool) error {
	a.mu.Lock()
	f, st := a.pty, a.st.Status
	a.mu.Unlock()

	if f == nil {
		return ErrNotLive
	}

	if st != Idle && !force {
		return fmt.Errorf("%w: %s", ErrBusy, st)
	}

	if _, err := io.WriteString(f, pasteStart+pasteSafe(text)+pasteEnd); err != nil {
		return err
	}

	time.Sleep(pasteGap)

	_, err := io.WriteString(f, "\r")

	return err
}

// pasteSafe drops escape characters from text to paste: text from a chat or
// an issue must not end the paste early and go on as keys.
func pasteSafe(text string) string {
	return strings.ReplaceAll(text, "\x1b", "")
}

// Interrupt stops the current turn, not the program.
func (a *Agent) Interrupt() error {
	a.mu.Lock()
	f := a.pty
	a.mu.Unlock()

	if f == nil {
		return ErrNotLive
	}

	_, err := f.Write([]byte{0x1b})

	return err
}

// Kill ends the program: a hangup first, then a kill if it lingers. What
// it started ends with it (see pump).
func (a *Agent) Kill() error {
	a.mu.Lock()
	cmd, done := a.cmd, a.done
	live := a.pty != nil
	a.mu.Unlock()

	if !live || cmd == nil || cmd.Process == nil {
		return ErrNotLive
	}

	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGHUP)

	select {
	case <-done:
		return nil
	case <-time.After(killGrace):
	}

	_ = syscall.Kill(-pid, syscall.SIGKILL)

	select {
	case <-done:
		return nil
	case <-time.After(killGrace + reapGrace + drainWait):
		return fmt.Errorf("agent %s did not exit", a.Info().ID)
	}
}

// Done returns a channel that closes when the program ends; nil if it
// never started.
func (a *Agent) Done() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.done
}

// note writes a line of the gateway's own into the terminal history, shown
// dim, e.g. that the agent was resumed.
func (a *Agent) note(text string) {
	_, _ = a.ring.Write([]byte("\r\n\x1b[2m[claude-bot] " + text + "\x1b[0m\r\n"))
}
