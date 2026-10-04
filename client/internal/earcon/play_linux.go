//go:build linux

package earcon

import (
	"errors"
	"os"
	"os/exec"
)

// players are tried in order; the first installed one wins.
var players = [][]string{
	{"pw-play"},
	{"paplay"},
	{"aplay", "-q"},
	{"ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet"},
}

// play writes the WAV to a temp file and hands it to an installed system
// player. Playback is asynchronous; the temp file is removed once it exits.
func play(wav []byte) error {
	file, err := os.CreateTemp("", "kitsune-earcon-*.wav")
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err := file.Write(wav); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}

	for _, player := range players {
		path, err := exec.LookPath(player[0])
		if err != nil {
			continue
		}
		args := append(append([]string(nil), player[1:]...), name)
		cmd := exec.Command(path, args...)
		if err := cmd.Start(); err != nil {
			continue
		}
		go func() {
			_ = cmd.Wait()
			_ = os.Remove(name)
		}()
		return nil
	}

	_ = os.Remove(name)
	return errors.New("no audio player found (tried pw-play, paplay, aplay, ffplay)")
}
