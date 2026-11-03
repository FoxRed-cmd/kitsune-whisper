// Package version parses the Client and Server build versions and decides
// whether two releases may talk to each other.
//
// Release tags carry a "v" prefix and the Server reports a bare version, so
// both forms parse. A local build ("dev") or an unreadable value disables the
// comparison instead of turning every request into an upgrade error.
package version

import (
	"strconv"
	"strings"
)

// Number is a parsed major.minor.patch release version.
type Number struct {
	Major int
	Minor int
	Patch int
	// Pre is the pre-release suffix, without the leading hyphen.
	Pre string
}

// Parse reads "v1.2.3", "1.2", and "1.2.3-rc1". A value with fewer than two
// numeric components, or with a non-numeric component, is not a release
// version and returns ok=false.
func Parse(value string) (Number, bool) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "v"), "V")
	if trimmed == "" {
		return Number{}, false
	}
	if index := strings.IndexAny(trimmed, "-+"); index >= 0 {
		trimmed = trimmed[:index]
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Number{}, false
	}
	numbers := make([]int, len(parts))
	for index, part := range parts {
		if part == "" {
			return Number{}, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Number{}, false
		}
		numbers[index] = number
	}
	parsed := Number{Major: numbers[0], Minor: numbers[1]}
	if len(numbers) == 3 {
		parsed.Patch = numbers[2]
	}
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		parsed.Pre = strings.TrimSpace(value[index+1:])
	}
	return parsed, true
}

// SameMajorMinor reports whether two release versions may talk to each other.
// A patch difference is compatible; an unreleased local build or an
// unreadable value is never rejected.
func SameMajorMinor(client, server string) bool {
	left, leftOK := Parse(client)
	right, rightOK := Parse(server)
	if !leftOK || !rightOK {
		return true
	}
	return left.Major == right.Major && left.Minor == right.Minor
}

// Newer reports whether candidate is a later release than current. An
// unreadable current version (a local "dev" build) is never "behind", so the
// caller must decide whether to offer an update at all.
func Newer(candidate, current string) bool {
	left, leftOK := Parse(candidate)
	right, rightOK := Parse(current)
	if !leftOK || !rightOK {
		return false
	}
	if left.Major != right.Major {
		return left.Major > right.Major
	}
	if left.Minor != right.Minor {
		return left.Minor > right.Minor
	}
	if left.Patch != right.Patch {
		return left.Patch > right.Patch
	}
	// A release is newer than a pre-release of the same number.
	return left.Pre == "" && right.Pre != ""
}
