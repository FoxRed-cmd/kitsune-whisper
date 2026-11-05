// Package components reports which Components the installer placed on this
// machine and which Autostart modes their presence allows.
//
// A Component is either the Client or the Local Server. The Client is derived
// from the installed binary; the Local Server from its bundle manifest and the
// supervisor registration that lets the Client start it on demand. Detection
// lives here so the Settings screen, autostart, and the wizard never probe the
// filesystem or a supervisor themselves.
package components

// Set is the set of Components installed on this machine. The zero value means
// no Components are installed.
type Set struct {
	// Client reports whether the Client binary is present.
	Client bool `json:"client"`
	// Server reports whether the Local Server bundle and its supervisor
	// registration are present.
	Server bool `json:"server"`
}

// Any reports whether at least one Component is installed.
func (s Set) Any() bool { return s.Client || s.Server }

// Mode is one Autostart mode: the Components allowed to start at login.
// Off is the absence of a mode and is not a combination of Components.
type Mode string

const (
	// ModeOff is autostart switched off: nothing starts at login.
	ModeOff Mode = "off"
	// ModeClientAndServer starts the Client, which starts the Local Server.
	ModeClientAndServer Mode = "client-server"
	// ModeClientOnly starts the Client but not the Local Server.
	ModeClientOnly Mode = "client"
	// ModeServerOnly starts the Local Server without a Client here.
	ModeServerOnly Mode = "server"
)

// Valid reports whether m is one of the three autostart modes.
func (m Mode) Valid() bool {
	switch m {
	case ModeClientAndServer, ModeClientOnly, ModeServerOnly:
		return true
	default:
		return false
	}
}

// Set returns the Components m starts. Off starts none.
func (m Mode) Set() Set {
	switch m {
	case ModeClientAndServer:
		return Set{Client: true, Server: true}
	case ModeClientOnly:
		return Set{Client: true}
	case ModeServerOnly:
		return Set{Server: true}
	default:
		return Set{}
	}
}

// Allows reports whether m can be offered on this installation: every
// Component it starts is installed. Off is never an offered mode; it is what
// the switch shows when no registration is enabled.
func (s Set) Allows(m Mode) bool {
	started := m.Set()
	if !started.Any() {
		return false
	}
	return (!started.Client || s.Client) && (!started.Server || s.Server)
}

// Modes lists the autostart modes this installation offers, fullest first.
// Client and Server comes before the single-Component modes, so the first
// entry is the default when both Components are installed.
func (s Set) Modes() []Mode {
	order := []Mode{ModeClientAndServer, ModeClientOnly, ModeServerOnly}
	modes := make([]Mode, 0, len(order))
	for _, mode := range order {
		if s.Allows(mode) {
			modes = append(modes, mode)
		}
	}
	return modes
}

// DefaultMode is the mode the selector starts on: the fullest combination this
// installation supports. It is ModeOff when no Component is installed.
func (s Set) DefaultMode() Mode {
	modes := s.Modes()
	if len(modes) == 0 {
		return ModeOff
	}
	return modes[0]
}

// State is the autostart state the Settings screen renders. Mode is the active
// mode when Enabled; while autostart is off it is the default mode, so turning
// the switch on has an explicit, installed target.
type State struct {
	// Installed is the set of Components present on this machine.
	Installed Set `json:"installed"`
	// Supported reports whether this platform has a login supervisor the
	// Client can change.
	Supported bool `json:"supported"`
	// Enabled reports whether any registration is enabled at login.
	Enabled bool `json:"enabled"`
	// Mode is the active mode, or the default while autostart is off.
	Mode Mode `json:"mode"`
}
