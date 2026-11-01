package shell

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
)

// DiagnosticsView is the diagnostics block: versions, the redacted effective
// Server configuration, the log tail, and one copyable text block. Secrets are
// masked again here, so a Server bug cannot turn the copy action into a leak.
type DiagnosticsView struct {
	ClientVersion     string   `json:"client_version"`
	ServerVersion     string   `json:"server_version,omitempty"`
	Profile           string   `json:"profile,omitempty"`
	ServerState       string   `json:"server_state,omitempty"`
	ModelName         string   `json:"model,omitempty"`
	ModelState        string   `json:"model_state,omitempty"`
	ModelPath         string   `json:"model_path,omitempty"`
	IdleUnloadSeconds int      `json:"idle_unload_seconds,omitempty"`
	ConfigPath        string   `json:"config_path,omitempty"`
	ConfigText        string   `json:"config_text,omitempty"`
	Redacted          []string `json:"redacted,omitempty"`
	LogPath           string   `json:"log_path,omitempty"`
	LogText           string   `json:"log_text,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	CopyText          string   `json:"copy_text"`
	Error             string   `json:"error,omitempty"`
}

// diagnosticsView builds the diagnostics block from the cached Admin API
// results. It performs no network I/O; RefreshDiagnostics loads them first.
func (s *Shell) diagnosticsView(serverView servers.View) DiagnosticsView {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()

	view := DiagnosticsView{
		ClientVersion:     s.opts.Version,
		ServerVersion:     server.Version,
		ServerState:       serverStateLabel(s.catalog(), server.State),
		Profile:           activeProfileName(serverView, s.opts.Profile),
		ModelName:         server.Model,
		ModelPath:         server.ModelPath,
		IdleUnloadSeconds: server.IdleUnloadSeconds,
	}
	// Prefer the live /status model state over the cached lifecycle result.
	if server.ModelState != "" {
		view.ModelState = modelLifecycleLabel(s.catalog(), server.ModelState)
	} else if serverView.Lifecycle != nil {
		view.ModelState = modelLifecycleLabel(s.catalog(), serverView.Lifecycle.ModelState)
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

// copyDiagnostics puts the generated diagnostics block on the clipboard through
// the runtime, so the page holds no clipboard logic and the copy uses the same
// seam as a Spool copy.
func (s *Shell) copyDiagnostics() {
	view := s.diagnosticsView(s.serversView())
	if strings.TrimSpace(view.CopyText) == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.copyDiagnostics")})
		return
	}
	if err := s.opts.Runtime.Copy(view.CopyText); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.copyDiagnostics", err.Error())})
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.server.diagnosticsCopied")}, nil)
}

// copyLogs puts the loaded log tail on the clipboard through the runtime.
func (s *Shell) copyLogs() {
	view := s.diagnosticsView(s.serversView())
	if strings.TrimSpace(view.LogText) == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noLogs")})
		return
	}
	if err := s.opts.Runtime.Copy(view.LogText); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.copyLogs", err.Error())})
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.server.logsCopied")}, nil)
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
// It stays in English on purpose: the block is a bug-report artifact pasted
// into an issue tracker, not a localized screen. All UI labels around it are
// translated.
func diagnosticsText(view DiagnosticsView) string {
	var out strings.Builder
	out.WriteString("kitsune-whisper diagnostics\n")
	writeLine(&out, "client version", view.ClientVersion)
	writeLine(&out, "server profile", view.Profile)
	writeLine(&out, "server version", view.ServerVersion)
	writeLine(&out, "server state", view.ServerState)
	writeLine(&out, "model", view.ModelName)
	writeLine(&out, "model state", view.ModelState)
	writeLine(&out, "model path", view.ModelPath)
	if view.IdleUnloadSeconds > 0 {
		writeLine(&out, "idle unload seconds", strconv.Itoa(view.IdleUnloadSeconds))
	}
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
