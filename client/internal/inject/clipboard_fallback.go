package inject

// fallbackClipboard tries a primary Clipboard and falls back to another when it
// fails. On Wayland it pairs the native clipboard with the wl-copy/wl-paste
// commands, which cover compositors the library cannot speak to.
type fallbackClipboard struct {
	primary  Clipboard
	fallback Clipboard
}

// Read returns the primary clipboard's text, or the fallback's on error.
func (c fallbackClipboard) Read() (string, error) {
	text, err := c.primary.Read()
	if err == nil {
		return text, nil
	}
	return c.fallback.Read()
}

// Write sets the primary clipboard, falling back when that fails.
func (c fallbackClipboard) Write(text string) error {
	if err := c.primary.Write(text); err == nil {
		return nil
	}
	return c.fallback.Write(text)
}

// CanRestore reports whether either backing clipboard can restore.
func (c fallbackClipboard) CanRestore() bool {
	return c.primary.CanRestore() || c.fallback.CanRestore()
}

// commandRunner runs a clipboard command, feeding stdin and returning stdout.
type commandRunner func(name string, args []string, stdin []byte) ([]byte, error)

// commandClipboard reads and writes the clipboard through external commands,
// used as wl-paste/wl-copy on Wayland.
type commandClipboard struct {
	readCommand  string
	writeCommand string
	run          commandRunner
}

// Read returns the command's stdout.
func (c commandClipboard) Read() (string, error) {
	out, err := c.run(c.readCommand, []string{"--no-newline"}, nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Write pipes text to the command's stdin.
func (c commandClipboard) Write(text string) error {
	_, err := c.run(c.writeCommand, nil, []byte(text))
	return err
}

// CanRestore reports that the command clipboard supports restoring.
func (commandClipboard) CanRestore() bool { return true }
