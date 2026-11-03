package servers

import (
	"net"
	"net/url"
	"strconv"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// connectAddress combines this computer's LAN address with the Server's port.
// A blank port or address means the address cannot be determined.
func connectAddress(lanAddress func() string, port string) string {
	if port == "" {
		return ""
	}
	host := lanAddress()
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, port)
}

// connectPortLocked is the port a LAN Client should use, or "" when there is
// no address to show. Only a Server running on this computer has a known
// address: for a Remote Server the Client's own LAN address would point at the
// wrong machine. s.mu must be held.
func (s *Service) connectPortLocked(profiles []Profile, activeID string) string {
	var active *Profile
	for index := range profiles {
		if profiles[index].ID == activeID {
			active = &profiles[index]
			break
		}
	}
	if active == nil || !trust.IsLoopbackURL(active.URL) {
		return ""
	}
	network := s.healthForLocked(activeID).Network
	// A saved switch applies only to the profile it was made on, and only
	// until the Server reports its live listener.
	pending := s.exposureFor == activeID && s.exposure != nil && s.exposure.Expose
	if !pending && (network == nil || !network.ExposeToNetwork) {
		return ""
	}
	if network != nil && network.Port > 0 {
		return strconv.Itoa(network.Port)
	}
	parsed, err := url.Parse(active.URL)
	if err != nil {
		return ""
	}
	return parsed.Port()
}
