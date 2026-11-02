//go:build !windows

package process

import "testing"

func TestHideLeavesTheCommandAlone(t *testing.T) {
	cmd := Command("example-helper")
	if cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %#v, want nil outside Windows", cmd.SysProcAttr)
	}
}
