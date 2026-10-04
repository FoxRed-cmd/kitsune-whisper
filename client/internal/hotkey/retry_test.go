package hotkey

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisterWithRetrySucceedsAfterFailures(t *testing.T) {
	var attempts int32
	err := registerWithRetry(context.Background(), func() error {
		if atomic.AddInt32(&attempts, 1) < 3 {
			return errors.New("display not ready")
		}
		return nil
	}, RetryPolicy{Attempts: 5, Initial: time.Millisecond, Max: 2 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("registerWithRetry error: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestRegisterWithRetryGivesUpAfterBudget(t *testing.T) {
	failure := errors.New("combination taken")
	var attempts int32
	err := registerWithRetry(context.Background(), func() error {
		atomic.AddInt32(&attempts, 1)
		return failure
	}, RetryPolicy{Attempts: 3, Initial: time.Millisecond, Max: time.Millisecond}, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestRegisterWithRetryStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int32
	err := registerWithRetry(ctx, func() error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("display not ready")
	}, RetryPolicy{Initial: time.Second}, func(int, error) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestRegisterWithRetryAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var attempts int32
	err := registerWithRetry(ctx, func() error {
		atomic.AddInt32(&attempts, 1)
		return nil
	}, RetryPolicy{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 0 {
		t.Fatalf("attempts = %d, want 0", got)
	}
}
