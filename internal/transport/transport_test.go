package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestProxy(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	if p, err := Proxy(ProxyDirect); err != nil || p != nil {
		t.Errorf("Proxy(direct) set = %v, %v; want no proxy", p != nil, err)
	}

	if p, err := Proxy(ProxyEnv); err != nil || p == nil {
		t.Errorf("Proxy(env) set = %v, %v; want the environment's", p != nil, err)
	}

	p, err := Proxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}

	if u, err := p(req); err != nil || u.Host != "127.0.0.1:7890" {
		t.Errorf("Proxy(url) picks %v, %v", u, err)
	}

	for _, bad := range []string{"", "not a url", "127.0.0.1:7890"} {
		if _, err := Proxy(bad); err == nil {
			t.Errorf("Proxy(%q) accepted", bad)
		}
	}
}

func TestClient_boundsEveryRequest(t *testing.T) {
	t.Parallel()

	if c := Client(nil); c.Timeout != ClientTimeout {
		t.Errorf("Client().Timeout = %v, want %v", c.Timeout, ClientTimeout)
	}
}

func get(ctx context.Context, t *testing.T, c *http.Client, url string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.Body.Close()
}

// conns counts the connections a test server opened and saw closed.
type conns struct {
	mu           sync.Mutex
	opened, shut int
}

func (c *conns) track(_ net.Conn, s http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch s { //nolint:exhaustive // only these matter
	case http.StateNew:
		c.opened++
	case http.StateClosed:
		c.shut++
	}
}

// waitFor polls until the server saw opened connections and at least shut
// closed ones.
func (c *conns) waitFor(t *testing.T, opened, shut int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		c.mu.Lock()
		o, s := c.opened, c.shut
		c.mu.Unlock()

		if o == opened && s >= shut {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("server saw %d connections opened and %d closed, want %d and %d", o, s, opened, shut)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func newServer(t *testing.T) (*httptest.Server, *conns) {
	t.Helper()

	c := &conns{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ConnState = c.track
	srv.Start()
	t.Cleanup(srv.Close)

	return srv, c
}

// closedPort is a URL nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	_ = l.Close()

	return "http://" + l.Addr().String()
}

func TestClient_failureClosesIdleConnections(t *testing.T) {
	t.Parallel()

	srv, seen := newServer(t)
	c := Client(nil)

	if err := get(t.Context(), t, c, srv.URL); err != nil {
		t.Fatal(err)
	}

	if err := get(t.Context(), t, c, closedPort(t)); err == nil {
		t.Fatal("request to a closed port succeeded")
	}

	if err := get(t.Context(), t, c, srv.URL); err != nil {
		t.Fatal(err)
	}

	// The connection pooled before the failure is closed; the retry opens a new one.
	seen.waitFor(t, 2, 1)
}

func TestClient_cancelledRequestKeepsConnections(t *testing.T) {
	t.Parallel()

	srv, seen := newServer(t)
	c := Client(nil)

	if err := get(t.Context(), t, c, srv.URL); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := get(ctx, t, c, closedPort(t)); err == nil {
		t.Fatal("cancelled request succeeded")
	}

	if err := get(t.Context(), t, c, srv.URL); err != nil {
		t.Fatal(err)
	}

	seen.waitFor(t, 1, 0)
}
