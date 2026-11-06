package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket keep-alive.
const (
	pingEvery    = 30 * time.Second
	readTimeout  = 3 * pingEvery    // no frame, not even a pong, for this long: the connection is dead
	writeTimeout = 10 * time.Second // bound on writing one frame
	maxFrame     = 4 << 20          // largest frame read
)

// KeepAlive bounds what conn reads, pings the server every 30 s until done
// is closed, and answers its pings. A read fails once nothing, not even a
// pong, arrived for 90 s; call the returned touch after every message read
// to push that deadline back.
func KeepAlive(conn *websocket.Conn, done <-chan struct{}) (touch func()) {
	touch = func() { _ = conn.SetReadDeadline(time.Now().Add(readTimeout)) }

	conn.SetReadLimit(maxFrame)
	touch()
	conn.SetPongHandler(func(string) error {
		touch()

		return nil
	})
	conn.SetPingHandler(func(data string) error {
		touch()

		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeTimeout))
	})

	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()

		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout))
			}
		}
	}()

	return touch
}

// WriteJSON sends v as a JSON text frame, bounded by the write timeout.
// Only one goroutine may write at a time (the keep-alive pings excepted).
func WriteJSON(conn *websocket.Conn, v any) error {
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))

	if err := conn.WriteJSON(v); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}

	return nil
}

// Session runs one WebSocket connection: it dials url with d, calls up (if
// not nil) once the connection is open, then handle with every message read,
// keeping the connection alive (KeepAlive) meanwhile. It ends when handle
// asks to (end, or an error), the connection drops, or ctx ends, which
// returns a nil error. opened tells whether the dial got through, e.g. for
// Reconnect.
func Session(ctx context.Context, d *websocket.Dialer, url string, up func(),
	handle func(conn *websocket.Conn, data []byte) (end bool, err error),
) (opened bool, err error) {
	conn, resp, err := d.DialContext(ctx, url, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}

	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}

	defer conn.Close() //nolint:errcheck // closing a finished connection

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	done := make(chan struct{})
	defer close(done)

	touch := KeepAlive(conn, done)

	if up != nil {
		up()
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}

			return true, fmt.Errorf("read: %w", err)
		}

		touch()

		if end, err := handle(conn, data); end || err != nil {
			return true, err
		}
	}
}
