package hotkey

import (
	"context"
	"time"
)

// RetryPolicy bounds how hotkey registration is retried at startup, since the
// Client may launch before the display is ready (ADR 0001).
type RetryPolicy struct {
	// Attempts is the total number of attempts; <= 0 retries until ctx ends.
	Attempts int
	// Initial is the first backoff; <= 0 uses 100ms.
	Initial time.Duration
	// Max caps the exponential backoff; <= 0 uses 10s.
	Max time.Duration
}

// registerWithRetry calls attempt until it succeeds, backing off exponentially
// between failures. It returns the last error when the attempt budget is spent,
// or ctx.Err() when ctx ends first.
func registerWithRetry(ctx context.Context, attempt func() error, policy RetryPolicy, onRetry func(attempt int, err error)) error {
	initial := policy.Initial
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	maxDelay := policy.Max
	if maxDelay <= 0 {
		maxDelay = 10 * time.Second
	}
	if maxDelay < initial {
		maxDelay = initial
	}

	delay := initial
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt()
		if err == nil {
			return nil
		}
		if onRetry != nil {
			onRetry(n, err)
		}
		if policy.Attempts > 0 && n >= policy.Attempts {
			return err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay *= 2; delay > maxDelay {
			delay = maxDelay
		}
	}
}
