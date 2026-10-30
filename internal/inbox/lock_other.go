//go:build !unix

package inbox

import "sync"

// lockMu stands in for flock where there is none: it only orders this
// process, which is enough for the commands claude-bot supports there.
var lockMu sync.Mutex

func lockPath(string) (func(), error) {
	lockMu.Lock()

	return lockMu.Unlock, nil
}
