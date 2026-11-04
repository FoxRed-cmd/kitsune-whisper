package cli

import (
	"context"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// errLocalServerUnavailable reports that a Local Server is installed but the
// Client cannot drive a supervisor for it.
var errLocalServerUnavailable = servers.ErrLocalUnavailable

// maybeStartLocalServer starts a stopped Local Server when one is installed and
// the current Autostart mode permits it. Everything except Client only permits
// the Server, including the default mode used while Autostart is off.
//
// The attempt is best-effort: the caller logs a returned error and keeps
// opening the window, where the offline Server and the failure explain the
// state. A Server that already answers its health endpoint is left alone.
func maybeStartLocalServer(
	ctx context.Context,
	state components.State,
	local servers.LocalServer,
	reachable func(context.Context) bool,
) error {
	if !state.Installed.Server || state.Mode == components.ModeClientOnly {
		return nil
	}
	if reachable != nil && reachable(ctx) {
		return nil
	}
	if local == nil || !local.Available() {
		return errLocalServerUnavailable
	}
	return local.Start()
}

// localServerReachable probes the Local Server profile once, so launch does not
// ask the supervisor to start a Server that is already answering. It returns
// nil when the Client has no Local profile; the supervisor start is idempotent
// in that case.
func localServerReachable(cfg config.ClientConfig) func(context.Context) bool {
	profile, ok := launchLocalProfile(cfg)
	if !ok {
		return nil
	}
	return func(ctx context.Context) bool {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		// The health endpoint is unauthenticated, so an unpaired profile still
		// reports that the process is listening.
		status := trust.CheckServer(probeCtx, profile, "", 3*time.Second)
		return status.State == trust.StateOnline || status.State == trust.StateUnauthenticated
	}
}

// launchLocalProfile finds the Local Server profile, preferring the stable
// local id and falling back to an active loopback profile.
func launchLocalProfile(cfg config.ClientConfig) (config.ServerProfile, bool) {
	for _, profile := range cfg.Servers {
		if profile.ID == config.LocalServerID {
			return profile, true
		}
	}
	if active, ok := cfg.ActiveProfile(); ok && trust.IsLoopbackURL(active.URL) {
		return active, true
	}
	return config.ServerProfile{}, false
}
