package main

import (
	"path/filepath"
	"time"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
)

// A running listener, and the gateway, keep a heartbeat file: who runs,
// since when, and how it is doing. A heartbeat older than heartbeatStale is
// that of a process that is gone.
const (
	heartbeatEvery = 15 * time.Second
	heartbeatStale = 45 * time.Second // three missed beats
)

// beat is what every heartbeat says.
type beat struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	BeatAt    time.Time `json:"beat_at"`
	Version   string    `json:"version"`
}

func (b beat) alive() bool { return time.Since(b.BeatAt) < heartbeatStale }

// heartbeat is written to .claude-bot/serve.json while the listener runs.
type heartbeat struct {
	beat

	Channels []string `json:"channels"`
	// Stopped lists channels that failed while the others kept running;
	// Errors says why, by channel.
	Stopped []string          `json:"stopped,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// gatewayHeartbeat is written to the gateway home while the gateway runs.
type gatewayHeartbeat struct {
	beat

	UI       string       `json:"ui,omitempty"` // the page, without its token
	Projects []runOutcome `json:"projects"`     // by id
}

// runOutcome is how the gateway is doing with one project.
type runOutcome struct {
	ID    int64        `json:"id"`
	State projectState `json:"state"`
	Error string       `json:"error,omitempty"`
}

// writeBeat writes a heartbeat. It is rewritten all the time and means
// nothing after a crash, so it is not flushed to disk.
func writeBeat(path string, hb any) error {
	return atomicfile.WriteJSONNoSync(path, hb, privatePerm)
}

// readBeat reads the heartbeat at path into hb and reports whether it is
// alive. A heartbeat that cannot be read is not; hb then holds whatever
// could be read of it.
func readBeat(path string, hb interface{ alive() bool }) bool {
	return atomicfile.ReadJSON(path, hb) == nil && hb.alive()
}

// readHeartbeat reads the heartbeat of the listener of the bot directory dir.
func readHeartbeat(dir string) (heartbeat, bool) {
	var hb heartbeat

	alive := readBeat(filepath.Join(dir, heartbeatFile), &hb)

	return hb, alive
}
