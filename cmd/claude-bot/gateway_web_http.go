package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"

	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The HTTP plumbing of the page's API: who may call it, and how handlers
// answer.

const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// loopbackHosts returns the Host headers that reach addr, which must be a
// loopback address: the page holds credentials and is for this machine.
func loopbackHosts(addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("page address %q: %w", addr, err)
	}

	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("page address %q is not a loopback address; the page is for this machine only", addr)
	}

	hosts := []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port}
	if own := net.JoinHostPort(host, port); !slices.Contains(hosts, own) {
		hosts = append(hosts, own) // e.g. 127.0.0.2
	}

	return hosts, nil
}

// guard turns away requests not addressed to this machine, and sets the
// headers every response needs.
func (w *web) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !slices.Contains(w.hosts, r.Host) {
			http.Error(rw, "forbidden host", http.StatusMisdirectedRequest)

			return
		}

		h := rw.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		next.ServeHTTP(rw, r)
	})
}

// authorize wants the page token as a bearer token. A header, unlike a
// cookie, is never sent by another site's page.
func (w *web) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")

		got := r.Header.Get(headerAuthorization)
		if got == "" {
			got = wsToken(r)
		}

		if !bearerIs(got, w.token) {
			apiError(rw, http.StatusUnauthorized, "missing or wrong token: open the page with `claude-bot gateway open`")

			return
		}

		r.Body = http.MaxBytesReader(rw, r.Body, maxRequestBody)

		next.ServeHTTP(rw, r)
	})
}

// bearerIs reports, in constant time, whether an Authorization value is
// token as a bearer token.
func bearerIs(header, token string) bool {
	return subtle.ConstantTimeCompare([]byte(header), []byte(bearerPrefix+token)) == 1
}

// api adapts a handler that returns its answer: v as JSON with a 200, no
// content if v is nil, or the error as fail answers it.
func (w *web) api(f func(*http.Request) (any, error)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		v, err := f(r)

		switch {
		case err != nil:
			w.fail(rw, err)
		case v == nil:
			rw.WriteHeader(http.StatusNoContent)
		default:
			writeAPI(rw, http.StatusOK, v)
		}
	}
}

func writeAPI(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)

	enc := json.NewEncoder(rw)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func apiError(rw http.ResponseWriter, status int, msg string) {
	writeAPI(rw, status, map[string]string{"error": msg})
}

// httpError is an error the page is told as it is, with its status.
type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return e.err.Error() }
func (e *httpError) Unwrap() error { return e.err }

func badRequest(err error) error   { return &httpError{status: http.StatusBadRequest, err: err} }
func unauthorized(err error) error { return &httpError{status: http.StatusUnauthorized, err: err} }
func notFound(err error) error     { return &httpError{status: http.StatusNotFound, err: err} }
func conflict(err error) error     { return &httpError{status: http.StatusConflict, err: err} }

func badRequestf(format string, args ...any) error { return badRequest(fmt.Errorf(format, args...)) }

// formError is a channel form the gateway cannot use: a bad request, which
// testing the form reports as a failed test.
type formError struct{ err error }

func (e *formError) Error() string { return e.err.Error() }
func (e *formError) Unwrap() error { return e.err }

func badForm(err error) error { return &formError{err: err} }

func badFormf(format string, args ...any) error { return badForm(fmt.Errorf(format, args...)) }

// fail answers with err: with its status if it has one, as a 400, 404, 409
// or 503 if it is one, else as a 500.
func (w *web) fail(rw http.ResponseWriter, err error) {
	var (
		he    *httpError
		form  *formError
		taken *store.ConflictError
	)

	switch {
	case errors.As(err, &he):
		apiError(rw, he.status, he.Error())
	case errors.As(err, &form):
		apiError(rw, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, inbox.ErrNoTask):
		apiError(rw, http.StatusNotFound, err.Error())
	case errors.As(err, &taken):
		apiError(rw, http.StatusConflict, err.Error())
	case errors.Is(err, errNoRunner):
		apiError(rw, http.StatusServiceUnavailable, err.Error())
	default:
		w.log.Error("page request", "err", err)
		apiError(rw, http.StatusInternalServerError, err.Error())
	}
}

// decodeBody reads the request's JSON body into v; a body it cannot read
// is a bad request.
func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest(err)
	}

	return nil
}
