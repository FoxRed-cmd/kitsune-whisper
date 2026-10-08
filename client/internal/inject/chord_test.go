package inject

import (
	"reflect"
	"testing"
)

func TestChordEvents(t *testing.T) {
	cases := []struct {
		name     string
		shortcut Shortcut
		want     []keyEvent
	}{
		{
			name:     "ctrl_v",
			shortcut: CtrlV,
			want: []keyEvent{
				{keyCtrl, true},
				{keyV, true}, {keyV, false},
				{keyCtrl, false},
			},
		},
		{
			name:     "ctrl_shift_v",
			shortcut: CtrlShiftV,
			want: []keyEvent{
				{keyCtrl, true}, {keyShift, true},
				{keyV, true}, {keyV, false},
				{keyShift, false}, {keyCtrl, false},
			},
		},
		{
			name:     "shift_insert",
			shortcut: ShiftInsert,
			want: []keyEvent{
				{keyShift, true},
				{keyInsert, true}, {keyInsert, false},
				{keyShift, false},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chordEvents(tc.shortcut); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("chordEvents(%v) = %#v, want %#v", tc.shortcut, got, tc.want)
			}
		})
	}
}
