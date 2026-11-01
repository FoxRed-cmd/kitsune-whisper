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
	return WAVDurationHeader(data, int64(len(data)))
}

// WAVDurationHeader returns the duration of a PCM WAV stream from a header
// prefix and the stream's full size. It lets a caller list a saved recording
// without reading the audio body.
func WAVDurationHeader(head []byte, totalSize int64) (time.Duration, error) {
	if len(head) < 12 || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0, ErrNotWAV
	}
	if totalSize < int64(len(head)) {
		totalSize = int64(len(head))
	}
	var (
		byteRate uint32
		dataSize int64
		haveFmt  bool
		haveData bool
	)
	for offset := 12; offset+8 <= len(head); {
		id := string(head[offset : offset+4])
		size := binary.LittleEndian.Uint32(head[offset+4 : offset+8])
		body := offset + 8
		switch id {
		case "fmt ":
			if body+16 > len(head) {
				return 0, ErrNotWAV
			}
			byteRate = binary.LittleEndian.Uint32(head[body+8 : body+12])
			haveFmt = true
		case "data":
			dataSize = int64(size)
			if int64(body)+dataSize > totalSize {
				dataSize = totalSize - int64(body)
			}
			haveData = true
		}
		if haveFmt && haveData {
			break
		}
		offset = body + int(size) + int(size%2)
	}
	if !haveFmt || !haveData || byteRate == 0 || dataSize < 0 {
		return 0, ErrNotWAV
	}
	seconds := float64(dataSize) / float64(byteRate)
	return time.Duration(seconds * float64(time.Second)), nil
}
