package shell

// pageIcon renders one decorative icon into the Settings page. The icons are
// inline SVG so the Client ships no image assets: they inherit the theme's
// colour through currentColor and are hidden from assistive technology because
// every icon sits next to a visible text label.
func pageIcon(name string) string {
	inner, ok := pageIconPaths[name]
	if !ok {
		return ""
	}
	return `<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">` + inner + `</svg>`
}

// navIcon maps a navigation entry to its icon.
func navIcon(key string) string {
	switch key {
	case "general":
		return "sliders"
	case "appearance":
		return "monitor"
	case "dictation":
		return "mic"
	case "server":
		return "server"
	case "models":
		return "models"
	case "spool":
		return "spool"
	case "diagnostics":
		return "activity"
	}
	return ""
}

// sectionIcon maps a Settings section id to the icon in its heading.
func sectionIcon(id string) string {
	switch id {
	case "general":
		return pageIcon("sliders")
	case "appearance":
		return pageIcon("monitor")
	case "dictation":
		return pageIcon("mic")
	case "dictation-hotkeys":
		return pageIcon("keyboard")
	case "dictation-microphone":
		return pageIcon("mic")
	case "dictation-language":
		return pageIcon("globe")
	case "dictation-feedback":
		return pageIcon("bell")
	case "dictation-paste":
		return pageIcon("paste")
	case "dictation-storage":
		return pageIcon("spool")
	}
	return ""
}

// pageIconPaths holds the stroked geometry of each icon on a 24×24 grid.
var pageIconPaths = map[string]string{
	"sliders": `<path d="M4 6h9"/><circle cx="16" cy="6" r="2.2"/><path d="M18.2 6H20"/>` +
		`<path d="M4 12h3"/><circle cx="10" cy="12" r="2.2"/><path d="M12.2 12H20"/>` +
		`<path d="M4 18h9"/><circle cx="16" cy="18" r="2.2"/><path d="M18.2 18H20"/>`,
	"monitor": `<rect x="3" y="4.5" width="18" height="13" rx="2"/>` +
		`<path d="M9 21h6"/><path d="M12 17.5V21"/>` +
		`<rect x="8" y="12" width="8" height="2.4" rx="1.2"/>`,
	"mic": `<rect x="9" y="3" width="6" height="10.5" rx="3"/>` +
		`<path d="M5.5 11.5a6.5 6.5 0 0 0 13 0"/><path d="M12 18v3"/><path d="M9 21h6"/>`,
	"server": `<rect x="3.5" y="4" width="17" height="6.5" rx="2"/>` +
		`<rect x="3.5" y="13.5" width="17" height="6.5" rx="2"/>` +
		`<path d="M7 7.25h.01"/><path d="M7 16.75h.01"/>`,
	"models": `<path d="M12 3.2l7.6 4.2v9.2L12 20.8l-7.6-4.2V7.4z"/>` +
		`<path d="M12 12l7.6-4.4"/><path d="M12 12L4.4 7.6"/><path d="M12 12v8.6"/>`,
	"gear": `<circle cx="12" cy="12" r="3.1"/>` +
		`<path d="M12 2.8v2.4M12 18.8v2.4M21.2 12h-2.4M5.2 12H2.8M18.5 5.5l-1.7 1.7M7.2 16.8l-1.7 1.7M18.5 18.5l-1.7-1.7M7.2 7.2L5.5 5.5"/>`,
	"spool": `<path d="M3.5 12.5h4.2l1.8 2.6h5l1.8-2.6h4.2"/>` +
		`<path d="M5.6 4.8h12.8L21 12.2v4.6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4.6z"/>`,
	"activity": `<path d="M3 12h3.6l2.7-6.4L14 18.4l2.6-6.4H21"/>`,
	"wand": `<path d="M5 19l9.4-9.4"/><path d="M14.4 9.6l2.3-2.3"/>` +
		`<path d="M17.5 3.2l.9 2.2 2.2.9-2.2.9-.9 2.2-.9-2.2-2.2-.9 2.2-.9z"/>` +
		`<path d="M6.4 4.6l.6 1.4 1.4.6-1.4.6-.6 1.4-.6-1.4L4.4 6.6l1.4-.6z"/>`,
	"keyboard": `<rect x="2.5" y="6" width="19" height="12" rx="2.2"/>` +
		`<path d="M6 10h.01M9.5 10h.01M13 10h.01M16.5 10h.01M6 13.5h.01M9.5 13.5h.01M13 13.5h.01M16.5 13.5h.01"/>` +
		`<path d="M8 16.5h8"/>`,
	"globe": `<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17"/>` +
		`<path d="M12 3.5c2.3 2.4 3.4 5.3 3.4 8.5s-1.1 6.1-3.4 8.5c-2.3-2.4-3.4-5.3-3.4-8.5s1.1-6.1 3.4-8.5z"/>`,
	"bell": `<path d="M6.5 9.5a5.5 5.5 0 0 1 11 0c0 4 1.5 5.5 1.5 5.5H5s1.5-1.5 1.5-5.5z"/>` +
		`<path d="M10 18.5a2.3 2.3 0 0 0 4 0"/>`,
	"paste": `<rect x="5.5" y="5" width="13" height="15" rx="2"/>` +
		`<path d="M9 5V3.8h6V5"/><path d="M9 10h6"/><path d="M9 14h4"/>`,
	"plus": `<path d="M12 5.5v13"/><path d="M5.5 12h13"/>`,
	"trash": `<path d="M4.5 7h15"/><path d="M9.5 7V5.2A1.2 1.2 0 0 1 10.7 4h2.6a1.2 1.2 0 0 1 1.2 1.2V7"/>` +
		`<path d="M6.5 7l.9 12.1a1.6 1.6 0 0 0 1.6 1.4h6a1.6 1.6 0 0 0 1.6-1.4L17.5 7"/>` +
		`<path d="M10.2 11v5.4M13.8 11v5.4"/>`,
	"check":   `<path d="M5 12.5l4.3 4.3L19 6.8"/>`,
	"restart": `<path d="M20 12a8 8 0 1 1-2.6-5.9"/><path d="M20 3.8v4.6h-4.6"/>`,
	"refresh": `<path d="M4 12a8 8 0 0 1 13.7-5.6L20 8.6"/><path d="M20 4v4.6h-4.6"/>` +
		`<path d="M20 12a8 8 0 0 1-13.7 5.6L4 15.4"/><path d="M4 20v-4.6h4.6"/>`,
	"stop":     `<rect x="6.5" y="6.5" width="11" height="11" rx="2"/>`,
	"play":     `<path d="M8.5 5.5l9 6.5-9 6.5z"/>`,
	"download": `<path d="M12 4v10.5"/><path d="M7.5 10.5L12 15l4.5-4.5"/><path d="M5 19.5h14"/>`,
	"power":    `<path d="M12 3.5v8"/><path d="M7.2 6.4a7.5 7.5 0 1 0 9.6 0"/>`,
}
