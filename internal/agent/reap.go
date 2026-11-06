package agent

import (
	"bytes"
	"os"
	"slices"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// An agent's program starts more programs: shells, servers, subagents.
// Claude Code runs each shell in a session of its own, and a command left
// in the background there (`server &`, nohup) outlives the shell: launchd
// or init adopts it and it runs on, unseen, after the agent ended. So the
// agent keeps track of its family while it runs, and ends all of it with
// the program.
//
// A process is family if it is the program, a child of family, in the
// session or process group of family, or has the agent's EnvAgentID: that
// one even an adopted process carries, but macOS hides the environment of
// its own system programs (/bin, /usr/bin). The family is looked at every
// familyPoll; a system program that leaves its session sooner than that,
// on macOS, escapes.
const (
	// familyPoll is how often an agent's family is looked at.
	familyPoll = 500 * time.Millisecond
	// reapGrace is how long what an agent left gets to end after a
	// SIGTERM, before a SIGKILL.
	reapGrace = 2 * time.Second
	// reapPoll is how often reap looks whether they ended.
	reapPoll = 100 * time.Millisecond
)

// proc is a process as the family sees it.
type proc struct {
	PID, PPID, PGID, SID int
	// Start is when it started, in the system's own unit: a pid used
	// again has another.
	Start int64
}

// Proc names a process across restarts of the owner: its pid, and when
// it started, so that a pid used again since is not taken for it.
type Proc struct {
	PID   int   `json:"pid"`
	Start int64 `json:"start"`
}

// family is an agent's program and everything it started.
type family struct {
	id   string
	root int
	// list and environ read the system; tests give their own.
	list    func() []proc
	environ func(pid int) []byte

	rootStart int64
	members   map[int]int64 // pid → start
	sessions  map[int]bool
	groups    map[int]bool
	// checked are the processes whose environment was read: once each.
	checked map[Proc]bool
}

func newFamily(id string, root int) *family {
	return &family{
		id: id, root: root, list: listProcs, environ: procEnviron,
		members: map[int]int64{}, sessions: map[int]bool{}, groups: map[int]bool{}, checked: map[Proc]bool{},
	}
}

// scan looks at the system and takes in what joined the family, forgets
// what ended, and reports whether the family changed.
func (f *family) scan() bool {
	procs := f.list()
	alive := make(map[int]int64, len(procs))

	for _, p := range procs {
		alive[p.PID] = p.Start
		if p.PID == f.root {
			f.rootStart = p.Start
		}
	}

	ended := f.forget(alive)

	return f.grow(procs, alive) || ended
}

// forget drops what ended, and reports whether a member did.
func (f *family) forget(alive map[int]int64) bool {
	changed := false

	for pid, start := range f.members {
		if s, ok := alive[pid]; !ok || s != start {
			delete(f.members, pid)

			changed = true
		}
	}

	for k := range f.checked {
		if s, ok := alive[k.PID]; !ok || s != k.Start {
			delete(f.checked, k)
		}
	}

	return changed
}

// grow takes in what joined the family, and reports whether any did.
func (f *family) grow(procs []proc, alive map[int]int64) bool {
	changed := false
	self := os.Getpid()
	ownSession, _ := unix.Getsid(self)
	ownGroup := unix.Getpgrp()

	// Each one taken in may bring its children, session and group.
	for grew := true; grew; {
		grew = false

		for _, p := range procs {
			if p.PID <= 1 || p.PID == self || f.isMember(p) || !f.belongs(p, alive) {
				continue
			}

			f.members[p.PID] = p.Start

			if p.SID > 1 && p.SID != ownSession {
				f.sessions[p.SID] = true
			}

			if p.PGID > 1 && p.PGID != ownGroup {
				f.groups[p.PGID] = true
			}

			grew, changed = true, true
		}
	}

	return changed
}

func (f *family) isMember(p proc) bool {
	start, ok := f.members[p.PID]

	return ok && start == p.Start
}

// belongs reports whether p is family. A session or group counts while
// its leader is family or gone: once a stranger leads it, the number was
// used again.
func (f *family) belongs(p proc, alive map[int]int64) bool {
	led := func(leader int) bool {
		start, ok := alive[leader]

		return !ok || f.members[leader] == start
	}

	switch {
	case p.PID == f.root:
		return true
	case f.isMember(proc{PID: p.PPID, Start: alive[p.PPID]}):
		return true
	case f.sessions[p.SID] && led(p.SID), f.groups[p.PGID] && led(p.PGID):
		return true
	case p.Start < f.rootStart:
		// Older than the program: not its.
		return false
	}

	k := Proc{PID: p.PID, Start: p.Start}
	if f.checked[k] {
		return false
	}

	f.checked[k] = true

	return agentOf(f.environ(p.PID)) == f.id
}

// procs is the family as it is now.
func (f *family) procs() []Proc {
	out := make([]Proc, 0, len(f.members))
	for pid, start := range f.members {
		out = append(out, Proc{PID: pid, Start: start})
	}

	slices.SortFunc(out, func(a, b Proc) int { return a.PID - b.PID })

	return out
}

// reap ends the family, once more looked at, and returns how many of it
// were left. The program itself has ended: what is left, it left.
func (f *family) reap() int {
	f.scan()
	delete(f.members, f.root)

	return reapProcs(f.procs(), f.scanned)
}

func (f *family) scanned() []Proc {
	f.scan()
	delete(f.members, f.root)

	return f.procs()
}

// reapProcs ends procs: a SIGTERM, then a SIGKILL for what lingers, and
// returns how many there were. alive reports what is left of them.
func reapProcs(procs []Proc, alive func() []Proc) int {
	if len(procs) == 0 {
		return 0
	}

	self := os.Getpid()

	for _, p := range procs {
		if p.PID != self {
			_ = syscall.Kill(p.PID, syscall.SIGTERM)
		}
	}

	for deadline := time.Now().Add(reapGrace); time.Now().Before(deadline); {
		time.Sleep(reapPoll)

		if len(alive()) == 0 {
			return len(procs)
		}
	}

	for _, p := range alive() {
		if p.PID != self {
			_ = syscall.Kill(p.PID, syscall.SIGKILL)
		}
	}

	return len(procs)
}

// agentOf is the agent id in an environment block: NUL-separated
// KEY=value strings, possibly after other NUL-separated strings.
func agentOf(env []byte) string {
	key := []byte(EnvAgentID + "=")

	for kv := range bytes.SplitSeq(env, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, key); ok {
			return string(v)
		}
	}

	return ""
}

// ReapAll ends what agents of an earlier run of the owner left running,
// e.g. after it crashed: the families they had when last saved, and
// whatever carries one of their ids. It returns how many there were.
func ReapAll(infos []Info) int {
	ids := map[string]bool{}
	saved := map[Proc]bool{}

	for _, info := range infos {
		ids[info.ID] = true

		for _, p := range info.Procs {
			saved[p] = true
		}
	}

	if len(ids) == 0 {
		return 0
	}

	left := func() []Proc {
		var out []Proc

		self := os.Getpid()

		for _, p := range listProcs() {
			k := Proc{PID: p.PID, Start: p.Start}
			if p.PID > 1 && p.PID != self && (saved[k] || ids[agentOf(procEnviron(p.PID))]) {
				out = append(out, k)
			}
		}

		return out
	}

	return reapProcs(left(), left)
}
