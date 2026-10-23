package indicator

import (
	"math"
	"testing"
)

func TestLevelAboveFloorIsRelativeToTheNoiseFloor(t *testing.T) {
	cases := []struct {
		name       string
		rms, floor float64
		want       float64
	}{
		{name: "at the floor", rms: 0.04, floor: 0.04, want: 0},
		{name: "below the floor", rms: 0.02, floor: 0.04, want: 0},
		{name: "twice the floor", rms: 0.08, floor: 0.04, want: math.Tanh(6.02 / 18)},
		{name: "four times the floor", rms: 0.16, floor: 0.04, want: math.Tanh(12.04 / 18)},
		{name: "quiet device behaves the same", rms: 0.004, floor: 0.002, want: math.Tanh(6.02 / 18)},
		{name: "digitally silent", rms: 0, floor: 0, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := levelAboveFloor(tc.rms, tc.floor)
			if math.Abs(got-tc.want) > 0.01 {
				t.Fatalf("levelAboveFloor(%v, %v) = %v, want %v", tc.rms, tc.floor, got, tc.want)
			}
		})
	}
}

func TestLevelTargetIgnoresSilentSamplesBeforeCalibration(t *testing.T) {
	i := &Indicator{}
	for n := 0; n < 10; n++ {
		if got := i.levelTarget(0); got != 0 {
			t.Fatalf("silent sample %d = %v, want 0", n, got)
		}
	}
	if got := i.levelTarget(0.04); got != 0 {
		t.Fatalf("calibration sample = %v, want 0", got)
	}
	if got := i.levelTarget(0.05); got > 0.5 {
		t.Fatalf("one dB above the floor = %v, want a small level", got)
	}
}

func TestLevelTargetTracksTheNoiseFloorAndReactsToSpeech(t *testing.T) {
	i := &Indicator{}
	if got := i.levelTarget(0.04); got != 0 {
		t.Fatalf("first sample calibrated to %v, want 0", got)
	}
	for n := 0; n < 30; n++ {
		i.levelTarget(0.04)
	}
	if got := i.levelTarget(0.08); got < 0.3 {
		t.Fatalf("speech twice the floor = %v, want at least 0.3", got)
	}

	// A quieter room pulls the floor down, so the same speech reads louder.
	for n := 0; n < 30; n++ {
		i.levelTarget(0.01)
	}
	if got := i.levelTarget(0.04); got < 0.45 {
		t.Fatalf("speech four times the quieter floor = %v, want at least 0.45", got)
	}
}
