package inject

import (
	"context"
	"fmt"
	"runtime"

	"golang.design/x/clipboard"
)

// systemClipboard is the real Clipboard port, backed by
// golang.design/x/clipboard, which is cgo-free and speaks X11, Wayland
// data-control, and the native Windows clipboard.
type systemClipboard struct{}

func newClipboard() (Clipboard, error) {
	if err := clipboard.Init(); err != nil {
		return nil, fmt.Errorf("init clipboard: %w", err)
	}
	return systemClipboard{}, nil
}

func (systemClipboard) Read() (string, error) {
	data, err := clipboard.Read(context.Background(), clipboard.FmtText)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (systemClipboard) Write(text string) error {
	_, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
	return err
}

// CanRestore reports whether the previous clipboard survives a synthetic paste.
// Windows never tells a program when its paste is consumed, so restoring there
// would race the user's next copy; X11 and Wayland can restore.
func (systemClipboard) CanRestore() bool {
	return runtime.GOOS != "windows"
}
