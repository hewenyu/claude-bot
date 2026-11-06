package agent

import "sync"

// notifier calls f after notify, in the background and once for however
// many notifies come while it waits or runs: one goroutine at most, and
// every notify is followed by a call of f that begins after it. A terminal
// that redraws every half second must not start a goroutine each time.
type notifier struct {
	f func()

	mu      sync.Mutex
	running bool
	again   bool
}

func (n *notifier) notify() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.running {
		n.again = true

		return
	}

	n.running = true

	go n.loop()
}

func (n *notifier) loop() {
	for {
		n.f()

		n.mu.Lock()
		if !n.again {
			n.running = false
			n.mu.Unlock()

			return
		}

		n.again = false
		n.mu.Unlock()
	}
}
