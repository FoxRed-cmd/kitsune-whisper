// Package humanize formats small values for the Client UI.
package humanize

import "strconv"

// Bytes renders a byte count in decimal units, matching the Server's catalog
// sizes without shipping translations for every unit the way a full unit
// library would. Non-positive values render an em dash.
func Bytes(value int64) string {
	if value <= 0 {
		return "—"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1000 && unit < len(units)-1 {
		size /= 1000
		unit++
	}
	if unit == 0 {
		return strconv.FormatInt(value, 10) + " B"
	}
	return strconv.FormatFloat(size, 'f', 1, 64) + " " + units[unit]
}
