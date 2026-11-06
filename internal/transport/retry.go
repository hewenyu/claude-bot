package transport

import (
	"context"
	"log/slog"
	"time"
)

// Reconnect backoff of the long-lived listeners.
const (
	ReconnectStart = 2 * time.Second // first wait after a failed connection
	ReconnectCap   = time.Minute
)

// MaxRetryAfter is the longest rate-limit wait honoured before giving up.
const MaxRetryAfter = 60 * time.Second

// ReconnectBackoff is the backoff between reconnects of a listener.
func ReconnectBackoff() Backoff {
	return Backoff{Start: ReconnectStart, Cap: ReconnectCap}
}

// Retry calls try until it succeeds or fails with an error fatal reports,
// waiting with b between attempts and logging each failure under msg. When
// ctx ends first it returns a nil error, so callers check ctx.Err().
func Retry[T any](ctx context.Context, b Backoff, log *slog.Logger, msg string,
	fatal func(error) bool, try func(context.Context) (T, error),
) (T, error) {
	for {
		v, err := try(ctx)

		switch {
		case ctx.Err() != nil:
			return v, nil //nolint:nilerr // cancelled: the error is just the shutdown
		case err == nil || fatal(err):
			return v, err
		}

		wait := b.Next()
		log.Warn(msg, "err", err, "retry_in", wait)

		if !Sleep(ctx, wait) {
			return v, nil
		}
	}
}

// Reconnect runs session until ctx ends or a session fails with an error
// fatal reports. connected tells whether a session got through; one that
// then ended with a nil error was asked to reconnect by the server and
// starts again at once. Any other end waits with ReconnectBackoff, which a
// connect resets. name labels the log lines.
func Reconnect(ctx context.Context, log *slog.Logger, name string,
	fatal func(error) bool, session func(context.Context) (connected bool, err error),
) error {
	backoff := ReconnectBackoff()

	for {
		connected, err := session(ctx)

		switch {
		case ctx.Err() != nil:
			return nil //nolint:nilerr // cancelled: the connection error is just the shutdown
		case fatal(err):
			return err
		case connected:
			backoff.Reset()
		}

		if err == nil && connected {
			log.Info(name+" reconnecting", "reason", "requested by the server")

			continue
		}

		wait := backoff.Next()
		log.Warn(name+" connection", "err", err, "retry_in", wait)

		if !Sleep(ctx, wait) {
			return nil
		}
	}
}

// RetryOnce calls call, and once more if limited reports a rate limit with a
// wait of at most MaxRetryAfter.
func RetryOnce(ctx context.Context, limited func(error) (wait time.Duration, ok bool), call func() error) error {
	err := call()

	if wait, ok := limited(err); ok && wait <= MaxRetryAfter {
		if !Sleep(ctx, wait) {
			return ctx.Err()
		}

		err = call()
	}

	return err
}
