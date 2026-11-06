// Package term keeps what a terminal printed and reads the titles it set.
package term

import (
	"bytes"
	"sync"
)

// Ring keeps the last bytes a terminal printed, so that a viewer that
// attaches later can replay them and see the screen.
type Ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// NewRing returns a ring that keeps at most size bytes.
func NewRing(size int) *Ring {
	return &Ring{max: size}
}

// Write appends p, dropping the oldest output beyond the limit. It trims
// only once the ring is a quarter over, so a busy terminal does not copy the
// whole buffer on every read, and cuts at a line start where it can, so that
// a replay does not begin inside an escape sequence.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf = append(r.buf, p...)

	if len(r.buf) > r.max+r.max/4 {
		over := len(r.buf) - r.max

		cut := over
		if i := bytes.IndexByte(r.buf[over:], '\n'); i >= 0 && i < r.max/2 {
			cut = over + i + 1
		}

		r.buf = append(r.buf[:0:0], r.buf[cut:]...)
	}

	return len(p), nil
}

// Bytes returns a copy of what the ring holds.
func (r *Ring) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return bytes.Clone(r.buf)
}
