// Package transport builds the HTTP and WebSocket clients the channel
// adapters share, with the same proxy rules and bounded timeouts everywhere,
// plus the bounded download and reconnect backoff they all need.
package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Proxy modes besides a proxy URL.
const (
	ProxyDirect = "direct" // no proxy
	ProxyEnv    = "env"    // HTTPS_PROXY, NO_PROXY etc.
)

// ProxyFunc selects the proxy for a request; nil means direct.
type ProxyFunc = func(*http.Request) (*url.URL, error)

// Timeouts for the phases of a connection that can hang.
const (
	dialTimeout      = 10 * time.Second
	keepAlive        = 30 * time.Second
	tlsTimeout       = 15 * time.Second
	headerTimeout    = 30 * time.Second
	idleTimeout      = 90 * time.Second
	maxIdleConns     = 10
	handshakeTimeout = 45 * time.Second // WebSocket upgrade, TLS included
)

// ClientTimeout bounds a whole request made with Client, reading the body
// included: an API call or an attachment download.
const ClientTimeout = 2 * time.Minute

// MaxResponse bounds an API answer read into memory.
const MaxResponse = 16 << 20

// Proxy parses a proxy mode: ProxyDirect, ProxyEnv or a proxy URL.
func Proxy(mode string) (ProxyFunc, error) {
	switch mode {
	case ProxyDirect:
		return nil, nil
	case ProxyEnv:
		return http.ProxyFromEnvironment, nil
	}

	u, err := url.Parse(mode)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid proxy %q: want %s, %s or a URL", mode, ProxyDirect, ProxyEnv)
	}

	return http.ProxyURL(u), nil
}

// ProxyOr is Proxy for a configured mode that may be empty, which means
// fallback.
func ProxyOr(mode, fallback string) (ProxyFunc, error) {
	if mode == "" {
		mode = fallback
	}

	return Proxy(mode)
}

// Client returns the client for a channel's API calls and downloads: every
// request is bounded by ClientTimeout, and a failed request closes the idle
// connections, so the retry starts on a new one. WebSocket and other long
// connections do not go through it.
func Client(proxy ProxyFunc) *http.Client {
	return &http.Client{Transport: closeOnError{HTTP(proxy)}, Timeout: ClientTimeout}
}

// HTTP returns a transport that bounds every phase of a request that can
// hang: dial, TLS and waiting for the response headers.
func HTTP(proxy ProxyFunc) *http.Transport {
	return &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: headerTimeout,
		IdleConnTimeout:       idleTimeout,
		MaxIdleConns:          maxIdleConns,
	}
}

// closeOnError closes the idle connections of its transport after a request
// fails: a connection pooled before the failure may be just as broken. A
// request the caller cancelled says nothing about the network.
type closeOnError struct{ *http.Transport }

// RoundTrip implements http.RoundTripper.
func (t closeOnError) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.CloseIdleConnections()
	}

	return resp, err
}

// WebSocket returns a dialer that goes through the same proxy.
func WebSocket(proxy ProxyFunc) *websocket.Dialer {
	return &websocket.Dialer{Proxy: proxy, HandshakeTimeout: handshakeTimeout}
}
