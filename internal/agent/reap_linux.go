package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listProcs is every process in /proc that can be read.
func listProcs() []proc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	out := make([]proc, 0, len(entries))

	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}

		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue // ended meanwhile
		}

		if p, ok := parseStat(pid, b); ok {
			out = append(out, p)
		}
	}

	return out
}

// parseStat reads /proc/<pid>/stat: "pid (comm) state ppid pgrp session
// … starttime …", where comm may hold spaces and parentheses.
func parseStat(pid int, b []byte) (proc, bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return proc{}, false
	}

	// From state on: state is field 3, starttime field 22.
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return proc{}, false
	}

	num := func(s string) int64 {
		n, _ := strconv.ParseInt(s, 10, 64)

		return n
	}

	return proc{PID: pid, PPID: int(num(f[1])), PGID: int(num(f[2])), SID: int(num(f[3])), Start: num(f[19])}, true
}

// procEnviron is a process's environment: NUL-separated, empty for
// another user's.
func procEnviron(pid int) []byte {
	b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))

	return b
}
