package jev

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// server answers every Noul with 0.5 after failing the first fail requests
// with status; it counts requests.
func server(t *testing.T, fail int32, status int) (*Client, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)

		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s with auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}

		if n <= fail {
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"detail":"echoes the user's message"}`)

			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}

		answers := map[string]any{}

		for id, q := range req.Questions {
			if q.Type != "noul" || q.Criteria["true"] == "" {
				t.Errorf("question %s = %+v", id, q)
			}

			answers[id] = map[string]any{"type": "noul", "noul": 0.5}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.14.0", "answers": answers})
	}))
	t.Cleanup(srv.Close)

	return &Client{URL: srv.URL, Key: "k", Model: DefaultModel, HTTP: srv.Client()}, &calls
}

func questions(n int) map[string]Noul {
	qs := make(map[string]Noul, n)
	for i := range n {
		qs[fmt.Sprintf("q%d", i)] = Noul{Instructions: "?", True: "yes", False: "no"}
	}

	return qs
}

func TestNouls_batches(t *testing.T) {
	t.Parallel()

	c, calls := server(t, 0, 0)

	got, err := c.Nouls(t.Context(), "state", questions(batchSize+1))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != batchSize+1 || calls.Load() != 2 {
		t.Errorf("got %d answers in %d requests; want %d in 2", len(got), calls.Load(), batchSize+1)
	}
}

func TestNouls_retriesOverload(t *testing.T) {
	t.Parallel()

	c, calls := server(t, 1, statusOverloaded)

	if _, err := c.Nouls(t.Context(), "state", questions(1)); err != nil || calls.Load() != 2 {
		t.Errorf("Nouls = %v after %d requests; want success on the retry", err, calls.Load())
	}
}

func TestNouls_errorsLeaveOutTheBody(t *testing.T) {
	t.Parallel()

	c, calls := server(t, 1, http.StatusUnprocessableEntity)

	_, err := c.Nouls(t.Context(), "state", questions(1))
	if err == nil || calls.Load() != 1 {
		t.Fatalf("Nouls = %v after %d requests; want one failed request", err, calls.Load())
	}

	if strings.Contains(err.Error(), "user's message") {
		t.Errorf("error %q quotes the response body", err)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		_, _ = fmt.Fprint(w, `{"models":[{"name":"jev-latest"}]}`)
	}))
	t.Cleanup(srv.Close)

	for key, ok := range map[string]bool{"good": true, "bad": false} {
		c := New(key)
		c.URL = srv.URL

		if err := c.Check(t.Context()); (err == nil) != ok {
			t.Errorf("Check with key %q = %v", key, err)
		}
	}
}
