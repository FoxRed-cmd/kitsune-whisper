package inject

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolveWaylandTools(t *testing.T) {
	cases := []struct {
		name       string
		requested  string
		hasWtype   bool
		hasYdotool bool
		want       []waylandTool
		wantErr    bool
	}{
		{name: "auto lists wtype then ydotool", requested: "auto", hasWtype: true, hasYdotool: true, want: []waylandTool{toolWtype, toolYdotool}},
		{name: "auto lists ydotool only", requested: "auto", hasYdotool: true, want: []waylandTool{toolYdotool}},
		{name: "auto with nothing", requested: "auto", want: nil},
		{name: "empty is auto", requested: "", hasWtype: true, want: []waylandTool{toolWtype}},
		{name: "explicit wtype present", requested: "wtype", hasWtype: true, want: []waylandTool{toolWtype}},
		{name: "explicit wtype missing", requested: "wtype", hasYdotool: true, want: nil},
		{name: "explicit ydotool present", requested: "ydotool", hasYdotool: true, want: []waylandTool{toolYdotool}},
		{name: "explicit ydotool missing", requested: "ydotool", hasWtype: true, want: nil},
		{name: "none", requested: "none", hasWtype: true, want: nil},
		{name: "unknown", requested: "xdotool", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveWaylandTools(tc.requested, tc.hasWtype, tc.hasYdotool)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveWaylandTools(%q) = %v, want error", tc.requested, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWaylandTools(%q) error: %v", tc.requested, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolveWaylandTools(%q) = %v, want %v", tc.requested, got, tc.want)
			}
		})
	}
}

func TestRunChainFallsThrough(t *testing.T) {
	var tried []waylandTool
	run := func(tool waylandTool, _ Shortcut) error {
		tried = append(tried, tool)
		if tool == toolWtype {
			return errors.New("wtype: unsupported compositor")
		}
		return nil
	}
	err := runChain([]waylandTool{toolWtype, toolYdotool}, CtrlV, run)
	if err != nil {
		t.Fatalf("runChain error: %v", err)
	}
	if !reflect.DeepEqual(tried, []waylandTool{toolWtype, toolYdotool}) {
		t.Fatalf("tried %v, want wtype then ydotool", tried)
	}
}

func TestRunChainReturnsLastError(t *testing.T) {
	last := errors.New("ydotool failed")
	run := func(tool waylandTool, _ Shortcut) error {
		if tool == toolYdotool {
			return last
		}
		return errors.New("wtype failed")
	}
	err := runChain([]waylandTool{toolWtype, toolYdotool}, CtrlV, run)
	if !errors.Is(err, last) {
		t.Fatalf("runChain error = %v, want the last failure", err)
	}
}

func TestRunChainEmpty(t *testing.T) {
	run := func(waylandTool, Shortcut) error {
		t.Fatal("run should not be called for an empty chain")
		return nil
	}
	if err := runChain(nil, CtrlV, run); !errors.Is(err, errNoWaylandTool) {
		t.Fatalf("runChain error = %v, want errNoWaylandTool", err)
	}
}

func TestWaylandArgs(t *testing.T) {
	cases := []struct {
		name     string
		tool     waylandTool
		shortcut Shortcut
		wantName string
		wantArgs []string
	}{
		{
			name: "wtype ctrl_v", tool: toolWtype, shortcut: CtrlV,
			wantName: "wtype", wantArgs: []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"},
		},
		{
			name: "wtype ctrl_shift_v", tool: toolWtype, shortcut: CtrlShiftV,
			wantName: "wtype",
			wantArgs: []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"},
		},
		{
			name: "wtype shift_insert", tool: toolWtype, shortcut: ShiftInsert,
			wantName: "wtype", wantArgs: []string{"-M", "shift", "-k", "Insert", "-m", "shift"},
		},
		{
			name: "ydotool ctrl_v", tool: toolYdotool, shortcut: CtrlV,
			wantName: "ydotool", wantArgs: []string{"key", "29:1", "47:1", "47:0", "29:0"},
		},
		{
			name: "ydotool ctrl_shift_v", tool: toolYdotool, shortcut: CtrlShiftV,
			wantName: "ydotool",
			wantArgs: []string{"key", "29:1", "42:1", "47:1", "47:0", "42:0", "29:0"},
		},
		{
			name: "ydotool shift_insert", tool: toolYdotool, shortcut: ShiftInsert,
			wantName: "ydotool", wantArgs: []string{"key", "42:1", "110:1", "110:0", "42:0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, args := waylandArgs(tc.tool, tc.shortcut)
			if name != tc.wantName || !reflect.DeepEqual(args, tc.wantArgs) {
				t.Fatalf("waylandArgs(%v, %v) = (%q, %v), want (%q, %v)",
					tc.tool, tc.shortcut, name, args, tc.wantName, tc.wantArgs)
			}
		})
	}
}
