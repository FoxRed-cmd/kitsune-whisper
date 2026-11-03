package trust_test

import (
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func TestCheckVersionAcceptsPatchDriftAndLocalBuilds(t *testing.T) {
	for _, pair := range [][2]string{
		{"v0.1.0", "0.1.7"},
		{"dev", "0.1.0"},
		{"0.1.0", ""},
	} {
		if err := trust.CheckVersion(pair[0], pair[1]); err != nil {
			t.Errorf("CheckVersion(%q, %q) = %v", pair[0], pair[1], err)
		}
	}
}

func TestCheckVersionNamesTheSideToUpgrade(t *testing.T) {
	olderServer := trust.CheckVersion("v0.2.0", "0.1.9")
	if olderServer == nil || !strings.Contains(olderServer.Error(), "upgrade the Server") {
		t.Fatalf("older Server error = %v", olderServer)
	}
	newerServer := trust.CheckVersion("v0.1.0", "0.2.0")
	if newerServer == nil || !strings.Contains(newerServer.Error(), "upgrade the Client") {
		t.Fatalf("newer Server error = %v", newerServer)
	}
}

func TestApplyVersionReplacesAnAuthFailureWithAnUpgradeState(t *testing.T) {
	status := trust.Status{
		State:   trust.StateUnauthenticated,
		Version: "0.2.0",
		Error:   "the Server rejected the Device token",
	}

	applied := trust.ApplyVersion(status, "v0.1.0")

	if applied.State != trust.StateVersionMismatch {
		t.Fatalf("state = %q", applied.State)
	}
	if !strings.Contains(applied.Error, "upgrade the Client") {
		t.Fatalf("error = %q", applied.Error)
	}
	if status.State != trust.StateUnauthenticated {
		t.Fatal("ApplyVersion mutated the input")
	}
}

func TestApplyVersionKeepsACompatibleStatus(t *testing.T) {
	status := trust.Status{State: trust.StateOnline, Version: "0.1.9"}
	if applied := trust.ApplyVersion(status, "v0.1.0"); applied.State != trust.StateOnline || applied.Error != "" {
		t.Fatalf("status = %+v", applied)
	}
}
