package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// echoServer is a WebSocket server that sends frames on connect, then
// echoes back whatever it reads.
func echoServer(t *testing.T, frames ...string) string {
	t.Helper()

	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck // test server

		for _, f := range frames {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
				return
			}
		}

		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			_ = conn.WriteMessage(typ, data)
		}
	}))
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestSession(t *testing.T) {
	t.Parallel()

	url := echoServer(t, "one", "two")
	up := 0

	var got []string

	opened, err := Session(t.Context(), WebSocket(nil), url, func() { up++ }, func(conn *websocket.Conn, data []byte) (bool, error) {
		got = append(got, strings.TrimSpace(string(data)))

		switch got[len(got)-1] {
		case "two":
			return false, WriteJSON(conn, "echo")
		case `"echo"`:
			return true, nil // asked to end
		}

		return false, nil
	})
	if !opened || err != nil || up != 1 {
		t.Fatalf("Session() = %v, %v with up called %d times; want opened, nil, once", opened, err, up)
	}

	if want := []string{"one", "two", `"echo"`}; !slices.Equal(got, want) {
		t.Errorf("frames = %q, want %q", got, want)
	}
}

func TestSession_endsWithContext(t *testing.T) {
	t.Parallel()

	url := echoServer(t, "hello")
	ctx, cancel := context.WithCancel(t.Context())

	opened, err := Session(ctx, WebSocket(nil), url, nil, func(*websocket.Conn, []byte) (bool, error) {
		cancel() // the connection closes, so the next read fails

		return false, nil
	})
	if !opened || err != nil {
		t.Errorf("Session() after cancel = %v, %v; want opened and nil", opened, err)
	}
}

func TestSession_dialFailure(t *testing.T) {
	t.Parallel()

	url := "ws" + strings.TrimPrefix(closedPort(t), "http")

	opened, err := Session(t.Context(), WebSocket(nil), url, nil, func(*websocket.Conn, []byte) (bool, error) { return false, nil })
	if opened || err == nil {
		t.Errorf("Session() to a closed port = %v, %v; want not opened and an error", opened, err)
	}
}
