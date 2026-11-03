package humanize_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/humanize"
)

func TestBytesRendersDecimalUnits(t *testing.T) {
	cases := []struct {
		value int64
		want  string
	}{
		{value: -1, want: "—"},
		{value: 0, want: "—"},
		{value: 999, want: "999 B"},
		{value: 1000, want: "1.0 KB"},
		{value: 1500, want: "1.5 KB"},
		{value: 1_500_000, want: "1.5 MB"},
		{value: 2_500_000_000, want: "2.5 GB"},
	}
	for _, tc := range cases {
		if got := humanize.Bytes(tc.value); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
