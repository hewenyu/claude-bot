package transport

import (
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket keep-alive.
const (
	PingEvery    = 30 * time.Second
	ReadTimeout  = 3 * PingEvery    // no frame, not even a pong, for this long: the connection is dead
	WriteTimeout = 10 * time.Second // bound on writing one frame
	MaxFrame     = 4 << 20          // largest frame read
)

// KeepAlive bounds what conn reads, pings the server every PingEvery until
// done is closed, and answers its pings. A read fails once nothing, not even
// a pong, arrived for ReadTimeout; call the returned touch after every
// message read to push that deadline back.
func KeepAlive(conn *websocket.Conn, done <-chan struct{}) (touch func()) {
	touch = func() { _ = conn.SetReadDeadline(time.Now().Add(ReadTimeout)) }

	conn.SetReadLimit(MaxFrame)
	touch()
	conn.SetPongHandler(func(string) error {
		touch()

		return nil
	})
	conn.SetPingHandler(func(data string) error {
		touch()

		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(WriteTimeout))
	})

	go func() {
		t := time.NewTicker(PingEvery)
		defer t.Stop()

		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(WriteTimeout))
			}
		}
	}()

	return touch
}
