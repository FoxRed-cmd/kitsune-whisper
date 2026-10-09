//go:build linux

package inject

import (
	"context"
	"errors"

	"golang.design/x/clipboard"
)

// errNoPrimary reports that no backend could reach the primary selection, so a
// Shift+Insert transcription stays only on the ordinary clipboard.
var errNoPrimary = errors.New("primary selection unavailable")

// ReadPrimary returns the primary selection through the native clipboard, which
// speaks to X11, and to XWayland under a Wayland session.
func (systemClipboard) ReadPrimary() (string, error) {
	data, err := clipboard.Read(context.Background(), clipboard.FmtText, clipboard.FromPrimary())
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WritePrimary replaces the primary selection through the native clipboard.
func (systemClipboard) WritePrimary(text string) error {
	_, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text), clipboard.FromPrimary())
	return err
}

// ReadPrimary returns the primary selection through wl-paste --primary.
func (c commandClipboard) ReadPrimary() (string, error) {
	out, err := c.run(c.readCommand, []string{"--no-newline", "--primary"}, nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// WritePrimary replaces the primary selection through wl-copy --primary.
func (c commandClipboard) WritePrimary(text string) error {
	_, err := c.run(c.writeCommand, []string{"--primary"}, []byte(text))
	return err
}

// ReadPrimary prefers the native clipboard and falls back to wl-paste
// --primary, mirroring Read.
func (c fallbackClipboard) ReadPrimary() (string, error) {
	if primary, ok := c.primary.(PrimaryClipboard); ok {
		if text, err := primary.ReadPrimary(); err == nil {
			return text, nil
		}
	}
	if fallback, ok := c.fallback.(PrimaryClipboard); ok {
		return fallback.ReadPrimary()
	}
	return "", errNoPrimary
}

// WritePrimary prefers the native clipboard and falls back to wl-copy --primary,
// mirroring Write.
func (c fallbackClipboard) WritePrimary(text string) error {
	if primary, ok := c.primary.(PrimaryClipboard); ok {
		if err := primary.WritePrimary(text); err == nil {
			return nil
		}
	}
	if fallback, ok := c.fallback.(PrimaryClipboard); ok {
		return fallback.WritePrimary(text)
	}
	return errNoPrimary
}
