package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MaxDownload bounds an attachment download, well above what people attach
// to a bug report.
const MaxDownload = 512 << 20

// maxResponse bounds an API answer read into memory.
const maxResponse = 16 << 20

// ErrTooLarge is a download or an API answer over its bound: refused rather
// than cut short.
var ErrTooLarge = errors.New("too large")

// Response is an API answer, read whole.
type Response struct {
	Status int // e.g. 200
	Header http.Header
	Body   []byte
}

// Do sends a request with c and reads the whole answer, whatever its status.
// header is added to the request (e.g. Content-Type, Authorization); body
// may be nil. An answer over 16 MiB fails with ErrTooLarge. Errors carry no
// prefix: the caller knows what the call was.
func Do(ctx context.Context, c *http.Client, method, url string, header http.Header, body io.Reader) (Response, error) {
	return do(ctx, c, method, url, header, body, maxResponse)
}

func do(ctx context.Context, c *http.Client, method, url string, header http.Header, body io.Reader, limit int64) (Response, error) {
	var res Response

	err := send(ctx, c, method, url, header, body, func(resp *http.Response) error {
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return fmt.Errorf("read answer: %w", err)
		}

		if int64(len(data)) > limit {
			return fmt.Errorf("answer %w: over %d bytes", ErrTooLarge, limit)
		}

		res = Response{Status: resp.StatusCode, Header: resp.Header, Body: data}

		return nil
	})

	return res, err
}

// send makes a request and hands the response to read, closing its body
// after.
func send(ctx context.Context, c *http.Client, method, url string, header http.Header, body io.Reader,
	read func(*http.Response) error,
) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}

	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	return read(resp)
}

// Download GETs a file with c, sending header (e.g. Authorization), and
// hands its content to save as it arrives, so a large file is never held in
// memory. save is what bounds it (see channel.SaveStream).
func Download(ctx context.Context, c *http.Client, url string, header http.Header, save func(io.Reader) error) error {
	err := send(ctx, c, http.MethodGet, url, header, nil, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return errors.New(resp.Status)
		}

		return save(resp.Body)
	})
	if err != nil {
		return fmt.Errorf("GET file: %w", err)
	}

	return nil
}

// Backoff spaces out reconnect attempts: Start, then doubling up to Cap.
// The zero value is unusable; Reset it after a success.
type Backoff struct {
	Start, Cap time.Duration

	cur time.Duration
}

// Next returns the delay before the next attempt.
func (b *Backoff) Next() time.Duration {
	b.cur = min(max(2*b.cur, b.Start), b.Cap)

	return b.cur
}

// Reset starts the next failure streak from Start again.
func (b *Backoff) Reset() { b.cur = 0 }

// Sleep waits d; false means ctx ended first.
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
