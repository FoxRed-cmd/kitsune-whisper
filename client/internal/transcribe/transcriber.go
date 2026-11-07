// Package transcribe is the Client's Transcriber port and its HTTP adapter.
//
// The port takes one Utterance's audio (canonical 16 kHz mono PCM WAV) and
// returns a Transcription. The HTTP adapter speaks the frozen /transcribe
// contract; tests drive it against httptest.Server.
package transcribe

import (
	"context"
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/api"
)

// Applied reports which processing steps actually ran on the utterance.
type Applied = api.AppliedFlags

// Transcription is the text the Server returns for an utterance. Text is the
// Delivered text (after any requested processing); RawText is the untouched
// Transcription, and Warnings explains any step that was requested but skipped.
//
// The shape is generated from the Server's OpenAPI contract (ADR 0014).
type Transcription = api.TranscribeResponse

// Transcriber transcribes one utterance's audio.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte) (Transcription, error)
}

// ServerError is the Server's error envelope, decoded.
type ServerError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server %d %s: %s", e.StatusCode, e.Code, e.Message)
}
