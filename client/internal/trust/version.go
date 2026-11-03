package trust

import (
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/version"
)

// VersionMismatchError reports a Client and Server release that cannot talk to
// each other. The message names the side to upgrade instead of leaving the
// user with an authentication failure.
type VersionMismatchError struct {
	Client string
	Server string
}

func (e *VersionMismatchError) Error() string {
	_, clientOK := version.Parse(e.Client)
	_, serverOK := version.Parse(e.Server)
	if !clientOK || !serverOK {
		return fmt.Sprintf(
			"the Client (%s) and Server (%s) versions are incompatible; upgrade both",
			e.Client,
			e.Server,
		)
	}
	if version.Newer(e.Server, e.Client) {
		return fmt.Sprintf(
			"the Server is %s but this Client is %s; upgrade the Client",
			e.Server,
			e.Client,
		)
	}
	return fmt.Sprintf(
		"the Client is %s but the Server is %s; upgrade the Server",
		e.Client,
		e.Server,
	)
}

// CheckVersion reports an incompatible Client/Server release. A patch difference
// is compatible, and a local build or an unreadable version disables the check.
func CheckVersion(client, server string) error {
	if version.SameMajorMinor(client, server) {
		return nil
	}
	return &VersionMismatchError{Client: client, Server: server}
}

// ApplyVersion turns a reachable Server's reported version into a distinct
// upgrade state. A Server that did not report a version (an older release)
// keeps whatever state the probe already found.
func ApplyVersion(status Status, clientVersion string) Status {
	if status.Version == "" || clientVersion == "" {
		return status
	}
	if err := CheckVersion(clientVersion, status.Version); err != nil {
		status.State = StateVersionMismatch
		status.Error = err.Error()
	}
	return status
}
