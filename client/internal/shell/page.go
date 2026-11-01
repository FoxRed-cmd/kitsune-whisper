package shell

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
)

// Page renders the Settings window's initial HTML for a status snapshot and a
// settings view. The desktop adapter serves it from the asset server; later
// snapshots arrive through renderStatus, renderSettings, renderMessages, and
// setLevel. The page holds no routing, validation, or retry logic: it collects
// values and sends one JSON intent, and Go decides.
func Page(initial Snapshot, settings SettingsView) string {
	title := settings.Title
	if title == "" {
		title = "kitsune-whisper"
	}
	subtitle := settings.Subtitle
	language := ""
	if settings.Language != "" {
		language = string(settings.Language)
	}
	actionsStyle := ""
	if len(settings.Sections) == 0 {
		// A build without a settings store has nothing to save or restart.
		actionsStyle = ` style="display:none"`
	}
	return strings.NewReplacer(
		"__LANG__", html.EscapeString(language),
		"__TITLE__", html.EscapeString(title),
		"__SUBTITLE__", html.EscapeString(subtitle),
		"__SETTINGS__", SettingsHTML(settings),
		"__SAVE__", html.EscapeString(settings.Actions.Save),
		"__RESTART__", html.EscapeString(settings.Actions.Restart),
		"__RESTART_HINT__", html.EscapeString(settings.Actions.RestartHint),
		"__ACTIONS_STYLE__", actionsStyle,
		"__INITIAL__", jsonForScript(initial),
	).Replace(pageHTML)
}

// SettingsHTML renders the General, Dictation, Servers, and Devices screens
// into an HTML fragment. Every string is escaped; the fragment is inserted
// into the page and also pushed verbatim after a save or a language switch.
func SettingsHTML(view SettingsView) string {
	var out strings.Builder
	for _, section := range view.Sections {
		out.WriteString(`<section class="group" data-section="` + escape(section.ID) + `">`)
		out.WriteString(`<h2>` + escape(section.Title) + `</h2>`)
		for _, field := range section.Fields {
			writeField(&out, field, view.View)
		}
		out.WriteString(`</section>`)
	}
	out.WriteString(serversHTML(view.Servers, view.Labels))
	out.WriteString(serverSettingsHTML(view.Servers, view.Labels))
	out.WriteString(diagnosticsHTML(view.Diagnostics, view.Labels))
	if view.DeviceError != "" {
		out.WriteString(`<p class="device-error">` + escape(view.DeviceError) + `</p>`)
	}
	if view.ConfigPath != "" {
		out.WriteString(`<p class="config-path">` + escape(view.ConfigTitle) + `: <code>` + escape(view.ConfigPath) + `</code></p>`)
	}
	return out.String()
}

// serversHTML renders the Server profiles and the active profile's Devices.
// Inputs have stable ids so the page can collect one pairing request without
// rebuilding any other screen.
func serversHTML(view servers.View, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="servers">`)
	out.WriteString(`<h2>` + escape(labels.Title) + `</h2>`)
	if len(view.Profiles) == 0 {
		out.WriteString(`<p class="help">` + escape(labels.NoServers) + `</p>`)
	} else {
		out.WriteString(`<h3>` + escape(labels.Profiles) + `</h3>`)
		for _, profile := range view.Profiles {
			out.WriteString(`<div class="row" data-profile="` + escape(profile.ID) + `">`)
			out.WriteString(`<strong>` + escape(profile.Name) + `</strong>`)
			if profile.Active {
				out.WriteString(`<span class="badge">` + escape(labels.Active) + `</span>`)
			}
			out.WriteString(`<code>` + escape(profile.URL) + `</code>`)
			out.WriteString(`</div>`)
		}
	}
	out.WriteString(`<h3>` + escape(labels.PairTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(labels.PairHelp) + `</p>`)
	out.WriteString(`<div class="field"><label for="server-address">` + escape(labels.Address) + `</label>`)
	out.WriteString(`<input type="text" id="server-address" autocomplete="off" spellcheck="false" placeholder="desktop.local:8765"></div>`)
	out.WriteString(`<div class="field"><label for="server-code">` + escape(labels.Code) + `</label>`)
	out.WriteString(`<input type="text" id="server-code" autocomplete="off" spellcheck="false" maxlength="16"></div>`)
	out.WriteString(`<div class="field"><label for="server-name">` + escape(labels.Name) + `</label>`)
	out.WriteString(`<input type="text" id="server-name" autocomplete="off" spellcheck="false">`)
	out.WriteString(`<p class="help">` + escape(labels.NameHelp) + `</p></div>`)
	out.WriteString(`<button type="button" class="primary" data-action="pair-remote">` + escape(labels.Pair) + `</button>`)
	out.WriteString(`<h3>` + escape(labels.ManagementTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(labels.ManagementHelp) + `</p>`)
	if view.Lifecycle != nil {
		out.WriteString(`<p class="row"><strong>` + escape(labels.ModelState) + `:</strong> <span data-model-state="` + escape(view.Lifecycle.ModelState) + `">` + escape(view.Lifecycle.ModelState) + `</span></p>`)
	}
	if view.LifecycleError != "" {
		out.WriteString(`<p class="failure">` + escape(view.LifecycleError) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="server-restart">` + escape(labels.RestartServer) + `</button>`)
	out.WriteString(`<button type="button" data-action="server-stop">` + escape(labels.StopServer) + `</button>`)
	out.WriteString(`<button type="button" data-action="server-start">` + escape(labels.StartServer) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="exposure">`)
	out.WriteString(`<h2>` + escape(labels.ExposureTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.ExposureHelp) + `</p>`)
	if view.Exposure != nil {
		state := labels.ExposureOffState
		if view.Exposure.Expose {
			state = labels.ExposureOnState
		}
		out.WriteString(`<p class="notice">` + escape(labels.ExposureTitle) + `: ` + escape(state) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="exposure" data-expose="on" data-confirm="` + escape(labels.ExposureConfirm) + `">` + escape(labels.ExposureOn) + `</button>`)
	out.WriteString(`<button type="button" data-action="exposure" data-expose="off">` + escape(labels.ExposureOff) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="devices">`)
	out.WriteString(`<h2>` + escape(labels.Devices) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.DevicesFor) + `</p>`)
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="refresh-devices">` + escape(labels.Refresh) + `</button>`)
	out.WriteString(`<button type="button" data-action="pairing-code">` + escape(labels.GenerateCode) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`<p class="help">` + escape(labels.CodeHelp) + `</p>`)
	if view.PairingCode != nil {
		out.WriteString(`<p class="pair-code" data-code="` + escape(view.PairingCode.Code) + `"><strong>` + escape(view.PairingCode.Code) + `</strong> · ` + escape(labels.CodeExpires) + `</p>`)
		if view.PairingCode.Fingerprint != "" {
			out.WriteString(`<p class="help">` + escape(labels.Fingerprint) + `: <code>` + escape(view.PairingCode.Fingerprint) + `</code></p>`)
		}
	}
	if view.PairingError != "" {
		out.WriteString(`<p class="failure">` + escape(view.PairingError) + `</p>`)
	}
	if view.DevicesError != "" {
		out.WriteString(`<p class="failure">` + escape(view.DevicesError) + `</p>`)
	}
	if len(view.Devices) == 0 {
		out.WriteString(`<p class="help">` + escape(labels.NoDevices) + `</p>`)
	} else {
		out.WriteString(`<table class="devices"><thead><tr><th>` + escape(labels.Name) + `</th><th>` + escape(labels.LastSeen) + `</th><th></th></tr></thead><tbody>`)
		for _, device := range view.Devices {
			out.WriteString(`<tr data-device="` + escape(device.ID) + `"><td>` + escape(device.Name) + `</td><td>`)
			out.WriteString(escape(lastSeen(device.LastSeen, labels.Never)))
			out.WriteString(`</td><td><button type="button" data-action="revoke-device" data-device-id="` + escape(device.ID) + `" data-confirm="` + escape(labels.RevokeConfirm) + `">` + escape(labels.Revoke) + `</button></td></tr>`)
		}
		out.WriteString(`</tbody></table>`)
	}
	out.WriteString(`</section>`)
	return out.String()
}

// serverSettingsHTML renders the active Server's effective configuration as an
// editable form. Every field carries its absolute “server.*“ path; Go sends
// the collected patch to the Admin API, which validates it and leaves the file
// untouched on failure. Locked fields (environment or command-line overrides)
// are read-only.
func serverSettingsHTML(view servers.View, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="server-settings">`)
	out.WriteString(`<h2>` + escape(labels.SettingsTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.SettingsHelp) + `</p>`)
	if view.ConfigError != "" {
		out.WriteString(`<p class="failure">` + escape(view.ConfigError) + `</p>`)
	}
	if view.Config == nil {
		out.WriteString(`<p class="help">` + escape(labels.Unavailable) + `</p>`)
		out.WriteString(`</section>`)
		return out.String()
	}
	paths := make([]string, 0, len(view.Config.Fields))
	for path := range view.Config.Fields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		field := view.Config.Fields[path]
		id := "server-" + strings.NewReplacer(".", "-", "_", "-").Replace(path)
		out.WriteString(`<div class="field" data-field="` + escape(path) + `">`)
		out.WriteString(`<div class="field-head"><label for="` + escape(id) + `">` + escape(path) + `</label>`)
		switch field.Mode {
		case "restart":
			out.WriteString(`<span class="badge">` + escape(labels.ModeRestart) + `</span>`)
		case "hot":
			out.WriteString(`<span class="badge hot">` + escape(labels.ModeHot) + `</span>`)
		}
		if field.Source != "default" {
			out.WriteString(`<span class="source">` + escape(field.Source) + `</span>`)
		}
		out.WriteString(`</div>`)
		disabled := ""
		if field.Locked {
			disabled = ` disabled`
		}
		value := ""
		if field.Value != nil {
			value = fmt.Sprint(field.Value)
		}
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-server-path="` + escape(path) +
			`" autocomplete="off" spellcheck="false" value="` + escape(value) + `"` + disabled + `>`)
		if field.Locked && field.Reason != "" {
			out.WriteString(`<p class="lock">` + escape(fmt.Sprintf(labels.Locked, field.Reason)) + `</p>`)
		}
		out.WriteString(`</div>`)
	}
	out.WriteString(`<button type="button" class="primary" data-action="save-server">` + escape(labels.SaveSettings) + `</button>`)
	out.WriteString(`</section>`)
	return out.String()
}

// diagnosticsHTML renders versions, the redacted effective configuration, and
// the log tail, plus copy actions for both. The copy block is the one text a
// user pastes into a bug report.
func diagnosticsHTML(view DiagnosticsView, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="diagnostics">`)
	out.WriteString(`<h2>` + escape(labels.DiagnosticsTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.DiagnosticsHelp) + `</p>`)
	out.WriteString(`<dl class="diagnostics-versions">`)
	for _, row := range [][2]string{
		{labels.ClientVersion, view.ClientVersion},
		{labels.ServerVersion, view.ServerVersion},
		{labels.DiagnosticsFor, view.Profile},
		{labels.ServerState, view.ServerState},
		{labels.ModelState, view.ModelState},
		{labels.ConfigPath, view.ConfigPath},
	} {
		out.WriteString(`<dt>` + escape(row[0]) + `</dt><dd>` + escape(valueOr(row[1], labels.Unavailable)) + `</dd>`)
	}
	out.WriteString(`</dl>`)
	if len(view.Redacted) > 0 {
		out.WriteString(`<p class="help">` + escape(fmt.Sprintf(labels.Redacted, strings.Join(view.Redacted, ", "))) + `</p>`)
	}
	if view.Error != "" {
		out.WriteString(`<p class="failure">` + escape(view.Error) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="refresh-diagnostics">` + escape(labels.RefreshDiag) + `</button>`)
	out.WriteString(`<button type="button" data-action="copy-diagnostics">` + escape(labels.CopyDiagnostics) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`<h3>` + escape(labels.ConfigTitle) + `</h3>`)
	if view.ConfigText == "" {
		out.WriteString(`<p class="help">` + escape(labels.Unavailable) + `</p>`)
	} else {
		out.WriteString(`<pre class="diagnostics" id="diagnostics-config">` + escape(view.ConfigText) + `</pre>`)
	}
	out.WriteString(`<h3>` + escape(labels.LogsTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(labels.LogsHelp) + `</p>`)
	if view.LogPath != "" {
		out.WriteString(`<p class="help">` + escape(labels.LogPath) + `: <code>` + escape(view.LogPath) + `</code></p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="load-logs">` + escape(labels.LoadLogs) + `</button>`)
	out.WriteString(`<button type="button" data-action="copy-logs">` + escape(labels.CopyLogs) + `</button>`)
	out.WriteString(`</div>`)
	if view.LogText == "" {
		out.WriteString(`<p class="help">` + escape(labels.NoLogs) + `</p>`)
	} else {
		out.WriteString(`<pre class="diagnostics" id="diagnostics-logs">` + escape(view.LogText) + `</pre>`)
	}
	out.WriteString(`<pre class="diagnostics copy-source" id="diagnostics-copy" hidden>` + escape(view.CopyText) + `</pre>`)
	out.WriteString(`</section>`)
	return out.String()
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func lastSeen(value *float64, never string) string {
	if value == nil {
		return never
	}
	return time.Unix(int64(*value), 0).Local().Format("2006-01-02 15:04")
}

func writeField(out *strings.Builder, field dictation.Field, view dictation.View) {
	id := "field-" + strings.NewReplacer(".", "-", "_", "-").Replace(field.Path)
	out.WriteString(`<div class="field" data-field="` + escape(field.Path) + `">`)
	out.WriteString(`<div class="field-head"><label for="` + escape(id) + `">` + escape(field.Label) + `</label>`)
	if field.Badge != "" {
		out.WriteString(`<span class="badge">` + escape(field.Badge) + `</span>`)
	}
	if field.Source != "default" && field.SourceLabel != "" {
		out.WriteString(`<span class="source">` + escape(field.SourceLabel) + `</span>`)
	}
	out.WriteString(`</div>`)

	disabled := ""
	if field.Locked {
		disabled = ` disabled`
	}
	switch field.Kind {
	case dictation.KindToggle:
		out.WriteString(`<input type="checkbox" id="` + escape(id) + `" data-path="` + escape(field.Path) + `"`)
		if field.Value == "true" {
			out.WriteString(` checked`)
		}
		out.WriteString(disabled + `>`)
	case dictation.KindSelect:
		writeSelect(out, id, field, disabled)
	case dictation.KindMicrophone:
		writeSelect(out, id, field, disabled)
		out.WriteString(`<div class="mic-actions">`)
		out.WriteString(`<button type="button" data-action="test-mic"` + disabled + `>` + escape(view.Actions.Test) + `</button>`)
		out.WriteString(`<button type="button" data-action="stop-mic"` + disabled + `>` + escape(view.Actions.Stop) + `</button>`)
		out.WriteString(`</div>`)
		out.WriteString(`<div class="meter" role="meter" aria-label="` + escape(view.Actions.Level) + `" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"><div class="meter-fill"></div></div>`)
	case dictation.KindHotkey:
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-path="` + escape(field.Path) +
			`" data-hotkey="true" autocomplete="off" spellcheck="false" value="` + escape(field.Value) +
			`" placeholder="` + escape(view.Actions.HotkeyPlaceholder) + `"` + disabled + `>`)
	default:
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-path="` + escape(field.Path) +
			`" autocomplete="off" spellcheck="false" value="` + escape(field.Value) + `"` + disabled + `>`)
	}
	if field.Help != "" {
		out.WriteString(`<p class="help">` + escape(field.Help) + `</p>`)
	}
	if field.Locked {
		out.WriteString(`<p class="lock">` + escape(field.LockReason) + `</p>`)
	}
	out.WriteString(`</div>`)
}

func writeSelect(out *strings.Builder, id string, field dictation.Field, disabled string) {
	out.WriteString(`<select id="` + escape(id) + `" data-path="` + escape(field.Path) + `"` + disabled + `>`)
	for _, option := range field.Options {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == field.Value {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	out.WriteString(`</select>`)
}

func escape(value string) string { return html.EscapeString(value) }

// jsonForScript encodes v as JSON that can sit inside an inline <script>.
// Angle brackets, ampersands, and the JavaScript line separators are escaped
// so an error message cannot close the element or break the statement.
func jsonForScript(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return strings.NewReplacer(
		"<", `\u003c`,
		">", `\u003e`,
		"&", `\u0026`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	).Replace(string(data))
}

const pageHTML = `<!doctype html>
<html lang="__LANG__">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<style>
  :root { color-scheme: light dark; --bg:#f6f6f7; --fg:#18181b; --muted:#6b6b74; --card:#ffffff; --line:#dedee3; --accent:#4c6ef5; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#18181b; --fg:#f4f4f5; --muted:#a1a1aa; --card:#232327; --line:#34343a; --accent:#748ffc; }
  }
  * { box-sizing: border-box; }
  body { margin:0; font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); }
  main { max-width: 720px; margin: 0 auto; padding: 36px 32px 64px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  .sub { color: var(--muted); margin: 0 0 24px; }
  .card, .group { background:var(--card); border:1px solid var(--line); border-radius:12px; padding:20px 22px; margin-bottom:18px; }
  .group h2 { font-size: 16px; margin: 0 0 14px; }
  .state { display:flex; align-items:center; gap:12px; }
  .dot { width:14px; height:14px; border-radius:50%; background:#8a8a8e; box-shadow:0 0 0 4px color-mix(in srgb, #8a8a8e 25%, transparent); }
  .state[data-state="recording"] .dot { background:#e5484d; box-shadow:0 0 0 4px color-mix(in srgb, #e5484d 25%, transparent); }
  .state[data-state="processing"] .dot { background:#f5a524; box-shadow:0 0 0 4px color-mix(in srgb, #f5a524 25%, transparent); }
  .state[data-state="error"] .dot { background:#d6409f; box-shadow:0 0 0 4px color-mix(in srgb, #d6409f 25%, transparent); }
  .state strong { font-size: 18px; }
  dl { display:grid; grid-template-columns: max-content 1fr; gap:8px 20px; margin:18px 0 0; }
  dt { color:var(--muted); }
  dd { margin:0; overflow-wrap:anywhere; }
  .field { padding: 12px 0; border-top: 1px solid var(--line); }
  .field:first-of-type { border-top: none; }
  .field-head { display:flex; align-items:center; gap:8px; flex-wrap:wrap; margin-bottom:6px; }
  .field-head label { font-weight: 600; }
  .field input[type="text"], .field select { width:100%; padding:8px 10px; border:1px solid var(--line); border-radius:8px; background:var(--bg); color:var(--fg); font: inherit; }
  .field input[type="checkbox"] { width:18px; height:18px; }
  .field input[disabled], .field select[disabled], .field button[disabled] { opacity:.6; cursor:not-allowed; }
  .help { color:var(--muted); margin:6px 0 0; font-size: 13px; }
  .lock { color:#c2410c; margin:6px 0 0; font-size: 13px; }
  .badge { font-size: 12px; padding:2px 8px; border-radius:999px; background:#f5a52433; color:#b45309; }
  .badge.hot { background:#2f9e4433; color:#2f9e44; }
  pre.diagnostics { max-height:240px; overflow:auto; background:var(--bg); border:1px solid var(--line); border-radius:8px; padding:10px 12px; font-size:13px; white-space:pre-wrap; overflow-wrap:anywhere; }
  .diagnostics-versions { margin-bottom:12px; }
  .source { font-size: 12px; color:var(--muted); }
  .mic-actions { display:flex; gap:8px; margin-top:8px; }
  .meter { height:10px; border-radius:999px; background:var(--line); overflow:hidden; margin-top:8px; }
  .meter-fill { height:100%; width:0; background:var(--accent); transition:width 60ms linear; }
  .device-error { color:#d6409f; }
  .row { display:flex; align-items:center; gap:10px; flex-wrap:wrap; padding:8px 0; border-top:1px solid var(--line); }
  .row:first-of-type { border-top:none; }
  .row code { color:var(--muted); overflow-wrap:anywhere; }
  .actions-inline { display:flex; gap:8px; flex-wrap:wrap; margin:10px 0; }
  .pair-code { font-size:18px; letter-spacing:.08em; }
  .devices { width:100%; border-collapse:collapse; margin-top:10px; }
  .devices th, .devices td { text-align:left; padding:8px 6px; border-top:1px solid var(--line); }
  .devices th { color:var(--muted); font-weight:600; border-top:none; }
  .devices td:last-child { text-align:right; }
  .messages { margin-bottom:18px; }
  .notice { color:#2f9e44; margin:0 0 6px; }
  .failure { color:#d6409f; margin:0 0 6px; }
  .error { margin-top:20px; padding:12px 14px; border:1px solid #d6409f55; border-radius:8px; color:#d6409f; display:none; }
  .error.visible { display:block; }
  footer { display:flex; gap:10px; align-items:center; margin-top: 20px; }
  button { font: inherit; padding: 8px 14px; border-radius: 8px; border:1px solid var(--line); background: var(--card); color: var(--fg); cursor: pointer; }
  button.primary { background: var(--accent); border-color: var(--accent); color: white; }
  .hint { color: var(--muted); font-size: 13px; margin-left: auto; }
</style>
</head>
<body>
<main>
  <h1 id="page-title">__TITLE__</h1>
  <p class="sub" id="page-subtitle">__SUBTITLE__</p>
  <section class="card">
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">—</strong></div>
    <dl>
      <dt id="label-phase">—</dt><dd id="phase">idle</dd>
      <dt id="label-running">—</dt><dd id="running">—</dd>
      <dt id="label-server">—</dt><dd id="server">—</dd>
      <dt id="label-server-status">—</dt><dd id="server-status">—</dd>
      <dt id="label-version">—</dt><dd id="version">—</dd>
    </dl>
    <p class="error" id="error"></p>
  </section>
  <div class="messages" id="messages"></div>
  <div id="settings">__SETTINGS__</div>
  <footer id="actions"__ACTIONS_STYLE__>
    <button type="button" class="primary" data-action="save" id="save-button">__SAVE__</button>
    <button type="button" data-action="restart" id="restart-button">__RESTART__</button>
    <span class="hint" id="restart-hint">__RESTART_HINT__</span>
  </footer>
</main>
<script>
  function text(id, value) {
    var el = document.getElementById(id);
    if (el) el.textContent = value || "";
  }
  function renderStatus(s) {
    var labels = s.labels || {};
    var state = document.getElementById("state");
    if (state) state.dataset.state = s.state || "idle";
    text("state-label", labels.state);
    text("phase", labels.phaseValue);
    text("running", s.running ? labels.running : labels.stopped);
    text("server", s.profile || "—");
    text("server-status", labels.serverState || "—");
    text("version", s.version || "—");
    text("label-phase", labels.phase);
    text("label-running", labels.client);
    text("label-server", labels.profile);
    text("label-server-status", labels.server);
    text("label-version", labels.version);
    var error = document.getElementById("error");
    if (error) {
      error.textContent = s.error || "";
      error.classList.toggle("visible", Boolean(s.error));
    }
  }
  function renderSettings(v) {
    var host = document.getElementById("settings");
    if (host) host.innerHTML = (v && v.html) || "";
    if (v && v.language) document.documentElement.lang = v.language;
    if (v && v.title) {
      document.title = v.title;
      text("page-title", v.title);
    }
    if (v) text("page-subtitle", v.subtitle);
    if (v && v.actions) {
      var save = document.getElementById("save-button");
      if (save && v.actions.save) save.textContent = v.actions.save;
      var restart = document.getElementById("restart-button");
      if (restart && v.actions.restart) restart.textContent = v.actions.restart;
      var hint = document.getElementById("restart-hint");
      if (hint) hint.textContent = v.actions.restartHint || "";
    }
  }
  function renderMessages(m) {
    var host = document.getElementById("messages");
    if (!host) return;
    host.textContent = "";
    ((m && m.notices) || []).forEach(function (line) {
      var p = document.createElement("p");
      p.className = "notice";
      p.textContent = line;
      host.appendChild(p);
    });
    ((m && m.failures) || []).forEach(function (line) {
      var p = document.createElement("p");
      p.className = "failure";
      p.textContent = line;
      host.appendChild(p);
    });
  }
  function setLevel(level) {
    var fill = document.querySelector("#settings .meter-fill");
    var meter = document.querySelector("#settings .meter[role='meter']");
    var clamped = Math.max(0, Math.min(1, Number(level) || 0));
    if (fill) fill.style.width = (clamped * 100).toFixed(1) + "%";
    if (meter) meter.setAttribute("aria-valuenow", String(Math.round(clamped * 100)));
  }
  function collectPatch() {
    var patch = {};
    var fields = document.querySelectorAll("#settings [data-path]");
    for (var i = 0; i < fields.length; i++) {
      var el = fields[i];
      if (el.disabled) continue;
      var path = el.dataset.path.replace(/^client\./, "");
      var parts = path.split(".");
      var cursor = patch;
      for (var j = 0; j < parts.length - 1; j++) {
        if (!cursor[parts[j]]) cursor[parts[j]] = {};
        cursor = cursor[parts[j]];
      }
      cursor[parts[parts.length - 1]] = el.type === "checkbox" ? el.checked : el.value;
    }
    return patch;
  }
  function collectServerPatch() {
    var patch = {};
    var fields = document.querySelectorAll("#settings [data-server-path]");
    for (var i = 0; i < fields.length; i++) {
      var el = fields[i];
      if (el.disabled) continue;
      var path = el.dataset.serverPath.replace(/^server\./, "");
      var parts = path.split(".");
      var cursor = patch;
      for (var j = 0; j < parts.length - 1; j++) {
        if (!cursor[parts[j]]) cursor[parts[j]] = {};
        cursor = cursor[parts[j]];
      }
      cursor[parts[parts.length - 1]] = el.value;
    }
    return patch;
  }
  function copyText(id) {
    var el = document.getElementById(id);
    if (!el) return;
    var value = el.textContent || "";
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(value).catch(function () {});
      return;
    }
    var area = document.createElement("textarea");
    area.value = value;
    document.body.appendChild(area);
    area.select();
    try { document.execCommand("copy"); } catch (error) {}
    document.body.removeChild(area);
  }
  function sendIntent(intent) {
    fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(intent)
    }).catch(function () {});
  }
  function hotkeySpec(event) {
    var key = event.key;
    var name = null;
    if (key === " ") name = "Space";
    else if (key === "Escape") name = "Esc";
    else if (key === "Enter") name = "Enter";
    else if (key === "Delete") name = "Delete";
    else if (key === "ArrowLeft") name = "Left";
    else if (key === "ArrowRight") name = "Right";
    else if (key === "ArrowUp") name = "Up";
    else if (key === "ArrowDown") name = "Down";
    else if (key === "Tab") name = "Tab";
    else if (/^[a-zA-Z]$/.test(key)) name = key.toUpperCase();
    else if (/^[0-9]$/.test(key)) name = key;
    else if (/^F([1-9]|1[0-9]|20)$/.test(key)) name = key;
    if (!name) return null;
    var parts = [];
    if (event.ctrlKey) parts.push("Ctrl");
    if (event.altKey) parts.push("Alt");
    if (event.shiftKey) parts.push("Shift");
    if (event.metaKey) parts.push("Super");
    parts.push(name);
    return parts.join("+");
  }
  function pairRemote() {
    function value(id) {
      var el = document.getElementById(id);
      return el ? el.value : "";
    }
    sendIntent({
      action: "pair-remote",
      address: value("server-address"),
      code: value("server-code"),
      name: value("server-name")
    });
  }
  function revokeDevice(button) {
    var message = button.dataset.confirm || "Revoke this Device?";
    if (window.confirm && !window.confirm(message)) return;
    sendIntent({ action: "revoke-device", device_id: button.dataset.deviceId || "" });
  }
  document.addEventListener("click", function (event) {
    var button = event.target.closest ? event.target.closest("[data-action]") : null;
    if (!button) return;
    var action = button.dataset.action;
    if (action === "save") sendIntent({ action: "save", patch: collectPatch() });
    else if (action === "restart") sendIntent({ action: "restart" });
    else if (action === "test-mic") {
      var input = document.querySelector("#settings [data-path='client.audio.device']");
      sendIntent({ action: "test-mic", device: input ? input.value : "" });
    } else if (action === "stop-mic") {
      setLevel(0);
      sendIntent({ action: "stop-mic" });
    } else if (action === "pair-remote") {
      pairRemote();
    } else if (action === "refresh-devices") {
      sendIntent({ action: "refresh-devices" });
    } else if (action === "revoke-device") {
      revokeDevice(button);
    } else if (action === "pairing-code") {
      sendIntent({ action: "pairing-code" });
    } else if (action === "exposure") {
      var expose = button.dataset.expose || "off";
      var exposureMessage = button.dataset.confirm || "";
      if (expose === "on" && exposureMessage && window.confirm && !window.confirm(exposureMessage)) return;
      sendIntent({ action: "exposure", value: expose });
    } else if (action === "save-server") {
      sendIntent({ action: "save-server", patch: collectServerPatch() });
    } else if (action === "refresh-diagnostics") {
      sendIntent({ action: "refresh-diagnostics" });
    } else if (action === "load-logs") {
      sendIntent({ action: "load-logs" });
    } else if (action === "copy-diagnostics") {
      copyText("diagnostics-copy");
    } else if (action === "copy-logs") {
      copyText("diagnostics-logs");
    } else if (action === "server-restart") {
      sendIntent({ action: "server-restart" });
    } else if (action === "server-stop") {
      sendIntent({ action: "server-stop" });
    } else if (action === "server-start") {
      sendIntent({ action: "server-start" });
    }
  });
  document.addEventListener("change", function (event) {
    var el = event.target.closest ? event.target.closest("[data-path]") : null;
    if (!el) return;
    if (el.dataset.path === "client.ui_language") {
      sendIntent({ action: "save", patch: { ui_language: el.value } });
    }
  });
  document.addEventListener("keydown", function (event) {
    var el = event.target.closest ? event.target.closest("[data-hotkey]") : null;
    if (!el) return;
    if (event.key === "Tab") return;
    event.preventDefault();
    var spec = hotkeySpec(event);
    if (!spec) return;
    el.value = spec;
    sendIntent({ action: "validate", path: el.dataset.path, value: spec });
  });
  renderStatus(__INITIAL__);
</script>
</body>
</html>
`
