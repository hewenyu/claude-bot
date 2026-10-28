package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
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

func TestDownload_refusesOversizedFiles(t *testing.T) {
	t.Parallel()

	c := &http.Client{Transport: sized(MaxDownload)}
	if data, err := Download(t.Context(), c, "https://cdn.example/a", nil); err != nil || len(data) != MaxDownload {
		t.Fatalf("Download at the limit = %d bytes, %v; want all of it", len(data), err)
	}

	c = &http.Client{Transport: sized(MaxDownload + 1)}
	if _, err := Download(t.Context(), c, "https://cdn.example/a", nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Download over the limit = %v; want ErrTooLarge, not a truncated file", err)
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
