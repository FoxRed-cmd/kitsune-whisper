package version_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/version"
)

func TestParseAcceptsReleaseAndTagForms(t *testing.T) {
	cases := []struct {
		input string
		want  version.Number
	}{
		{"1.2.3", version.Number{Major: 1, Minor: 2, Patch: 3}},
		{"v1.2.3", version.Number{Major: 1, Minor: 2, Patch: 3}},
		{"V0.1", version.Number{Major: 0, Minor: 1}},
		{"v2.4.6-rc1", version.Number{Major: 2, Minor: 4, Patch: 6, Pre: "rc1"}},
		{"2.4.6+build.7", version.Number{Major: 2, Minor: 4, Patch: 6, Pre: "build.7"}},
	}
	for _, test := range cases {
		got, ok := version.Parse(test.input)
		if !ok {
			t.Fatalf("Parse(%q) failed", test.input)
		}
		if got != test.want {
			t.Errorf("Parse(%q) = %+v, want %+v", test.input, got, test.want)
		}
	}
}

func TestParseRejectsLocalAndMalformedVersions(t *testing.T) {
	for _, input := range []string{"", "dev", "1", "1.x", "v1.2.3.4", "1..2", "latest"} {
		if _, ok := version.Parse(input); ok {
			t.Errorf("Parse(%q) unexpectedly succeeded", input)
		}
	}
}

func TestSameMajorMinorAllowsPatchDriftAndLocalBuilds(t *testing.T) {
	if !version.SameMajorMinor("v0.1.0", "0.1.7") {
		t.Fatal("patch difference should be compatible")
	}
	if version.SameMajorMinor("v0.1.0", "0.2.0") {
		t.Fatal("minor difference should be incompatible")
	}
	if version.SameMajorMinor("v1.2.0", "v2.2.0") {
		t.Fatal("major difference should be incompatible")
	}
	for _, pair := range [][2]string{{"dev", "0.1.0"}, {"0.1.0", "dev"}, {"", "0.1.0"}} {
		if !version.SameMajorMinor(pair[0], pair[1]) {
			t.Errorf("%q vs %q should skip the check", pair[0], pair[1])
		}
	}
}

func TestNewerComparesReleases(t *testing.T) {
	cases := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v0.1.10", "v0.1.9", true},
		{"v0.1.2", "v0.1.2", false},
		{"v0.1.2", "v0.1.3", false},
		{"v0.1.2", "dev", false},
		{"dev", "v0.1.2", false},
		{"v0.1.2", "v0.1.2-rc1", true},
	}
	for _, test := range cases {
		if got := version.Newer(test.candidate, test.current); got != test.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", test.candidate, test.current, got, test.want)
		}
	}
}
