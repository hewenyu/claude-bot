package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sized serves a body of n bytes.
type sized int

func (n sized) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(io.LimitReader(zeros{}, int64(n))),
		Request:    req,
	}, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

func TestDo(t *testing.T) {
	t.Parallel()

	c := &http.Client{Transport: sized(maxResponse)}

	res, err := Do(t.Context(), c, http.MethodGet, "https://api.example/a", nil, nil)
	if err != nil || res.Status != http.StatusOK || len(res.Body) != maxResponse {
		t.Fatalf("Do() at the limit = %d, %d bytes, %v; want all of it", res.Status, len(res.Body), err)
	}

	c = &http.Client{Transport: sized(maxResponse + 1)}
	if _, err := Do(t.Context(), c, http.MethodGet, "https://api.example/a", nil, nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Do() over the limit = %v; want ErrTooLarge, not a truncated answer", err)
	}
}

func TestDo_sendsHeaders(t *testing.T) {
	t.Parallel()

	headers := make(chan http.Header, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header

		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}))
	t.Cleanup(srv.Close)

	header := http.Header{"content-type": {JSON}, "x-acs-token": {"t"}}

	res, err := Do(t.Context(), srv.Client(), http.MethodPost, srv.URL, header, strings.NewReader("{}"))
	if err != nil || res.Status != http.StatusTeapot || string(res.Body) != "short and stout" {
		t.Fatalf("Do() = %d %q, %v", res.Status, res.Body, err)
	}

	if got := <-headers; got.Get("Content-Type") != JSON || got.Get("X-Acs-Token") != "t" {
		t.Errorf("request headers = %v", got)
	}
}

func TestDownload_streams(t *testing.T) {
	t.Parallel()

	c := &http.Client{Transport: sized(1 << 20)}

	var n int64

	err := Download(t.Context(), c, "https://cdn.example/a", nil, func(r io.Reader) error {
		var err error

		n, err = io.Copy(io.Discard, r)

		return err
	})
	if err != nil || n != 1<<20 {
		t.Errorf("Download() handed over %d bytes, %v; want 1 MiB", n, err)
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()

	b := Backoff{Start: time.Second, Cap: 5 * time.Second}

	var got []time.Duration
	for range 5 {
		got = append(got, b.Next())
	}

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Next() sequence = %v, want %v", got, want)
		}
	}

	b.Reset()

	if d := b.Next(); d != time.Second {
		t.Errorf("Next() after Reset = %v, want %v", d, time.Second)
	}
}

func TestSleep_endsWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if Sleep(ctx, time.Hour) {
		t.Error("Sleep() on a cancelled context waited it out")
	}
}
