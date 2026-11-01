package shell_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
)

// saveSpooled writes one failed Utterance into dir and returns its entry id.
func saveSpooled(t *testing.T, dir string) string {
	t.Helper()
	pcm := make([]byte, 16000*2)
	utterance := cycle.Utterance{
		Audio:    audio.EncodeWAV(pcm, 16000, 1),
		Duration: time.Second,
	}
	store := spool.New(spool.Options{Dir: dir})
	if err := store.Save(utterance); err != nil {
		t.Fatalf("spool a failed utterance: %v", err)
	}
	entries, err := store.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	return entries[0].ID
}

func newSpoolService(dir string, rt *fakeRuntime) *shell.SpoolService {
	return shell.NewSpoolService(shell.SpoolOptions{
		Dir:     func() string { return dir },
		Runtime: rt,
	})
}

func TestSpoolServiceRetriesAgainstTheActiveProfileAndShowsTheText(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	rt.transcribeText = "hello world"
	service := newSpoolService(dir, rt)

	view := service.View()
	if len(view.Entries) != 1 || view.Entries[0].ID != id {
		t.Fatalf("view = %+v", view)
	}
	if view.Entries[0].Duration != 1 {
		t.Fatalf("duration = %v, want 1s", view.Entries[0].Duration)
	}

	text, err := service.Retry(context.Background(), id)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
	if injected, _ := rt.spoolCalls(); injected != 0 {
		t.Fatal("a retry injected text without the user asking")
	}
	view = service.View()
	if view.Selected != id || view.Text != "hello world" {
		t.Fatalf("view after retry = %+v", view)
	}
	if len(rt.transcribed) != 1 || len(rt.transcribed[0]) == 0 {
		t.Fatalf("retry did not send the saved audio: %d calls", len(rt.transcribed))
	}
}

func TestSpoolServiceCopiesInjectsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	rt.transcribeText = "hello world"
	service := newSpoolService(dir, rt)
	if _, err := service.Retry(context.Background(), id); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if err := service.Copy(id); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := service.Insert(id); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if injected, copied := rt.spoolCalls(); injected != 1 || copied != 1 {
		t.Fatalf("injected=%d copied=%d, want 1/1", injected, copied)
	}
	if rt.copied[0] != "hello world" || rt.injected[0] != "hello world" {
		t.Fatalf("copied %v injected %v", rt.copied, rt.injected)
	}

	if err := service.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if view := service.View(); len(view.Entries) != 0 || view.Text != "" {
		t.Fatalf("view after delete = %+v", view)
	}
	if _, err := os.Stat(filepath.Join(dir, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the spool file survived delete: %v", err)
	}
}

func TestSpoolServiceRequiresARetryBeforeCopyOrInsert(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	service := newSpoolService(dir, rt)

	if err := service.Copy(id); !errors.Is(err, shell.ErrSpoolRetryFirst) {
		t.Fatalf("copy error = %v, want ErrSpoolRetryFirst", err)
	}
	if err := service.Insert(id); !errors.Is(err, shell.ErrSpoolRetryFirst) {
		t.Fatalf("insert error = %v, want ErrSpoolRetryFirst", err)
	}
	if injected, copied := rt.spoolCalls(); injected != 0 || copied != 0 {
		t.Fatalf("injected=%d copied=%d, want no work", injected, copied)
	}
}

func TestSpoolServiceKeepsTheEntryWhenARetryFails(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	rt.transcribeErr = errors.New("server unreachable")
	service := newSpoolService(dir, rt)

	if _, err := service.Retry(context.Background(), id); err == nil {
		t.Fatal("retry succeeded against a failing Server")
	}
	view := service.View()
	if len(view.Entries) != 1 || view.Text != "" {
		t.Fatalf("view after a failed retry = %+v", view)
	}
	if injected, _ := rt.spoolCalls(); injected != 0 {
		t.Fatal("a failed retry injected text")
	}
}

func newSpoolShell(t *testing.T, rt *fakeRuntime, desktop *fakeDesktop, settings shell.Settings, service shell.Spool) *shell.Shell {
	t.Helper()
	return shell.New(shell.Options{
		Runtime:  rt,
		Desktop:  desktop,
		Settings: settings,
		Spool:    service,
		Version:  "test",
		Profile:  "Local Server",
		Logger:   &fakeLogger{},
	})
}

func TestSpoolIntentsDriveEveryAction(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	rt.transcribeText = "hello world"
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	service := newSpoolService(dir, rt)
	sh := newSpoolShell(t, rt, desktop, settings, service)

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSpoolRetry, EntryID: id}); err != nil {
		t.Fatalf("retry intent: %v", err)
	}
	view, ok := window.lastSpool()
	if !ok || view.Text != "hello world" || view.Selected != id {
		t.Fatalf("pushed spool view = %+v", view)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("entries = %+v", view.Entries)
	}
	messages, _ := window.lastMessages()
	if len(messages.notices) != 1 {
		t.Fatalf("messages = %+v", messages)
	}

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSpoolCopy, EntryID: id}); err != nil {
		t.Fatalf("copy intent: %v", err)
	}
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSpoolInsert, EntryID: id}); err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	if injected, copied := rt.spoolCalls(); injected != 1 || copied != 1 {
		t.Fatalf("injected=%d copied=%d, want 1/1", injected, copied)
	}

	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSpoolDelete, EntryID: id}); err != nil {
		t.Fatalf("delete intent: %v", err)
	}
	view, _ = window.lastSpool()
	if len(view.Entries) != 0 {
		t.Fatalf("entries after delete = %+v", view.Entries)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

// TestAFailedCycleRefreshesTheSpoolOnly proves a new failed Utterance appears
// on the Spool screen without rebuilding the edited Dictation form.
func TestAFailedCycleRefreshesTheSpoolOnly(t *testing.T) {
	dir := t.TempDir()
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	settings := newFakeSettings(i18n.English)
	sh := newSpoolShell(t, rt, desktop, settings, newSpoolService(dir, rt))

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	before := window.settingsCount()

	saveSpooled(t, dir)
	rt.emit(runtime.Status{Running: true, Phase: runtime.PhaseIdle, Error: "server unreachable"})
	waitFor(t, "the Spool refresh", func() bool {
		view, ok := window.lastSpool()
		return ok && len(view.Entries) == 1
	})
	if got := window.settingsCount(); got != before {
		t.Fatalf("settings rebuilds = %d, want %d (no form reset)", got, before)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}

func TestSpoolCopyBeforeRetryShowsAnActionableMessage(t *testing.T) {
	dir := t.TempDir()
	id := saveSpooled(t, dir)
	rt := newFakeRuntime()
	desktop := newFakeDesktop()
	sh := newSpoolShell(t, rt, desktop, newFakeSettings(i18n.English), newSpoolService(dir, rt))

	done := startWithWindow(t, sh, desktop)
	window := desktop.getWindow()
	if err := window.sendIntent(t, shell.Intent{Action: shell.IntentSpoolCopy, EntryID: id}); err != nil {
		t.Fatalf("copy intent: %v", err)
	}
	messages, _ := window.lastMessages()
	if len(messages.failures) != 1 || !strings.Contains(messages.failures[0], "Retry") {
		t.Fatalf("messages = %+v", messages)
	}

	desktop.getTray().do(shell.ActionQuit)
	if res := <-done; res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
}
