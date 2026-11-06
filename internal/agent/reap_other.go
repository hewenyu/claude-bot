//go:build !darwin && !linux

package agent

// listProcs sees no process where it does not know how to: what an agent
// left there ends with its process group only.
func listProcs() []proc { return nil }

func procEnviron(int) []byte { return nil }
