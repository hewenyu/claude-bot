package agent

import (
	"os"

	"golang.org/x/sys/unix"
)

// listProcs is every process of this user. One that cannot be read is
// left out.
func listProcs() []proc {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil
	}

	out := make([]proc, 0, len(kps))

	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)

		sid, err := unix.Getsid(pid)
		if err != nil {
			continue // ended meanwhile
		}

		start := kp.Proc.P_starttime
		out = append(out, proc{
			PID: pid, PPID: int(kp.Eproc.Ppid), PGID: int(kp.Eproc.Pgid), SID: sid,
			Start: start.Sec*1_000_000 + int64(start.Usec),
		})
	}

	return out
}

// procEnviron is a process's arguments and environment, as
// kern.procargs2 gives them: NUL-separated strings. macOS leaves out the
// environment of its own system programs.
func procEnviron(pid int) []byte {
	b, _ := unix.SysctlRaw("kern.procargs2", pid)

	return b
}
