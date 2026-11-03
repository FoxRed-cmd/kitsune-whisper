package shell

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
)

// ErrSpoolRetryFirst reports that Copy or Insert was asked for an entry whose
// Transcription has not been produced yet. The Spool screen retries first.
var ErrSpoolRetryFirst = errors.New("spool: retry the Utterance before copying or inserting its text")

// Spool is the Spool screen's service. *SpoolService implements it, and the
// Settings window only ever calls this interface.
type Spool interface {
	// View lists the saved Utterances and the text of the last retry.
	View() SpoolView
	// Retry transcribes one saved Utterance against the active Server profile
	// and returns its text without injecting it.
	Retry(ctx context.Context, id string) (string, error)
	// Copy puts the retried text on the clipboard.
	Copy(id string) error
	// Insert places the retried text into the focused field.
	Insert(id string) error
	// Delete removes one saved Utterance.
	Delete(id string) error
}

// SpoolView is the Spool screen's data. The Shell adds the translated labels.
type SpoolView struct {
	Entries []spool.Entry `json:"entries"`
	// Selected is the entry whose retry produced Text, if any.
	Selected string `json:"selected,omitempty"`
	// Text is the Transcription of Selected, as shown on screen.
	Text string `json:"text,omitempty"`
	// Error is why the list could not be read, or "".
	Error  string      `json:"error,omitempty"`
	Labels SpoolLabels `json:"labels"`
}

// SpoolLabels are the translated strings of the Spool screen.
type SpoolLabels struct {
	Title         string `json:"title"`
	Help          string `json:"help"`
	Empty         string `json:"empty"`
	SavedAt       string `json:"savedAt"`
	Duration      string `json:"duration"`
	Text          string `json:"text"`
	EmptyText     string `json:"emptyText"`
	Retry         string `json:"retry"`
	Copy          string `json:"copy"`
	Insert        string `json:"insert"`
	Delete        string `json:"delete"`
	DeleteConfirm string `json:"deleteConfirm"`
}

// SpoolOptions wires the Spool screen to the settings and the running Client.
type SpoolOptions struct {
	// Dir returns the current spool directory; empty means the default cache
	// path. It is read per operation so a live spool_dir change is followed.
	Dir func() string
	// Runtime retries against the active profile and performs copy and insert.
	Runtime Runtime
}

// SpoolService is the Spool screen's implementation of Spool.
type SpoolService struct {
	opts SpoolOptions

	mu       sync.Mutex
	texts    map[string]string
	selected string
	listErr  string
}

// NewSpoolService builds a Spool screen service. It performs no I/O.
func NewSpoolService(opts SpoolOptions) *SpoolService {
	return &SpoolService{opts: opts, texts: map[string]string{}}
}

func (s *SpoolService) store() *spool.Spool {
	dir := ""
	if s.opts.Dir != nil {
		dir = s.opts.Dir()
	}
	return spool.New(spool.Options{Dir: dir})
}

// View lists the saved Utterances, newest first, plus the last retry's text.
func (s *SpoolService) View() SpoolView {
	entries, err := s.store().Entries()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.listErr = err.Error()
	} else {
		s.listErr = ""
	}
	view := SpoolView{Entries: entries, Selected: s.selected, Error: s.listErr}
	if s.selected != "" {
		view.Text = s.texts[s.selected]
	}
	return view
}

// Retry transcribes one saved Utterance against the active Server profile. The
// text is shown to the user; injection only happens when they choose Insert.
func (s *SpoolService) Retry(ctx context.Context, id string) (string, error) {
	if s.opts.Runtime == nil {
		return "", errors.New("spool: retry is unavailable in this build")
	}
	utterance, err := s.store().Load(id)
	if err != nil {
		return "", err
	}
	transcription, err := s.opts.Runtime.Transcribe(ctx, utterance.Audio)
	if err != nil {
		return "", fmt.Errorf("spool: retry on the active Server profile: %w", err)
	}
	text := strings.TrimSpace(transcription.Text)
	s.mu.Lock()
	s.texts[id] = text
	s.selected = id
	s.mu.Unlock()
	return text, nil
}

// Copy puts the retried text on the clipboard.
func (s *SpoolService) Copy(id string) error {
	text, ok := s.retried(id)
	if !ok {
		return ErrSpoolRetryFirst
	}
	if err := s.opts.Runtime.Copy(text); err != nil {
		return fmt.Errorf("spool: copy the text: %w", err)
	}
	return nil
}

// Insert places the retried text into the focused field.
func (s *SpoolService) Insert(id string) error {
	text, ok := s.retried(id)
	if !ok {
		return ErrSpoolRetryFirst
	}
	if err := s.opts.Runtime.Inject(text); err != nil {
		return fmt.Errorf("spool: insert the text: %w", err)
	}
	return nil
}

// Delete removes one saved Utterance and forgets its retried text.
func (s *SpoolService) Delete(id string) error {
	if err := s.store().Delete(id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.texts, id)
	if s.selected == id {
		s.selected = ""
	}
	s.mu.Unlock()
	return nil
}

func (s *SpoolService) retried(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.Runtime == nil {
		return "", false
	}
	text, ok := s.texts[id]
	return text, ok
}

// spoolLabels translates the Spool screen.
func (s *Shell) spoolLabels() SpoolLabels {
	catalog := s.catalog()
	return SpoolLabels{
		Title:         catalog.T("section.spool"),
		Help:          catalog.T("spool.help"),
		Empty:         catalog.T("spool.empty"),
		SavedAt:       catalog.T("spool.savedAt"),
		Duration:      catalog.T("spool.duration"),
		Text:          catalog.T("spool.text"),
		EmptyText:     catalog.T("spool.emptyText"),
		Retry:         catalog.T("action.retry"),
		Copy:          catalog.T("action.copyText"),
		Insert:        catalog.T("action.insertText"),
		Delete:        catalog.T("action.delete"),
		DeleteConfirm: catalog.T("spool.deleteConfirm"),
	}
}

// spoolView renders the Spool screen with the active catalog's labels.
func (s *Shell) spoolView() SpoolView {
	if s.opts.Spool == nil {
		return SpoolView{}
	}
	view := s.opts.Spool.View()
	view.Labels = s.spoolLabels()
	return view
}

// retrySpool transcribes one saved Utterance against the active profile and
// shows the text. A failure keeps the entry and reports one message.
func (s *Shell) retrySpool(id string) {
	if s.opts.Spool == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	if strings.TrimSpace(id) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	text, err := s.opts.Spool.Retry(ctx, id)
	if err != nil {
		s.pushSpool()
		s.pushMessages(nil, []string{s.catalog().T("error.spool.retry", err.Error())})
		return
	}
	s.pushSpool()
	if strings.TrimSpace(text) == "" {
		s.pushMessages([]string{s.catalog().T("notice.spool.empty")}, nil)
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.spool.retried")}, nil)
}

// spoolText copies or inserts the retried text of one entry.
func (s *Shell) spoolText(id string, copyText bool) {
	if s.opts.Spool == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	var err error
	if copyText {
		err = s.opts.Spool.Copy(id)
	} else {
		err = s.opts.Spool.Insert(id)
	}
	switch {
	case errors.Is(err, ErrSpoolRetryFirst):
		s.pushMessages(nil, []string{s.catalog().T("error.spool.retryFirst")})
	case err != nil && copyText:
		s.pushMessages(nil, []string{s.catalog().T("error.spool.copy", err.Error())})
	case err != nil:
		s.pushMessages(nil, []string{s.catalog().T("error.spool.insert", err.Error())})
	case copyText:
		s.pushMessages([]string{s.catalog().T("notice.spool.copied")}, nil)
	default:
		s.pushMessages([]string{s.catalog().T("notice.spool.inserted")}, nil)
	}
}

// deleteSpool removes one saved Utterance and re-renders the list.
func (s *Shell) deleteSpool(id string) {
	if s.opts.Spool == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	if err := s.opts.Spool.Delete(id); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.spool.delete", err.Error())})
		return
	}
	s.pushSpool()
	s.pushMessages([]string{s.catalog().T("notice.spool.deleted")}, nil)
}

// formatSpoolDuration renders a saved Utterance's length for the page.
func formatSpoolDuration(seconds float64) string {
	if seconds <= 0 {
		return "—"
	}
	return strconv.FormatFloat(seconds, 'f', 1, 64) + " s"
}
