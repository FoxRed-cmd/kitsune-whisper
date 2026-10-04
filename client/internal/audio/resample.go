package audio

import (
	"encoding/binary"
	"math"
)

// Canonical capture format: the Server expects 16 kHz mono PCM.
const (
	TargetSampleRate = 16000
	TargetChannels   = 1
)

// ResampleToMono16k converts interleaved little-endian S16 PCM captured at
// sampleRate with channels channels into canonical 16 kHz mono S16. When the
// source is already 16 kHz mono it returns pcm unchanged, so no conversion
// runs on the common device-native path.
func ResampleToMono16k(pcm []byte, sampleRate, channels int) []byte {
	if channels <= 0 {
		channels = 1
	}
	if sampleRate == TargetSampleRate && channels == TargetChannels {
		return pcm
	}
	mono := downmix(pcm, channels)
	if sampleRate <= 0 || sampleRate == TargetSampleRate {
		return mono
	}
	if sampleRate > TargetSampleRate {
		// Low-pass before decimating, or content above the target Nyquist
		// (8 kHz) folds back into the band as aliasing.
		mono = lowPass(mono, sampleRate, TargetSampleRate/2)
	}
	return resampleLinear(mono, sampleRate, TargetSampleRate)
}

// firHalfTaps sets the low-pass length to 33 taps: a short, cheap filter whose
// stopband begins around the target Nyquist.
const firHalfTaps = 16

// lowPass applies a Hann-windowed sinc low-pass at cutoff Hz, clamping at the
// edges so a constant signal (DC) survives untouched.
func lowPass(mono []byte, sampleRate, cutoff int) []byte {
	taps := lowPassTaps(sampleRate, cutoff, firHalfTaps)
	frames := len(mono) / 2
	out := make([]byte, frames*2)
	for frame := 0; frame < frames; frame++ {
		var acc float64
		for k, tap := range taps {
			index := frame + k - firHalfTaps
			switch {
			case index < 0:
				index = 0
			case index >= frames:
				index = frames - 1
			}
			acc += tap * float64(int16(binary.LittleEndian.Uint16(mono[index*2:])))
		}
		binary.LittleEndian.PutUint16(out[frame*2:], uint16(int16(math.Round(acc))))
	}
	return out
}

func lowPassTaps(sampleRate, cutoff, half int) []float64 {
	n := 2*half + 1
	fc := float64(cutoff) / float64(sampleRate)
	taps := make([]float64, n)
	var sum float64
	for i := 0; i < n; i++ {
		m := float64(i - half)
		var sinc float64
		if m == 0 {
			sinc = 2 * fc
		} else {
			sinc = math.Sin(2*math.Pi*fc*m) / (math.Pi * m)
		}
		window := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		taps[i] = sinc * window
		sum += taps[i]
	}
	for i := range taps {
		taps[i] /= sum
	}
	return taps
}

// downmix averages interleaved channels down to mono, leaving mono untouched.
func downmix(pcm []byte, channels int) []byte {
	if channels <= 1 {
		return pcm
	}
	frames := len(pcm) / (2 * channels)
	out := make([]byte, frames*2)
	for frame := 0; frame < frames; frame++ {
		var sum int32
		base := frame * channels * 2
		for channel := 0; channel < channels; channel++ {
			sum += int32(int16(binary.LittleEndian.Uint16(pcm[base+channel*2:])))
		}
		binary.LittleEndian.PutUint16(out[frame*2:], uint16(int16(sum/int32(channels))))
	}
	return out
}

// resampleLinear linearly interpolates mono S16 from srcRate to dstRate.
func resampleLinear(mono []byte, srcRate, dstRate int) []byte {
	inFrames := len(mono) / 2
	if inFrames == 0 {
		return mono
	}
	outFrames := inFrames * dstRate / srcRate
	if outFrames < 1 {
		outFrames = 1
	}
	out := make([]byte, outFrames*2)
	ratio := float64(srcRate) / float64(dstRate)
	for i := 0; i < outFrames; i++ {
		pos := float64(i) * ratio
		i0 := int(pos)
		frac := pos - float64(i0)
		if i0 >= inFrames-1 {
			i0 = inFrames - 1
			frac = 0
		}
		i1 := i0 + 1
		if i1 >= inFrames {
			i1 = inFrames - 1
		}
		s0 := float64(int16(binary.LittleEndian.Uint16(mono[i0*2:])))
		s1 := float64(int16(binary.LittleEndian.Uint16(mono[i1*2:])))
		value := int16(math.Round(s0 + frac*(s1-s0)))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
	}
	return out
}
