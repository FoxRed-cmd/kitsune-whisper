package notify_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/notify"
)

func TestNotifySendsOnceAndReportsAFailureWithoutRetrying(t *testing.T) {
	var mu sync.Mutex
	var calls [][2]string
	notifier := notify.New(notify.Options{
		Run: func(title, body string) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, [2]string{title, body})
			return errors.New("no notification daemon")
		},
		OnLog: func(string) {},
	})
	notifier.Notify("Dictation failed", "server unreachable")

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		count := len(calls)
		mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("notification was not sent")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("run calls = %d, want 1 (no retry)", len(calls))
	}
	if calls[0] != [2]string{"Dictation failed", "server unreachable"} {
		t.Fatalf("notification = %v", calls[0])
	}
}
