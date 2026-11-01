package shell

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
)

// DiagnosticsView is the diagnostics block: versions, the redacted effective
// Server configuration, the log tail, and one copyable text block. Secrets are
// masked again here, so a Server bug cannot turn the copy action into a leak.
type DiagnosticsView struct {
	ClientVersion string   `json:"client_version"`
	ServerVersion string   `json:"server_version,omitempty"`
	Profile       string   `json:"profile,omitempty"`
	ServerState   string   `json:"server_state,omitempty"`
	ModelState    string   `json:"model_state,omitempty"`
	ConfigPath    string   `json:"config_path,omitempty"`
	ConfigText    string   `json:"config_text,omitempty"`
	Redacted      []string `json:"redacted,omitempty"`
	LogPath       string   `json:"log_path,omitempty"`
	LogText       string   `json:"log_text,omitempty"`
	Truncated     bool     `json:"truncated,omitempty"`
	CopyText      string   `json:"copy_text"`
	Error         string   `json:"error,omitempty"`
}

// diagnosticsView builds the diagnostics block from the cached Admin API
// results. It performs no network I/O; RefreshDiagnostics loads them first.
func (s *Shell) diagnosticsView(serverView servers.View) DiagnosticsView {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()

	view := DiagnosticsView{
		ClientVersion: s.opts.Version,
		ServerVersion: server.Version,
		ServerState:   serverStateLabel(s.catalog(), server.State),
		Profile:       activeProfileName(serverView, s.opts.Profile),
	}
	if serverView.Lifecycle != nil {
		view.ModelState = modelStateLabel(s.catalog(), serverView.Lifecycle.ModelState)
	}
	if serverView.Config != nil {
		view.ConfigPath = serverView.Config.Path
		view.ConfigText = formatConfig(serverView.Config.Config)
		view.Redacted = append([]string(nil), serverView.Config.Redacted...)
	}
	if serverView.Logs != nil {
		view.LogPath = serverView.Logs.Path
		view.LogText = strings.Join(serverView.Logs.Lines, "\n")
		view.Truncated = serverView.Logs.Truncated
	}
	view.Error = firstNonEmpty(serverView.ConfigError, serverView.LogsError, serverView.LifecycleError)
	view.CopyText = diagnosticsText(view)
	return view
}

// activeProfileName prefers the active profile's display name over the
// startup fallback.
func activeProfileName(view servers.View, fallback string) string {
	for _, profile := range view.Profiles {
		if profile.Active {
			return profile.Name
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// diagnosticsText renders the one block the copy action puts on the clipboard.
func diagnosticsText(view DiagnosticsView) string {
	var out strings.Builder
	out.WriteString("kitsune-whisper diagnostics\n")
	writeLine(&out, "client version", view.ClientVersion)
	writeLine(&out, "server profile", view.Profile)
	writeLine(&out, "server version", view.ServerVersion)
	writeLine(&out, "server state", view.ServerState)
	writeLine(&out, "model state", view.ModelState)
	writeLine(&out, "config path", view.ConfigPath)
	if len(view.Redacted) > 0 {
		writeLine(&out, "redacted", strings.Join(view.Redacted, ", "))
	}
	if view.Error != "" {
		writeLine(&out, "error", view.Error)
	}
	out.WriteString("\nserver configuration:\n")
	if view.ConfigText == "" {
		out.WriteString("(not loaded)\n")
	} else {
		out.WriteString(view.ConfigText)
		out.WriteString("\n")
	}
	out.WriteString("\nserver log")
	if view.LogPath != "" {
		out.WriteString(" (" + view.LogPath + ")")
	}
	out.WriteString(":\n")
	if view.Truncated {
		out.WriteString("older lines omitted\n")
	}
	if view.LogText == "" {
		out.WriteString("(not loaded)\n")
	} else {
		out.WriteString(view.LogText)
		out.WriteString("\n")
	}
	return out.String()
}

func writeLine(out *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	out.WriteString(label + ": " + value + "\n")
}

// formatConfig renders the nested Server configuration as sorted
// “server.path: value“ lines. Any key that names a credential is masked even
// if the Server did not redact it.
func formatConfig(values map[string]any) string {
	lines := configLines("server", values)
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func configLines(prefix string, values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		path := prefix + "." + key
		child := values[key]
		if nested, ok := child.(map[string]any); ok {
			lines = append(lines, configLines(path, nested)...)
			continue
		}
		lines = append(lines, path+": "+formatConfigValue(key, child))
	}
	return lines
}

func formatConfigValue(key string, value any) string {
	if sensitiveKey(key) {
		return "***"
	}
	if value == nil {
		return "(unset)"
	}
	switch value.(type) {
	case map[string]any, []any:
		encoded, err := json.Marshal(value)
		if err == nil {
			return string(encoded)
		}
	}
	return fmt.Sprint(value)
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	for _, marker := range []string{"api_key", "token", "secret", "password"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
