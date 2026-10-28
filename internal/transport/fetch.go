package transport

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"
)

// MaxDownload bounds an attachment download, well above what people attach
// to a bug report.
const MaxDownload = 512 << 20

// ErrTooLarge is returned for a download over MaxDownload.
var ErrTooLarge = fmt.Errorf("file larger than %d MiB", MaxDownload>>20)

// ReadAll reads r to the end, failing with ErrTooLarge rather than returning
// a file cut short at MaxDownload.
func ReadAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	if len(data) > MaxDownload {
		return nil, ErrTooLarge
	}

	return data, nil
}

// Download GETs a file with c, sending header (e.g. Authorization), and
// reads it with ReadAll.
func Download(ctx context.Context, c *http.Client, url string, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("GET file: %w", err)
	}

	maps.Copy(req.Header, header)

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET file: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET file: %s", resp.Status)
	}

	return ReadAll(resp.Body)
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
