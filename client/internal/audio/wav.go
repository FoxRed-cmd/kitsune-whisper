// Package audio handles the canonical client audio format: 16 kHz mono PCM WAV.
//
// Ticket scope is the one-shot CLI, so this layer only reads WAV metadata
// (duration) to select a request timeout; microphone capture lands later.
package audio

import (
	"encoding/binary"
	"errors"
	"time"
)

// ErrNotWAV is returned when data is not a RIFF/WAVE stream.
var ErrNotWAV = errors.New("not a WAV file")

// WAVDuration returns the duration of a PCM WAV stream from its headers.
func WAVDuration(data []byte) (time.Duration, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, ErrNotWAV
	}
	var (
		byteRate uint32
		dataSize uint32
		haveFmt  bool
		haveData bool
	)
	for offset := 12; offset+8 <= len(data); {
		id := string(data[offset : offset+4])
		size := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		body := offset + 8
		switch id {
		case "fmt ":
			if body+16 > len(data) {
				return 0, ErrNotWAV
			}
			byteRate = binary.LittleEndian.Uint32(data[body+8 : body+12])
			haveFmt = true
		case "data":
			dataSize = size
			if uint64(body)+uint64(size) > uint64(len(data)) {
				dataSize = uint32(len(data) - body)
			}
			haveData = true
		}
		offset = body + int(size) + int(size%2)
	}
	if !haveFmt || !haveData || byteRate == 0 {
		return 0, ErrNotWAV
	}
	seconds := float64(dataSize) / float64(byteRate)
	return time.Duration(seconds * float64(time.Second)), nil
}
