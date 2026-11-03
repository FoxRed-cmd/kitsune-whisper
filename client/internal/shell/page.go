package shell

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/humanize"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
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
	if len(settings.Sections) == 0 || (settings.Wizard.Enabled && settings.Wizard.Active) {
		// A build without a settings store has nothing to save, and the wizard
		// has its own Back/Next footer.
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
		"__OPEN_SETTINGS__", html.EscapeString(initial.Labels.OpenSettings),
		"__ACTIONS_STYLE__", actionsStyle,
		"__INITIAL__", jsonForScript(initial),
	).Replace(pageHTML)
}

// navEntry is one Settings navigation item: a translated label and the section
// ids it shows in the content pane. A section may be listed under one entry
// only, so the pane never shows the same form twice.
type navEntry struct {
	Key     string
	Label   string
	Targets []string
}

// navEntries lists the Settings screens in navigation order. An entry is
// omitted when the build does not render its content, so the menu never offers
// an empty screen.
func navEntries(view SettingsView) []navEntry {
	var entries []navEntry
	for _, section := range view.Sections {
		switch section.ID {
		case "general", "appearance", "dictation":
			if section.Title != "" {
				entries = append(entries, navEntry{Key: section.ID, Label: section.Title, Targets: []string{section.ID}})
			}
		}
	}
	if view.Startup.Available {
		entries = append(entries, navEntry{
			Key:     "startup",
			Label:   view.Labels.StartupTitle,
			Targets: []string{"startup"},
		})
	}
	if view.Update.Title != "" {
		entries = append(entries, navEntry{
			Key:     "updates",
			Label:   view.Update.Title,
			Targets: []string{"updates"},
		})
	}
	if view.Labels.Title != "" {
		entries = append(entries, navEntry{
			Key:     "servers",
			Label:   view.Labels.Title,
			Targets: []string{"servers", "devices", "exposure"},
		})
	}
	if view.ModelLabels.Title != "" {
		entries = append(entries, navEntry{Key: "models", Label: view.ModelLabels.Title, Targets: []string{"models"}})
	}
	if view.Labels.SettingsTitle != "" {
		entries = append(entries, navEntry{
			Key:     "server",
			Label:   view.Labels.SettingsTitle,
			Targets: []string{"server-settings"},
		})
	}
	if view.Spool.Labels.Title != "" {
		entries = append(entries, navEntry{Key: "spool", Label: view.Spool.Labels.Title, Targets: []string{"spool"}})
	}
	if view.Labels.DiagnosticsTitle != "" {
		entries = append(entries, navEntry{
			Key:     "diagnostics",
			Label:   view.Labels.DiagnosticsTitle,
			Targets: []string{"diagnostics"},
		})
	}
	if view.Wizard.Enabled && !view.Wizard.Active && view.Wizard.Labels.ReopenTitle != "" {
		entries = append(entries, navEntry{
			Key:     "wizard",
			Label:   view.Wizard.Labels.ReopenTitle,
			Targets: []string{"wizard-launcher"},
		})
	}
	return entries
}

// SettingsHTML renders the Settings screens into a navigation menu plus the
// content pane. Every string is escaped; the fragment is inserted into the page
// and also pushed verbatim after a save or a language switch. The page shows
// one entry at a time and scrolls the menu and the pane independently.
func SettingsHTML(view SettingsView) string {
	if view.Wizard.Enabled && view.Wizard.Active {
		return `<div class="settings-layout settings-single"><div class="settings-pane" id="settings-pane">` +
			wizardHTML(view.Wizard) + `</div></div>`
	}
	var out strings.Builder
	for _, section := range view.Sections {
		out.WriteString(`<section class="group" data-section="` + escape(section.ID) + `">`)
		out.WriteString(`<h2>` + sectionIcon(section.ID) + escape(section.Title) + `</h2>`)
		// GNOME's system keybindings belong to the hotkeys they bind: the offer
		// sits directly under the last hotkey field of the Dictation screen, not
		// on a screen of its own or at the bottom of the pane.
		gnomeAfter := -1
		if section.ID == "dictation" && view.Gnome.Visible {
			for index, field := range section.Fields {
				if field.Kind == dictation.KindHotkey {
					gnomeAfter = index
				}
			}
		}
		if gnomeAfter < 0 && section.ID == "dictation" && view.Gnome.Visible {
			out.WriteString(gnomeShortcutsHTML(view.Gnome))
		}
		for index, field := range section.Fields {
			writeField(&out, field, view.View)
			if index == gnomeAfter {
				out.WriteString(gnomeShortcutsHTML(view.Gnome))
			}
		}
		out.WriteString(`</section>`)
	}
	out.WriteString(serversHTML(view.Servers, view.Labels, view.ModelLabels, view.ModelState))
	out.WriteString(spoolHTML(view.Spool))
	out.WriteString(startupHTML(view.Startup, view.Labels))
	out.WriteString(updatesHTML(view.Update))
	out.WriteString(serverSettingsHTML(view.Servers, view.Labels))
	out.WriteString(diagnosticsHTML(view.Diagnostics, view.Labels))
	if view.DeviceError != "" {
		out.WriteString(`<p class="device-error">` + escape(view.DeviceError) + `</p>`)
	}
	if view.ConfigPath != "" {
		out.WriteString(`<p class="config-path">` + escape(view.ConfigTitle) + `: <code>` + escape(view.ConfigPath) + `</code></p>`)
	}
	out.WriteString(wizardLauncherHTML(view.Wizard))

	entries := navEntries(view)
	var nav strings.Builder
	nav.WriteString(`<nav class="settings-nav" id="settings-nav" aria-label="` + escape(view.Title) + `">`)
	for index, entry := range entries {
		active := ""
		current := "false"
		if index == 0 {
			active = " is-active"
			current = "page"
		}
		nav.WriteString(`<button type="button" class="nav-item` + active + `" data-nav="` + escape(entry.Key) +
			`" data-targets="` + escape(strings.Join(entry.Targets, ",")) + `" aria-current="` + current + `">` +
			pageIcon(navIcon(entry.Key)) + escape(entry.Label) + `</button>`)
	}
	nav.WriteString(`</nav>`)
	return `<div class="settings-layout">` + nav.String() +
		`<div class="settings-pane" id="settings-pane">` + out.String() + `</div></div>`
}

// gnomeShortcutsHTML renders the GNOME custom-keybinding offer directly under
// the Dictation hotkeys, where the user changes the keys it binds.
func gnomeShortcutsHTML(view GnomeShortcutsView) string {
	var out strings.Builder
	out.WriteString(`<div class="gnome-offer">`)
	out.WriteString(`<div class="gnome-head">` + pageIcon("keyboard") + `<h3>` + escape(view.Title) + `</h3></div>`)
	out.WriteString(`<p class="help">` + escape(view.Help) + `</p>`)
	if view.Error != "" {
		out.WriteString(`<p class="failure">` + escape(view.Error) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	if view.Add != "" {
		out.WriteString(`<button type="button" data-action="gnome-shortcuts-add">` + pageIcon("plus") + escape(view.Add) + `</button>`)
	}
	if view.Installed && view.Remove != "" {
		out.WriteString(`<button type="button" data-action="gnome-shortcuts-remove">` + pageIcon("trash") + escape(view.Remove) + `</button>`)
	}
	out.WriteString(`</div></div>`)
	return out.String()
}

// serversHTML renders the Server profiles and the active profile's Devices.
// Inputs have stable ids so the page can collect one pairing request without
// rebuilding any other screen.
func serversHTML(view servers.View, labels ServerLabels, modelLabels ModelLabels, modelState string) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="servers">`)
	out.WriteString(`<h2>` + pageIcon("server") + escape(labels.Title) + `</h2>`)
	if len(view.Profiles) == 0 {
		out.WriteString(`<p class="help">` + escape(labels.NoServers) + `</p>`)
	} else {
		out.WriteString(`<h3>` + escape(labels.Profiles) + `</h3>`)
		out.WriteString(`<div class="actions-inline">`)
		out.WriteString(`<button type="button" data-action="refresh-servers">` + escape(labels.RefreshHealth) + `</button>`)
		out.WriteString(`</div>`)
		for _, profile := range view.Profiles {
			out.WriteString(`<div class="row" data-profile="` + escape(profile.ID) + `">`)
			out.WriteString(`<strong>` + escape(profile.Name) + `</strong>`)
			if profile.Active {
				out.WriteString(`<span class="badge">` + escape(labels.Active) + `</span>`)
			}
			healthKey := string(profile.Health.State)
			if profile.Health.State == trust.StateOnline && serverSoftStopped(profile.Health) {
				healthKey = "stopped"
			}
			health := labels.StateLabels[healthKey]
			if health == "" {
				health = labels.StateLabels[string(trust.StateUnknown)]
			}
			out.WriteString(`<span class="health-label">` + escape(labels.Health) + `:</span>`)
			out.WriteString(`<span class="health" data-health="` + escape(healthKey) + `">` + escape(health) + `</span>`)
			out.WriteString(`<code>` + escape(profile.URL) + `</code>`)
			if profile.Health.Error != "" && profile.Health.State != trust.StateOnline {
				out.WriteString(`<p class="failure">` + escape(profile.Health.Error) + `</p>`)
			}
			if !profile.Active {
				out.WriteString(`<button type="button" data-action="use-profile" data-profile-id="` +
					escape(profile.ID) + `">` + escape(labels.Use) + `</button>`)
			}
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
		out.WriteString(`<p class="row"><strong>` + escape(labels.ModelState) + `:</strong> <span data-model-state="` + escape(view.Lifecycle.ModelState) + `">` + escape(valueOr(modelState, view.Lifecycle.ModelState)) + `</span></p>`)
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
	live := activeNetwork(view)
	if live != nil {
		state := labels.ExposureLiveOff
		exposed := "off"
		if live.ExposeToNetwork {
			state = labels.ExposureLiveOn
			exposed = "on"
		}
		out.WriteString(`<p class="notice" data-exposure-live="` + exposed + `">` + escape(state) + `</p>`)
		address := fmt.Sprintf("%s:%d", live.BindHost, live.Port)
		out.WriteString(`<p class="help" data-exposure-listen="` + escape(address) + `">` +
			escape(fmt.Sprintf(labels.ExposureListen, address)) + `</p>`)
	}
	if view.ConnectAddress != "" {
		out.WriteString(`<div class="row" data-exposure-connect="` + escape(view.ConnectAddress) + `">`)
		out.WriteString(`<span class="help">` +
			escape(fmt.Sprintf(labels.ExposureConnect, view.ConnectAddress)) + `</span>`)
		out.WriteString(`<button type="button" data-action="copy-address">` + escape(labels.CopyAddress) + `</button>`)
		out.WriteString(`</div>`)
	}
	if live == nil {
		if view.Exposure != nil {
			state := labels.ExposureOffState
			if view.Exposure.Expose {
				state = labels.ExposureOnState
			}
			out.WriteString(`<p class="notice">` + escape(labels.ExposureTitle) + `: ` + escape(state) + `</p>`)
		} else if configured, ok := configuredExposure(view.Config); ok {
			state := labels.ExposureOffState
			configuredState := "off"
			if configured {
				state = labels.ExposureOnState
				configuredState = "on"
			}
			out.WriteString(`<p class="notice" data-exposure-configured="` + configuredState + `">` +
				escape(labels.ExposureTitle) + `: ` + escape(state) + `</p>`)
		}
	}
	if pendingExposure(view, live) {
		out.WriteString(`<p class="notice pending" data-exposure-pending="true">` + escape(labels.ExposurePending) + `</p>`)
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
		localServer := view.LocalServerActive()
		out.WriteString(`<table class="devices"><thead><tr><th>` + escape(labels.Name) + `</th><th>` + escape(labels.LastSeen) + `</th><th></th></tr></thead><tbody>`)
		for _, device := range view.Devices {
			out.WriteString(`<tr data-device="` + escape(device.ID) + `"><td>` + escape(device.Name) + `</td><td>`)
			out.WriteString(escape(lastSeen(device.LastSeen, labels.Never)))
			out.WriteString(`</td><td>`)
			if localServer && device.Current {
				out.WriteString(`<span class="badge active">` + escape(labels.ThisDevice) + `</span>`)
			} else {
				out.WriteString(`<button type="button" data-action="revoke-device" data-device-id="` + escape(device.ID) + `" data-confirm="` + escape(labels.RevokeConfirm) + `">` + escape(labels.Revoke) + `</button>`)
			}
			out.WriteString(`</td></tr>`)
		}
		out.WriteString(`</tbody></table>`)
	}
	out.WriteString(`</section>`)
	out.WriteString(modelsHTML(view.Models, view.ModelsError, modelLabels))
	return out.String()
}

// activeNetwork returns the listener the active Server reports from its last
// health check. nil means the Server is unreachable or predates the field.
func activeNetwork(view servers.View) *trust.NetworkStatus {
	for _, profile := range view.Profiles {
		if profile.ID == view.ActiveID {
			return profile.Health.Network
		}
	}
	return nil
}

// configuredExposure reads the LAN switch from the Server configuration the
// Diagnostics screen already loaded.
func configuredExposure(config *servers.ServerConfig) (bool, bool) {
	if config == nil {
		return false, false
	}
	value, ok := config.Config["expose_to_network"].(bool)
	return value, ok
}

// pendingExposure reports a saved LAN switch the running Server has not
// applied yet, so the page can say a restart is required.
func pendingExposure(view servers.View, live *trust.NetworkStatus) bool {
	if live == nil {
		return false
	}
	if view.Exposure != nil && len(view.Exposure.RestartRequired) > 0 {
		return true
	}
	if view.Exposure != nil {
		return view.Exposure.Expose != live.ExposeToNetwork
	}
	configured, ok := configuredExposure(view.Config)
	return ok && configured != live.ExposeToNetwork
}

// SpoolHTML renders only the Spool screen, for a live refresh that leaves the
// edited Dictation forms alone.
func SpoolHTML(view SpoolView) string { return spoolHTML(view) }

// spoolHTML renders the saved failed Utterances and the retry result. Every
// action carries the entry id; the page never decides what to do with it.
func spoolHTML(view SpoolView) string {
	if view.Labels.Title == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="spool">`)
	out.WriteString(`<h2>` + pageIcon("spool") + escape(view.Labels.Title) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.Help) + `</p>`)
	if view.Error != "" {
		out.WriteString(`<p class="failure">` + escape(view.Error) + `</p>`)
	}
	if len(view.Entries) == 0 {
		out.WriteString(`<p class="help">` + escape(view.Labels.Empty) + `</p>`)
		out.WriteString(`</section>`)
		return out.String()
	}
	out.WriteString(`<table class="devices"><thead><tr><th>` + escape(view.Labels.SavedAt) +
		`</th><th>` + escape(view.Labels.Duration) + `</th><th></th></tr></thead><tbody>`)
	for _, entry := range view.Entries {
		out.WriteString(`<tr data-entry="` + escape(entry.ID) + `"><td>`)
		out.WriteString(escape(savedAt(entry.SavedAt)))
		out.WriteString(`</td><td>` + escape(formatSpoolDuration(entry.Duration)) + `</td><td>`)
		out.WriteString(`<button type="button" data-action="spool-retry" data-entry-id="` +
			escape(entry.ID) + `">` + escape(view.Labels.Retry) + `</button> `)
		out.WriteString(`<button type="button" data-action="spool-delete" data-entry-id="` +
			escape(entry.ID) + `" data-confirm="` + escape(view.Labels.DeleteConfirm) + `">` +
			escape(view.Labels.Delete) + `</button>`)
		out.WriteString(`</td></tr>`)
	}
	out.WriteString(`</tbody></table>`)
	if view.Selected != "" {
		out.WriteString(`<h3>` + escape(view.Labels.Text) + `</h3>`)
		if view.Text == "" {
			out.WriteString(`<p class="help">` + escape(view.Labels.EmptyText) + `</p>`)
		} else {
			out.WriteString(`<pre class="spool-text" data-entry-id="` + escape(view.Selected) + `">` +
				escape(view.Text) + `</pre>`)
			out.WriteString(`<div class="actions-inline">`)
			out.WriteString(`<button type="button" data-action="spool-copy" data-entry-id="` +
				escape(view.Selected) + `">` + escape(view.Labels.Copy) + `</button>`)
			out.WriteString(`<button type="button" data-action="spool-insert" data-entry-id="` +
				escape(view.Selected) + `">` + escape(view.Labels.Insert) + `</button>`)
			out.WriteString(`</div>`)
		}
	}
	out.WriteString(`</section>`)
	return out.String()
}

// modelsHTML renders the Model catalog, download progress, activation and
// deletion actions, the idle timeout, and the local CTranslate2 directory.
func modelsHTML(view *servers.ModelsView, modelsError string, labels ModelLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="models">`)
	out.WriteString(`<h2>` + pageIcon("models") + escape(labels.Title) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.Help) + `</p>`)
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="load-models">` + escape(labels.Refresh) + `</button>`)
	out.WriteString(`</div>`)
	if view == nil {
		if modelsError != "" {
			out.WriteString(`<p class="failure">` + escape(modelsError) + `</p>`)
		}
		out.WriteString(`<p class="help">` + escape(labels.NoCatalog) + `</p>`)
		out.WriteString(`</section>`)
		return out.String()
	}
	if modelsError != "" {
		out.WriteString(`<p class="failure">` + escape(modelsError) + `</p>`)
	}
	out.WriteString(`<p class="model-state">` + escape(labels.State) + `: <strong>` + escape(modelStateLabel(labels, view.State)) + `</strong>`)
	if view.ActiveModel != "" {
		out.WriteString(` · ` + escape(labels.Active) + `: <code>` + escape(view.ActiveModel) + `</code>`)
	}
	if view.ActivePath != "" {
		out.WriteString(` (<code>` + escape(view.ActivePath) + `</code>)`)
	}
	if view.Error != "" {
		out.WriteString(` · <span class="failure">` + escape(view.Error) + `</span>`)
	}
	out.WriteString(`</p>`)
	if view.Recommended != "" && labels.RecommendedDevice != "" {
		out.WriteString(`<p class="help">` + escape(labels.RecommendedDevice) + `: <strong>` + escape(view.Recommended) + `</strong></p>`)
	}
	if len(view.Models) == 0 {
		out.WriteString(`<p class="help">` + escape(labels.NoCatalog) + `</p>`)
	} else {
		out.WriteString(`<table class="models"><thead><tr><th>` + escape(labels.Name) + `</th><th>` + escape(labels.Size) + `</th><th>` + escape(labels.Memory) + `</th><th>` + escape(labels.Speed) + `</th><th>` + escape(labels.Quality) + `</th><th></th></tr></thead><tbody>`)
		for _, model := range view.Models {
			out.WriteString(`<tr data-model="` + escape(model.Name) + `"><td><strong>` + escape(model.Name) + `</strong>`)
			if model.Recommended {
				out.WriteString(` <span class="badge">` + escape(labels.Recommended) + `</span>`)
			}
			if model.Installed {
				out.WriteString(` <span class="badge">` + escape(labels.Installed) + `</span>`)
			}
			if model.Name == view.ActiveModel {
				out.WriteString(` <span class="badge active">` + escape(labels.Active) + `</span>`)
			}
			out.WriteString(`</td><td>` + escape(humanize.Bytes(model.DiskBytes)) + `</td>`)
			out.WriteString(`<td>` + escape(humanize.Bytes(model.MemoryBytes)) + `</td>`)
			out.WriteString(`<td>` + escape(speedLabel(labels, model.Speed)) + `</td>`)
			out.WriteString(`<td>` + escape(qualityLabel(labels, model.Quality)) + `</td><td class="model-actions">`)
			out.WriteString(modelActions(model, view, labels))
			out.WriteString(`</td></tr>`)
			if model.Download != nil {
				out.WriteString(`<tr class="model-progress" data-model-progress="` + escape(model.Name) + `"><td colspan="6">` + escape(downloadLabel(labels, model.Download)) + `</td></tr>`)
			}
		}
		out.WriteString(`</tbody></table>`)
	}
	out.WriteString(`<h3>` + escape(labels.IdleTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(labels.IdleHelp) + `</p>`)
	out.WriteString(`<div class="field"><div class="field-head"><label for="model-idle-seconds">` + escape(labels.IdleLabel) + `</label></div>`)
	out.WriteString(`<div class="inline-field">`)
	out.WriteString(`<input type="number" min="0" step="30" id="model-idle-seconds" value="` + escape(strconv.Itoa(view.IdleUnloadSeconds)) + `">`)
	out.WriteString(`<button type="button" data-action="idle-timeout">` + escape(labels.IdleSave) + `</button>`)
	out.WriteString(`</div></div>`)
	out.WriteString(`<h3>` + escape(labels.AdvancedTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(labels.AdvancedHelp) + `</p>`)
	out.WriteString(`<div class="field"><label for="model-local-path">` + escape(labels.LocalPath) + `</label>`)
	out.WriteString(`<input type="text" id="model-local-path" autocomplete="off" spellcheck="false">`)
	out.WriteString(`<p class="help">` + escape(labels.LocalPathHint) + `</p></div>`)
	out.WriteString(`<button type="button" data-action="activate-local">` + escape(labels.UseLocal) + `</button>`)
	out.WriteString(`</section>`)
	return out.String()
}

// savedAt renders a spool entry's capture time in local time.
func savedAt(seconds float64) string {
	if seconds <= 0 {
		return "—"
	}
	return time.Unix(int64(seconds), 0).Local().Format("2006-01-02 15:04")
}

// modelActions renders the one control a catalog row offers.
func modelActions(model servers.Model, view *servers.ModelsView, labels ModelLabels) string {
	if model.Download.InProgress() {
		return `<button type="button" data-action="cancel-download" data-model="` + escape(model.Name) + `">` + escape(labels.Cancel) + `</button>`
	}
	if !model.Installed {
		return `<button type="button" data-action="download-model" data-model="` + escape(model.Name) + `">` + escape(labels.Download) + `</button>`
	}
	if model.Name == view.ActiveModel {
		return `<span class="badge active">` + escape(labels.Active) + `</span>`
	}
	var out strings.Builder
	out.WriteString(`<button type="button" data-action="activate-model" data-model="` + escape(model.Name) + `">` + escape(labels.Activate) + `</button>`)
	out.WriteString(` <button type="button" data-action="delete-model" data-model="` + escape(model.Name) + `" data-confirm="` + escape(labels.DeleteConfirm) + `">` + escape(labels.Delete) + `</button>`)
	return out.String()
}

func idleSecondsLabel(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return strconv.Itoa(seconds) + " s"
}

func modelStateLabel(labels ModelLabels, state string) string {
	switch state {
	case "loaded":
		return labels.StateLoaded
	case "cold":
		return labels.StateCold
	case "loading":
		return labels.StateLoading
	case "failed":
		return labels.StateFailed
	case "stopped":
		return labels.StateStopped
	}
	return state
}

func speedLabel(labels ModelLabels, speed string) string {
	switch speed {
	case "fast":
		return labels.SpeedFast
	case "balanced":
		return labels.SpeedBalanced
	case "slow":
		return labels.SpeedSlow
	}
	return speed
}

func qualityLabel(labels ModelLabels, quality string) string {
	switch quality {
	case "basic":
		return labels.QualityBasic
	case "good":
		return labels.QualityGood
	case "excellent":
		return labels.QualityExcellent
	}
	return quality
}

func downloadLabel(labels ModelLabels, download *servers.ModelDownload) string {
	state := download.State
	switch state {
	case "queued":
		return labels.Queued
	case "downloading":
		if download.TotalBytes > 0 {
			return labels.Downloading + " " + humanize.Bytes(download.ReceivedBytes) +
				" / " + humanize.Bytes(download.TotalBytes)
		}
		return labels.Downloading + " " + humanize.Bytes(download.ReceivedBytes)
	case "verifying":
		return labels.Verifying
	case "done":
		return labels.Done
	case "cancelled":
		return labels.Cancelled
	case "failed":
		if download.Error != "" {
			return labels.Failed + ": " + download.Error
		}
		return labels.Failed
	}
	return state
}

// startupHTML renders the login autostart switches for the Client and the
// local Server. The switches are plain toggles: Go enables or disables the
// platform registration and re-renders the section.
func startupHTML(view StartupView, labels ServerLabels) string {
	if !view.Available {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="startup">`)
	out.WriteString(`<h2>` + pageIcon("power") + escape(labels.StartupTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.StartupHelp) + `</p>`)
	writeStartupToggle(&out, "client", labels.StartupClient, labels.StartupClientHelp, view.Client, true)
	help := labels.StartupServerHelp
	if !view.ServerAvailable {
		help = labels.StartupServerUnavailable
	}
	writeStartupToggle(&out, "server", labels.StartupServer, help, view.Server, view.ServerAvailable)
	out.WriteString(`</section>`)
	return out.String()
}

// updatesHTML renders the release check and the verified installer action.
func updatesHTML(view UpdateView) string {
	if view.Title == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="updates">`)
	out.WriteString(`<h2>` + pageIcon("download") + escape(view.Title) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(view.Help) + `</p>`)
	out.WriteString(`<dl class="diagnostics-versions">`)
	out.WriteString(`<div><dt>` + escape(view.Current) + `</dt><dd>` + escape(view.CurrentVersion) + `</dd></div>`)
	if view.LatestVersion != "" {
		out.WriteString(`<div><dt>` + escape(view.Latest) + `</dt><dd>` + escape(view.LatestVersion) + `</dd></div>`)
	}
	out.WriteString(`</dl>`)
	switch {
	case view.Busy && view.Installing:
		out.WriteString(`<p class="notice" data-update-busy="true">` + escape(view.Downloading) + `</p>`)
	case view.Busy:
		out.WriteString(`<p class="notice" data-update-busy="true">` + escape(view.Checking) + `</p>`)
	case view.Available && view.Installable:
		out.WriteString(`<p class="notice">` + escape(view.AvailableMessage) + `</p>`)
	case view.Available:
		out.WriteString(`<p class="notice">` + escape(view.AvailableMessage) + `</p>`)
		out.WriteString(`<p class="help">` + escape(view.NotInstalledMessage) + `</p>`)
	case view.Checked && view.CurrentVersion == "dev":
		out.WriteString(`<p class="help">` + escape(view.DevBuildMessage) + `</p>`)
	case view.Checked:
		out.WriteString(`<p class="help">` + escape(view.UpToDateMessage) + `</p>`)
	}
	if view.Error != "" {
		out.WriteString(`<p class="failure">` + escape(view.Error) + `</p>`)
	}
	if view.Launched {
		out.WriteString(`<p class="notice">` + escape(view.LaunchedMessage) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="check-updates">` + pageIcon("download") + escape(view.Check) + `</button>`)
	if view.Available && view.Installable && !view.Busy && !view.Launched {
		out.WriteString(`<button type="button" class="primary" data-action="install-update">` + pageIcon("download") + escape(view.Download) + `</button>`)
	}
	out.WriteString(`</div>`)
	if view.ReleaseURL != "" {
		out.WriteString(`<p class="help"><a href="` + escape(view.ReleaseURL) + `" target="_blank" rel="noreferrer">` +
			escape(view.ReleasePage) + `</a></p>`)
	}
	out.WriteString(`</section>`)
	return out.String()
}

func writeStartupToggle(out *strings.Builder, component, label, help string, enabled, available bool) {
	out.WriteString(`<div class="field field-toggle" data-startup="` + escape(component) + `">`)
	out.WriteString(`<label class="toggle-row" for="startup-` + escape(component) + `">`)
	out.WriteString(`<span class="toggle-label">` + escape(label) + `</span>`)
	input := `<input type="checkbox" class="switch" id="startup-` + escape(component) +
		`" data-startup-component="` + escape(component) + `"`
	if enabled {
		input += ` checked`
	}
	if !available {
		input += ` disabled`
	}
	out.WriteString(input + `>`)
	out.WriteString(`</label>`)
	out.WriteString(`<p class="help">` + escape(help) + `</p>`)
	out.WriteString(`</div>`)
}

// serverSettingsHTML renders the active Server's effective configuration as an
// editable form. Every field carries its absolute “server.*“ path; Go sends
// the collected patch to the Admin API, which validates it and leaves the file
// untouched on failure. Locked fields (environment or command-line overrides)
// are read-only.
func serverSettingsHTML(view servers.View, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group server-settings" data-section="server-settings">`)
	out.WriteString(`<h2>` + pageIcon("gear") + escape(labels.SettingsTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.SettingsHelp) + `</p>`)
	if view.ConfigError != "" {
		out.WriteString(`<p class="failure">` + escape(view.ConfigError) + `</p>`)
	}
	if view.Config == nil {
		out.WriteString(`<p class="help">` + escape(labels.Unavailable) + `</p>`)
		out.WriteString(`</section>`)
		return out.String()
	}
	out.WriteString(`<div class="server-fields">`)
	visible := make(map[string]bool, len(view.Config.Fields))
	for path := range view.Config.Fields {
		if !hiddenServerField(path) {
			visible[path] = true
		}
	}
	drawn := make(map[string]bool, len(visible))
	drawGroup := func(title string, paths []string) {
		present := false
		for _, path := range paths {
			if visible[path] {
				present = true
				break
			}
		}
		if !present {
			return
		}
		out.WriteString(`<h3>` + escape(title) + `</h3>`)
		for _, path := range paths {
			if !visible[path] || drawn[path] {
				continue
			}
			writeServerSetting(&out, path, view.Config.Fields[path], labels)
			drawn[path] = true
		}
	}
	for _, group := range labels.SettingsGroups {
		drawGroup(group.Title, group.Fields)
	}
	leftovers := make([]string, 0, len(visible))
	for path := range visible {
		if !drawn[path] {
			leftovers = append(leftovers, path)
		}
	}
	sort.Strings(leftovers)
	if len(leftovers) > 0 {
		drawGroup(valueOr(labels.OtherTitle, labels.SettingsTitle), leftovers)
	}
	out.WriteString(`</div>`)
	out.WriteString(`<div class="server-save"><button type="button" class="primary" data-action="save-server">` + escape(labels.SaveSettings) + `</button></div>`)
	out.WriteString(`</section>`)
	return out.String()
}

// hiddenServerField reports settings the Server settings form must not offer.
// Text post-processing is intentionally out of scope for now, and the
// exposure/anonymous switches have their own confirmation flow on the
// Servers screen.
func hiddenServerField(path string) bool {
	if strings.HasPrefix(path, "server.processing.") {
		return true
	}
	switch path {
	case "server.expose_to_network", "server.allow_anonymous", "server.log_level":
		return true
	}
	return false
}

// writeServerSetting renders one Server setting with its human name and help.
// The absolute path stays on the control so a save can send the exact field.
func writeServerSetting(out *strings.Builder, path string, field servers.ConfigField, labels ServerLabels) {
	id := "server-" + strings.NewReplacer(".", "-", "_", "-").Replace(path)
	info := labels.FieldLabels[path]
	label := valueOr(info.Label, path)
	out.WriteString(`<div class="field" data-field="` + escape(path) + `">`)
	out.WriteString(`<div class="field-head"><label for="` + escape(id) + `">` + escape(label) + `</label>`)
	switch field.Mode {
	case "restart":
		out.WriteString(`<span class="badge">` + escape(labels.ModeRestart) + `</span>`)
	case "hot":
		out.WriteString(`<span class="badge hot">` + escape(labels.ModeHot) + `</span>`)
	}
	if field.Source != "default" {
		out.WriteString(`<span class="source">` + escape(labels.Source) + `: ` + escape(field.Source) + `</span>`)
	}
	out.WriteString(`</div>`)

	value := ""
	if field.Value != nil {
		value = fmt.Sprint(field.Value)
	}
	disabled := ""
	if field.Locked {
		disabled = ` disabled`
	}
	if !scalarValue(field.Value) {
		encoded, _ := json.Marshal(field.Value)
		out.WriteString(`<pre class="diagnostics" data-server-advanced="` + escape(path) + `">` + escape(string(encoded)) + `</pre>`)
		if info.Help != "" {
			out.WriteString(`<p class="help">` + escape(info.Help) + `</p>`)
		}
		out.WriteString(`<p class="help">` + escape(labels.AdvancedValue) + `</p>`)
	} else if _, ok := field.Value.(bool); ok {
		writeServerBool(out, id, path, value, disabled, labels)
	} else if options, ok := labels.Choices[path]; ok {
		writeServerChoice(out, id, path, value, disabled, options)
	} else if path == "server.decode.language" {
		writeServerLanguages(out, id, path, value, disabled, labels)
	} else if _, ok := field.Value.(float64); ok {
		step := "1"
		if path == "server.max_upload_mb" || path == "server.decode.temperature" {
			step = "any"
		}
		out.WriteString(`<input type="number" min="0" step="` + step + `" id="` + escape(id) + `" data-server-path="` + escape(path) +
			`" data-default="` + escape(value) + `" autocomplete="off" value="` + escape(value) + `"` + disabled + `>`)
	} else {
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-server-path="` + escape(path) +
			`" data-default="` + escape(value) + `" autocomplete="off" spellcheck="false" value="` + escape(value) + `"` + disabled + `>`)
	}
	if info.Help != "" {
		out.WriteString(`<p class="help">` + escape(info.Help) + `</p>`)
	}
	if field.Locked && field.Reason != "" {
		out.WriteString(`<p class="lock">` + escape(fmt.Sprintf(labels.Locked, field.Reason)) + `</p>`)
	}
	out.WriteString(`</div>`)
}

func writeServerBool(out *strings.Builder, id, path, value, disabled string, labels ServerLabels) {
	out.WriteString(`<select id="` + escape(id) + `" data-server-path="` + escape(path) +
		`" data-default="` + escape(value) + `"` + disabled + `>`)
	for _, option := range [][2]string{{"true", valueOr(labels.BoolOn, "On")}, {"false", valueOr(labels.BoolOff, "Off")}} {
		out.WriteString(`<option value="` + option[0] + `"`)
		if strings.EqualFold(value, option[0]) {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option[1]) + `</option>`)
	}
	out.WriteString(`</select>`)
}

// writeServerChoice renders an enumerated Server setting. A value outside the
// known list keeps an option of its own so saving another setting cannot
// silently rewrite it.
func writeServerChoice(out *strings.Builder, id, path, value, disabled string, options []dictation.Option) {
	known := false
	out.WriteString(`<select id="` + escape(id) + `" data-server-path="` + escape(path) +
		`" data-default="` + escape(value) + `"` + disabled + `>`)
	for _, option := range options {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == value {
			out.WriteString(` selected`)
			known = true
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	if !known && value != "" {
		out.WriteString(`<option value="` + escape(value) + `" selected>` + escape(value) + `</option>`)
	}
	out.WriteString(`</select>`)
}

func writeServerLanguages(out *strings.Builder, id, path, value, disabled string, labels ServerLabels) {
	out.WriteString(`<select id="` + escape(id) + `" data-server-path="` + escape(path) +
		`" data-default="` + escape(value) + `"` + disabled + `>`)
	for _, option := range labels.LanguageOptions {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == value {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	out.WriteString(`</select>`)
}

// diagnosticsHTML renders versions, the redacted effective configuration, and
// the log tail, plus copy actions for both. The copy block is the one text a
// user pastes into a bug report.
func diagnosticsHTML(view DiagnosticsView, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="diagnostics">`)
	out.WriteString(`<h2>` + pageIcon("activity") + escape(labels.DiagnosticsTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.DiagnosticsHelp) + `</p>`)
	out.WriteString(`<dl class="diagnostics-versions">`)
	idle := idleSecondsLabel(view.IdleUnloadSeconds)
	if view.IdleUnloadSeconds == 0 {
		idle = labels.Never
	}
	for _, row := range [][2]string{
		{labels.ClientVersion, view.ClientVersion},
		{labels.ServerVersion, view.ServerVersion},
		{labels.DiagnosticsFor, view.Profile},
		{labels.ServerState, view.ServerState},
		{labels.ModelName, view.ModelName},
		{valueOr(labels.DiagnosticsModel, labels.ModelState), view.ModelState},
		{labels.ModelPath, view.ModelPath},
		{labels.IdleUnload, idle},
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
	out.WriteString(`<div class="field"><div class="field-head"><label for="diagnostics-log-level">` + escape(labels.LogLevelTitle) + `</label></div>`)
	out.WriteString(`<select id="diagnostics-log-level" data-log-level="true">`)
	for _, option := range labels.Choices["server.log_level"] {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == view.LogLevel {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	out.WriteString(`</select><p class="help">` + escape(labels.LogLevelHelp) + `</p></div>`)
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
	if view.Truncated {
		out.WriteString(`<p class="help">` + escape(labels.LogTruncated) + `</p>`)
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
	out.WriteString(`<h3>` + escape(labels.ClientLogsTitle) + `</h3>`)
	if view.ClientLogPath != "" {
		out.WriteString(`<p class="help">` + escape(labels.ClientLogPath) + `: <code>` + escape(view.ClientLogPath) + `</code></p>`)
	}
	if view.ClientLogTruncated {
		out.WriteString(`<p class="help">` + escape(labels.LogTruncated) + `</p>`)
	}
	out.WriteString(`<div class="actions-inline"><button type="button" data-action="copy-client-logs">` + escape(labels.CopyClientLogs) + `</button></div>`)
	if view.ClientLogText == "" {
		out.WriteString(`<p class="help">` + escape(labels.NoLogs) + `</p>`)
	} else {
		out.WriteString(`<pre class="diagnostics" id="diagnostics-client-logs">` + escape(view.ClientLogText) + `</pre>`)
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

// scalarValue reports whether the Settings form can edit a value as one text
// input. Nested objects and lists are rendered read-only so saving another
// field cannot send them back as an invalid string.
func scalarValue(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// lastSeen renders a Device's last use in local time.
func lastSeen(value *float64, never string) string {
	if value == nil {
		return never
	}
	return time.Unix(int64(*value), 0).Local().Format("2006-01-02 15:04")
}

func writeField(out *strings.Builder, field dictation.Field, view dictation.View) {
	id := "field-" + strings.NewReplacer(".", "-", "_", "-").Replace(field.Path)
	disabled := ""
	if field.Locked {
		disabled = ` disabled`
	}
	// A toggle is one row: the label and its badges on the left, the switch on
	// the right. A bare checkbox under a separate heading reads as an orphaned
	// square and does not say what it switches.
	if field.Kind == dictation.KindToggle {
		out.WriteString(`<div class="field field-toggle" data-field="` + escape(field.Path) + `">`)
		out.WriteString(`<label class="toggle-row" for="` + escape(id) + `">`)
		out.WriteString(`<span class="toggle-label">` + escape(field.Label))
		if field.Badge != "" {
			out.WriteString(`<span class="badge">` + escape(field.Badge) + `</span>`)
		}
		if field.Source != "default" && field.SourceLabel != "" {
			out.WriteString(`<span class="source">` + escape(field.SourceLabel) + `</span>`)
		}
		out.WriteString(`</span>`)
		out.WriteString(`<input type="checkbox" id="` + escape(id) + `" class="switch" data-path="` + escape(field.Path) + `"`)
		if field.Value == "true" {
			out.WriteString(` checked`)
		}
		out.WriteString(disabled + `>`)
		out.WriteString(`</label>`)
		writeFieldNotes(out, field)
		out.WriteString(`</div>`)
		return
	}

	preview := ""
	if field.Preview {
		preview = ` data-indicator-preview="true"`
	}
	out.WriteString(`<div class="field" data-field="` + escape(field.Path) + `">`)
	out.WriteString(`<div class="field-head"><label for="` + escape(id) + `">` + escape(field.Label) + `</label>`)
	if field.Badge != "" {
		out.WriteString(`<span class="badge">` + escape(field.Badge) + `</span>`)
	}
	if field.Source != "default" && field.SourceLabel != "" {
		out.WriteString(`<span class="source">` + escape(field.SourceLabel) + `</span>`)
	}
	out.WriteString(`</div>`)

	switch field.Kind {
	case dictation.KindSelect:
		writeSelect(out, id, field, disabled)
	case dictation.KindPosition:
		writePosition(out, id, field, disabled, preview)
	case dictation.KindNumber:
		out.WriteString(`<input type="number" step="1" min="-4096" max="4096" id="` + escape(id) + `" data-path="` + escape(field.Path) +
			`"` + preview + ` autocomplete="off" value="` + escape(field.Value) + `"` + disabled + `>`)
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
	writeFieldNotes(out, field)
	out.WriteString(`</div>`)
}

// writePosition renders the six screen presets as a small screen map. The
// buttons drive a hidden input so the generic form collector still saves the
// value; clicking one previews the real pill immediately.
func writePosition(out *strings.Builder, id string, field dictation.Field, disabled, preview string) {
	out.WriteString(`<div class="position-grid" role="group" aria-label="` + escape(field.Label) + `">`)
	for _, option := range field.Options {
		active := ""
		pressed := "false"
		if option.Value == field.Value {
			active = " is-active"
			pressed = "true"
		}
		out.WriteString(`<button type="button" class="position-preset` + active +
			`" data-indicator-anchor="` + escape(option.Value) + `" aria-pressed="` + pressed +
			`" aria-label="` + escape(option.Label) + `" title="` + escape(option.Label) + `"` + disabled + `></button>`)
	}
	out.WriteString(`</div>`)
	out.WriteString(`<input type="hidden" id="` + escape(id) + `" data-path="` + escape(field.Path) +
		`"` + preview + ` value="` + escape(field.Value) + `"` + disabled + `>`)
}

// writeFieldNotes renders the help and lock text shared by every field shape.
func writeFieldNotes(out *strings.Builder, field dictation.Field) {
	if field.Help != "" {
		out.WriteString(`<p class="help">` + escape(field.Help) + `</p>`)
	}
	if field.Locked {
		out.WriteString(`<p class="lock">` + escape(field.LockReason) + `</p>`)
	}
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
  :root {
    color-scheme: light dark;
    --bg:#f2f4f7; --bg-soft:#e9ecf2; --card:#ffffff; --card-2:#f7f8fb;
    --fg:#1b1f27; --muted:#6a7382; --line:#dce1e9; --line-soft:#eaedf2;
    --accent:#4d6bf5; --accent-strong:#3a57e8; --accent-soft:#4d6bf514; --on-accent:#ffffff;
    --ok:#2f9e44; --warn:#b45309; --danger:#d6409f; --radius:9px;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg:#0e1116; --bg-soft:#151a23; --card:#141922; --card-2:#1a202b;
      --fg:#e7eaf1; --muted:#94a0b3; --line:#252c38; --line-soft:#1d232d;
      --accent:#8fa8ff; --accent-strong:#a9bfff; --accent-soft:#8fa8ff1f; --on-accent:#0d1220;
      --ok:#4ade80; --warn:#fbbf24; --danger:#f472b6;
    }
  }
  * { box-sizing: border-box; }
  html, body { height:100%; }
  body { margin:0; font: 13px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); overflow:hidden; -webkit-font-smoothing:antialiased; }
  main { height:100vh; padding:14px 18px 10px; display:flex; flex-direction:column; gap:10px; }
  .icon { width:15px; height:15px; flex:0 0 auto; fill:none; stroke:currentColor; stroke-width:1.7; stroke-linecap:round; stroke-linejoin:round; }
  .page-head { display:flex; align-items:center; justify-content:space-between; gap:10px 16px; flex-wrap:wrap; flex:0 0 auto; }
  h1 { font-size:17px; margin:0 0 1px; letter-spacing:-.01em; }
  .sub { color:var(--muted); margin:0; font-size:11.5px; }
  .card, .group { background:var(--card); border:1px solid var(--line); border-radius:var(--radius); }
  .group { margin:0; padding:14px 16px; }
  .status { display:flex; align-items:center; gap:8px 16px; flex-wrap:wrap; padding:8px 12px; flex:0 0 auto; }
  .status-meta { display:flex; gap:8px 16px; flex-wrap:wrap; margin:0; }
  .status-meta > div { display:flex; align-items:baseline; gap:5px; }
  .status-meta dt { color:var(--muted); font-size:11px; }
  .status-meta dd { margin:0; font-size:12px; overflow-wrap:anywhere; }
  .settings-layout { flex:1 1 auto; min-height:0; display:grid; grid-template-columns:186px minmax(0,1fr); gap:12px; }
  #settings { flex:1 1 auto; min-height:0; display:flex; flex-direction:column; }
  .settings-single { grid-template-columns:minmax(0,1fr); }
  .settings-nav { display:flex; flex-direction:column; gap:2px; overflow-y:auto; overflow-x:hidden; min-height:0; padding:2px 4px 2px 0; scrollbar-width:thin; }
  .nav-item { display:flex; align-items:center; gap:8px; font:inherit; text-align:left; border:1px solid transparent; background:transparent; color:var(--muted); border-radius:7px; padding:6px 9px; cursor:pointer; transition:background .12s, color .12s; }
  .nav-item .icon { width:14px; height:14px; opacity:.85; }
  .nav-item:hover { background:var(--bg-soft); color:var(--fg); }
  .nav-item.is-active { background:var(--accent-soft); color:var(--accent); font-weight:600; }
  .nav-item.is-active .icon { opacity:1; }
  .settings-pane { display:flex; flex-direction:column; gap:10px; min-height:0; overflow-y:auto; padding:2px 6px 8px 2px; scrollbar-width:thin; }
  /* Author display rules (notably the flex Server form) otherwise beat the
     user-agent [hidden] rule, so the Server screen stayed visible on every
     other screen. */
  .settings-pane [data-section][hidden] { display:none !important; }
  .settings-pane > .group { flex:0 0 auto; }
  /* The Server form is long: its fields scroll on their own so the Save
     button stays at the bottom of the pane whatever the form height is. */
  .settings-pane > .group.server-settings { flex:1 1 auto; min-height:0; display:flex; flex-direction:column; }
  .server-fields { flex:1 1 auto; min-height:0; overflow-y:auto; padding-right:4px; scrollbar-width:thin; }
  .server-save { flex:0 0 auto; display:flex; justify-content:flex-end; margin-top:10px; padding-top:10px; border-top:1px solid var(--line-soft); }
  .settings-single .settings-pane { overflow:hidden; }
  .settings-single .wizard { flex:1 1 auto; display:flex; flex-direction:column; min-height:0; }
  .wizard-head { flex:0 0 auto; }
  .wizard-body { flex:1 1 auto; min-height:0; overflow-y:auto; padding-right:4px; scrollbar-width:thin; }
  .group h2 { display:flex; align-items:center; gap:7px; font-size:14px; margin:0 0 10px; letter-spacing:-.005em; }
  .group h2 .icon { color:var(--accent); }
  .group h3 { display:flex; align-items:center; gap:6px; font-size:10.5px; margin:14px 0 5px; color:var(--muted); text-transform:uppercase; letter-spacing:.07em; }
  .state { display:flex; align-items:center; gap:8px; }
  .dot { width:10px; height:10px; border-radius:50%; background:#8a8a8e; box-shadow:0 0 0 3px color-mix(in srgb, #8a8a8e 25%, transparent); }
  .state[data-state="recording"] .dot { background:var(--danger); box-shadow:0 0 0 3px color-mix(in srgb, var(--danger) 28%, transparent); }
  .state[data-state="processing"] .dot { background:var(--warn); box-shadow:0 0 0 3px color-mix(in srgb, var(--warn) 28%, transparent); }
  .state[data-state="error"] .dot { background:var(--danger); box-shadow:0 0 0 3px color-mix(in srgb, var(--danger) 28%, transparent); }
  .state strong { font-size:13px; font-weight:600; }
  dl { display:grid; grid-template-columns:max-content 1fr; gap:5px 14px; margin:12px 0 0; }
  dt { color:var(--muted); font-size:11.5px; }
  dd { margin:0; font-size:12px; overflow-wrap:anywhere; }
  .field { padding:9px 0; border-top:1px solid var(--line-soft); }
  .field:first-of-type { border-top:none; }
  .field-head { display:flex; align-items:center; gap:6px; flex-wrap:wrap; margin-bottom:4px; }
  .field-head label { font-weight:600; font-size:12.5px; }
  .field input[type="text"], .field input[type="number"], .wizard textarea { width:100%; padding:6px 9px; border:1px solid var(--line); border-radius:6px; background:var(--card-2); color:var(--fg); font:inherit; font-size:12.5px; line-height:1.35; transition:border-color .12s, box-shadow .12s; }
  .field select { width:100%; padding:6px 30px 6px 9px; border:1px solid var(--line); border-radius:6px; background-color:var(--card-2); background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%238a97ab' stroke-width='2.2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E"); background-repeat:no-repeat; background-position:right 9px center; background-size:12px 12px; color:var(--fg); font:inherit; font-size:12.5px; line-height:1.35; appearance:none; -webkit-appearance:none; cursor:pointer; transition:border-color .12s, box-shadow .12s; }
  .field input[type="text"]:focus, .field input[type="number"]:focus, .field select:focus, .wizard textarea:focus { outline:none; border-color:var(--accent); box-shadow:0 0 0 3px var(--accent-soft); }
  .field input[type="number"] { -moz-appearance:textfield; appearance:textfield; }
  .field input[type="number"]::-webkit-outer-spin-button, .field input[type="number"]::-webkit-inner-spin-button { -webkit-appearance:none; margin:0; }
  .field .switch { appearance:none; -webkit-appearance:none; width:36px; height:20px; margin:0; padding:0; border:1px solid var(--line); border-radius:999px; background-color:var(--bg-soft); background-image:radial-gradient(circle, #fff 0 6.5px, transparent 7px); background-size:14px 14px; background-repeat:no-repeat; background-position:2px center; cursor:pointer; flex:0 0 auto; box-shadow:inset 0 0 0 1px rgba(0,0,0,.04); transition:background-color .15s, border-color .15s, background-position .15s; }
  .field .switch:checked { background-color:var(--accent); border-color:transparent; background-position:18px center; }
  .field .switch:disabled { opacity:.55; cursor:not-allowed; }
  .field-toggle { padding:10px 0; }
  /* A miniature screen: the filled bar sits where the pill will appear. */
  .position-grid { display:grid; grid-template-columns:repeat(3, 1fr); gap:6px; max-width:260px; margin-top:2px; }
  .position-preset { position:relative; height:44px; padding:0; border:1px solid var(--line); border-radius:7px; background:var(--card-2); color:var(--muted); }
  .position-preset::before { content:""; position:absolute; inset:6px; border:1px solid var(--line); border-radius:4px; opacity:.55; }
  .position-preset::after { content:""; position:absolute; width:18px; height:5px; border-radius:3px; background:currentColor; opacity:.5; }
  .position-preset[data-indicator-anchor="top-left"]::after { left:10px; top:10px; }
  .position-preset[data-indicator-anchor="top-center"]::after { left:50%; top:10px; transform:translateX(-50%); }
  .position-preset[data-indicator-anchor="top-right"]::after { right:10px; top:10px; }
  .position-preset[data-indicator-anchor="bottom-left"]::after { left:10px; bottom:10px; }
  .position-preset[data-indicator-anchor="bottom-center"]::after { left:50%; bottom:10px; transform:translateX(-50%); }
  .position-preset[data-indicator-anchor="bottom-right"]::after { right:10px; bottom:10px; }
  .position-preset:hover { border-color:var(--accent); color:var(--accent); }
  .position-preset.is-active { border-color:var(--accent); background:var(--accent-soft); color:var(--accent); }
  .position-preset.is-active::after { opacity:1; }
  .position-preset:disabled { opacity:.55; cursor:not-allowed; }
  .toggle-row { display:flex; align-items:center; justify-content:space-between; gap:12px; cursor:pointer; }
  .toggle-label { display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-weight:600; font-size:12.5px; }
  .inline-field { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
  .inline-field input[type="number"] { width:104px; flex:0 0 104px; }
  .field input[disabled], .field select[disabled], .field button[disabled] { opacity:.55; cursor:not-allowed; }
  .help { color:var(--muted); margin:4px 0 0; font-size:11.5px; }
  .lock { color:var(--warn); margin:4px 0 0; font-size:11.5px; }
  .badge { font-size:10.5px; padding:1px 7px; border-radius:999px; background:#f5a52426; color:var(--warn); }
  .badge.hot { background:#2f9e441f; color:var(--ok); }
  .badge.active { background:var(--accent-soft); color:var(--accent); }
  pre.diagnostics, .spool-text { background:var(--card-2); border:1px solid var(--line-soft); border-radius:7px; padding:8px 10px; font-size:11.5px; }
  pre.diagnostics { max-height:220px; overflow:auto; white-space:pre-wrap; overflow-wrap:anywhere; }
  .spool-text { white-space:pre-wrap; overflow-wrap:anywhere; }
  .diagnostics-versions { margin-bottom:10px; }
  .source { font-size:11px; color:var(--muted); }
  .mic-actions { display:flex; gap:6px; margin-top:6px; flex-wrap:wrap; }
  .meter { height:7px; border-radius:999px; background:var(--line-soft); overflow:hidden; margin-top:6px; }
  .meter-fill { height:100%; width:0; background:linear-gradient(90deg, var(--accent), var(--ok)); transition:width 60ms linear; }
  .meter.is-live { outline:2px solid var(--accent); outline-offset:2px; }
  .device-error { color:var(--danger); }
  .gnome-offer { margin:6px 0 2px; padding:10px 12px; border:1px solid var(--line); border-radius:8px; background:var(--card-2); }
  .gnome-head { display:flex; align-items:center; gap:7px; margin-bottom:2px; }
  .gnome-head .icon { color:var(--accent); }
  .gnome-head h3 { margin:0; font-size:12.5px; font-weight:600; color:var(--fg); text-transform:none; letter-spacing:0; }
  .gnome-offer .help { margin-top:2px; }
  .gnome-offer .actions-inline { margin:8px 0 0; }
  .shortcut-offer { margin-top:12px; padding:10px 12px; border:1px solid var(--line); border-radius:8px; background:var(--card-2); }
  .shortcut-offer h4 { margin:0 0 3px; font-size:12.5px; }
  .config-path { color:var(--muted); font-size:11.5px; margin:0 4px; }
  .config-path code { overflow-wrap:anywhere; }
  .row { display:flex; align-items:center; gap:8px; flex-wrap:wrap; padding:6px 0; border-top:1px solid var(--line-soft); }
  .row:first-of-type { border-top:none; }
  .row code { color:var(--muted); overflow-wrap:anywhere; }
  .actions-inline { display:flex; gap:6px; flex-wrap:wrap; margin:8px 0; }
  .pair-code { font-size:16px; letter-spacing:.08em; }
  .devices, .models { width:100%; border-collapse:collapse; margin-top:8px; }
  .devices th, .devices td, .models th, .models td { text-align:left; padding:5px 6px; border-top:1px solid var(--line-soft); font-size:12px; }
  .devices th, .models th { color:var(--muted); font-weight:600; font-size:11px; border-top:none; }
  .devices td:last-child, .models .model-actions { text-align:right; white-space:nowrap; }
  .models .model-progress td { color:var(--muted); font-size:11.5px; border-top:none; }
  /* Every row's action occupies the same column width, so the download
     buttons line up instead of growing with the model name. */
  .models .model-actions button, .models .model-actions .badge { width:152px; justify-content:center; }
  .models .model-actions .badge { display:inline-flex; align-items:center; }
  .model-state { margin:8px 0 0; }
  .messages { display:flex; flex-direction:column; gap:5px; flex:0 0 auto; max-height:20vh; overflow-y:auto; }
  .messages:empty { display:none; }
  .notice { color:var(--ok); margin:0; padding:6px 10px; border-radius:7px; background:#2f9e441a; font-size:12px; }
  .failure { color:var(--danger); margin:0; padding:6px 10px; border-radius:7px; background:#d6409f14; font-size:12px; }
  .failure:empty { display:none; }
  .error { margin:0; padding:8px 12px; border:1px solid #d6409f55; border-radius:7px; color:var(--danger); display:none; font-size:12px; }
  .error.visible { display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
  .health { font-size:11px; color:var(--muted); }
  .health[data-health="online"] { color:var(--ok); }
  .health[data-health="stopped"] { color:var(--warn); }
  .health[data-health="offline"], .health[data-health="unauthenticated"], .health[data-health="mismatch"] { color:var(--danger); }
  .wizard-steps { list-style:none; display:flex; flex-wrap:wrap; gap:4px 8px; padding:0; margin:0 0 12px; }
  .wizard-steps li { display:flex; align-items:center; gap:5px; font-size:11px; color:var(--muted); }
  .wizard-steps li[data-status="current"] { color:var(--accent); font-weight:600; }
  .wizard-steps li[data-status="skipped"] { text-decoration:line-through; }
  .wizard-step-number { display:inline-flex; align-items:center; justify-content:center; width:17px; height:17px; border-radius:999px; border:1px solid var(--line); font-size:10px; }
  .wizard-steps li[data-status="done"] .wizard-step-number { background:#2f9e441f; color:var(--ok); border-color:transparent; }
  .wizard-steps li[data-status="current"] .wizard-step-number { background:var(--accent); color:var(--on-accent); border-color:transparent; }
  .wizard-progress { color:var(--muted); font-size:11.5px; margin:0 0 4px; }
  .wizard-summary { margin:8px 0; }
  .wizard-missing { margin:6px 0; padding-left:18px; }
  .wizard-footer { display:flex; gap:6px; flex-wrap:wrap; flex:0 0 auto; margin-top:10px; padding-top:12px; border-top:1px solid var(--line-soft); }
  .wizard textarea { resize:vertical; }
  .wizard h3 { display:flex; align-items:center; gap:7px; font-size:14px; margin:0 0 8px; color:var(--fg); letter-spacing:-.005em; text-transform:none; }
  .wizard h4 { font-size:12.5px; margin:14px 0 5px; color:var(--fg); }
  footer { display:flex; gap:8px; align-items:center; flex:0 0 auto; padding-top:10px; border-top:1px solid var(--line); }
  button { display:inline-flex; align-items:center; gap:6px; font:inherit; font-size:12.5px; padding:5px 10px; border-radius:6px; border:1px solid var(--line); background:var(--card-2); color:var(--fg); cursor:pointer; transition:background .12s, border-color .12s, opacity .12s; }
  button:hover { background:var(--bg-soft); }
  button .icon { width:14px; height:14px; }
  button.primary { background:var(--accent); border-color:transparent; color:var(--on-accent); font-weight:600; }
  button.primary:hover { background:var(--accent-strong); }
  button.pending { opacity:.6; cursor:progress; }
  .hint { color:var(--muted); font-size:11.5px; margin-left:auto; }
  ::-webkit-scrollbar { width:9px; height:9px; }
  ::-webkit-scrollbar-thumb { background:var(--line); border-radius:999px; }
  ::-webkit-scrollbar-thumb:hover { background:var(--muted); }
  ::-webkit-scrollbar-track { background:transparent; }
  @media (max-width: 720px) {
    main { padding:12px 12px 8px; }
    .settings-layout { grid-template-columns:minmax(0,1fr); grid-template-rows:auto minmax(0,1fr); gap:8px; }
    .settings-nav { flex-direction:row; overflow-x:auto; overflow-y:hidden; padding:0 0 4px; }
    .nav-item { white-space:nowrap; padding:5px 8px; }
    .status-meta { gap:6px 12px; }
  }
</style>
</head>
<body>
<main>
  <header class="page-head">
    <div>
      <h1 id="page-title">__TITLE__</h1>
      <p class="sub" id="page-subtitle">__SUBTITLE__</p>
    </div>
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">—</strong></div>
  </header>
  <section class="card status" aria-live="polite">
    <dl class="status-meta">
      <div><dt id="label-phase">—</dt><dd id="phase">idle</dd></div>
      <div><dt id="label-running">—</dt><dd id="running">—</dd></div>
      <div><dt id="label-server">—</dt><dd id="server">—</dd></div>
      <div><dt id="label-server-status">—</dt><dd id="server-status">—</dd></div>
      <div><dt id="label-version">—</dt><dd id="version">—</dd></div>
    </dl>
  </section>
  <p class="error" id="error"><span id="error-text"></span> <button type="button" data-action="settings" id="error-settings">__OPEN_SETTINGS__</button></p>
  <div class="messages" id="messages"></div>
  <div id="settings">__SETTINGS__</div>
  <footer id="actions"__ACTIONS_STYLE__>
    <button type="button" class="primary" data-action="save" id="save-button"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M5 12.5l4.3 4.3L19 6.8"/></svg><span class="btn-label">__SAVE__</span></button>
    <button type="button" data-action="restart" id="restart-button"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M20 12a8 8 0 1 1-2.6-5.9"/><path d="M20 3.8v4.6h-4.6"/></svg><span class="btn-label">__RESTART__</span></button>
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
    text("error-text", s.error);
    if (labels.openSettings) text("error-settings", labels.openSettings);
    var error = document.getElementById("error");
    if (error) error.classList.toggle("visible", Boolean(s.error));
    var errorSettings = document.getElementById("error-settings");
    if (errorSettings) errorSettings.hidden = !s.errorAction;
    var wizardPhase = document.querySelector("[data-wizard-phase]");
    if (wizardPhase) wizardPhase.textContent = labels.phaseValue || "";
    var wizardState = document.querySelector("[data-wizard-try-state]");
    if (wizardState) wizardState.textContent = labels.state || "";
    var wizardError = document.querySelector("[data-wizard-error]");
    if (wizardError) wizardError.textContent = s.wizardError || "";
  }
  var activeSection = "";
  function navButtons() {
    return document.querySelectorAll("#settings-nav [data-nav]");
  }
  function selectSection(key, keepScroll) {
    var buttons = navButtons();
    if (!buttons.length) {
      activeSection = "";
      return;
    }
    var chosen = null;
    for (var i = 0; i < buttons.length; i++) {
      if (buttons[i].dataset.nav === key) chosen = buttons[i];
    }
    if (!chosen) chosen = buttons[0];
    activeSection = chosen.dataset.nav;
    var targets = (chosen.dataset.targets || "").split(",");
    for (var i = 0; i < buttons.length; i++) {
      var on = buttons[i] === chosen;
      buttons[i].classList.toggle("is-active", on);
      buttons[i].setAttribute("aria-current", on ? "page" : "false");
    }
    var sections = document.querySelectorAll("#settings-pane [data-section]");
    for (var i = 0; i < sections.length; i++) {
      sections[i].hidden = targets.indexOf(sections[i].dataset.section) < 0;
    }
    if (!keepScroll) {
      var pane = document.getElementById("settings-pane");
      if (pane) pane.scrollTop = 0;
    }
    syncIndicatorPreview();
  }
  var previewTimer = null;
  var previewWanted = false;
  function indicatorPreviewPosition() {
    var anchor = document.querySelector("#settings [data-path='client.feedback.indicator_anchor']");
    if (!anchor) return null;
    var x = document.querySelector("#settings [data-path='client.feedback.indicator_offset_x']");
    var y = document.querySelector("#settings [data-path='client.feedback.indicator_offset_y']");
    return {
      action: "preview-indicator",
      anchor: anchor.value || "top-center",
      offset_x: Number(x ? x.value : 0) || 0,
      offset_y: Number(y ? y.value : 0) || 0
    };
  }
  function sendIndicatorPreview() {
    var position = indicatorPreviewPosition();
    if (!position || activeSection !== "appearance" || !previewWanted || document.hidden) return;
    fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(position)
    }).catch(function () {});
  }
  function hideIndicatorPreview() {
    previewWanted = false;
    if (previewTimer) {
      clearTimeout(previewTimer);
      previewTimer = null;
    }
    fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "hide-indicator-preview" }),
      // A tab close can unload the page before an ordinary request leaves;
      // keepalive lets the hide reach Go anyway.
      keepalive: true
    }).catch(function () {});
  }
  function syncIndicatorPreview(immediate) {
    if (previewTimer) {
      clearTimeout(previewTimer);
      previewTimer = null;
    }
    if (activeSection !== "appearance" || document.hidden) {
      hideIndicatorPreview();
      return;
    }
    previewWanted = true;
    if (immediate) sendIndicatorPreview();
    else previewTimer = setTimeout(sendIndicatorPreview, 120);
  }
  // Unsaved edits and the caret survive a re-render. Every settings refresh
  // replaces the whole fragment, so a health probe, a Device appearing, or a
  // Model download would otherwise drop whatever the user typed since the last
  // save. A control counts as edited when its value differs from the one Go
  // last rendered; only those values are put back, so a save, a reset, or a
  // rejected value still follows Go. A successful pairing also keeps its typed
  // form: the page cannot tell which refresh consumed it, and dropping a user's
  // text is the worse outcome.
  function controlKey(el) {
    if (el.dataset.serverPath) return "server:" + el.dataset.serverPath;
    if (el.dataset.path) return "path:" + el.dataset.path;
    if (el.id) return "id:" + el.id;
    return "";
  }
  function controlValue(el) {
    if (el.type === "checkbox" || el.type === "radio") return el.checked ? "1" : "0";
    return el.value;
  }
  // Controls that save themselves on change follow Go: restoring a rejected
  // choice would contradict the failure message.
  function liveControl(el) {
    if (el.closest("[data-startup-component], [data-log-level]")) return true;
    return el.dataset.path === "client.ui_language" || el.dataset.path === "client.language";
  }
  function markRendered(root) {
    if (!root) return;
    var controls = root.querySelectorAll("input, textarea, select");
    for (var i = 0; i < controls.length; i++) {
      controls[i].__kitsuneRendered = controlValue(controls[i]);
    }
  }
  function captureEdits(root) {
    var edits = [];
    if (!root) return edits;
    var controls = root.querySelectorAll("input, textarea, select");
    for (var i = 0; i < controls.length; i++) {
      var el = controls[i];
      if (el.disabled || liveControl(el)) continue;
      var key = controlKey(el);
      if (!key || el.__kitsuneRendered === undefined) continue;
      var value = controlValue(el);
      if (value === el.__kitsuneRendered) continue;
      edits.push({ key: key, value: value });
    }
    return edits;
  }
  function findControl(root, key) {
    if (!root) return null;
    var controls = root.querySelectorAll("input, textarea, select");
    for (var i = 0; i < controls.length; i++) {
      if (controlKey(controls[i]) === key) return controls[i];
    }
    return null;
  }
  function hasOption(select, value) {
    for (var i = 0; i < select.options.length; i++) {
      if (select.options[i].value === value) return true;
    }
    return false;
  }
  function restoreEdits(root, edits) {
    for (var i = 0; i < edits.length; i++) {
      var el = findControl(root, edits[i].key);
      if (!el || el.disabled) continue;
      if (el.type === "checkbox" || el.type === "radio") el.checked = edits[i].value === "1";
      else if (el.tagName === "SELECT" && !hasOption(el, edits[i].value)) continue;
      else el.value = edits[i].value;
    }
  }
  function captureFocus(root) {
    var el = document.activeElement;
    if (!root || !el || !root.contains(el)) return null;
    var key = controlKey(el);
    if (!key) return null;
    var start = null;
    var end = null;
    try {
      start = el.selectionStart;
      end = el.selectionEnd;
    } catch (e) {}
    return { key: key, start: start, end: end };
  }
  function restoreFocus(root, focus) {
    if (!root || !focus) return;
    var el = findControl(root, focus.key);
    if (!el || !el.focus) return;
    el.focus();
    if (focus.start === null || !el.setSelectionRange) return;
    try {
      el.setSelectionRange(focus.start, focus.end);
    } catch (e) {}
  }
  function renderSettings(v) {
    var host = document.getElementById("settings");
    var pane = document.getElementById("settings-pane");
    var fields = document.querySelector("#settings .server-fields");
    var previousSection = activeSection;
    var previousScroll = pane ? pane.scrollTop : 0;
    var previousFields = fields ? fields.scrollTop : 0;
    var edits = captureEdits(host);
    var focus = captureFocus(host);
    if (host) host.innerHTML = (v && v.html) || "";
    if (host) {
      markRendered(host);
      restoreEdits(host, edits);
    }
    if (v && v.language) document.documentElement.lang = v.language;
    if (v && v.title) {
      document.title = v.title;
      text("page-title", v.title);
    }
    if (v) text("page-subtitle", v.subtitle);
    if (v && v.actions) {
      var save = document.getElementById("save-button");
      if (save && v.actions.save) {
        var saveLabel = save.querySelector(".btn-label");
        if (saveLabel) saveLabel.textContent = v.actions.save;
        else save.textContent = v.actions.save;
      }
      var restart = document.getElementById("restart-button");
      if (restart && v.actions.restart) {
        var restartLabel = restart.querySelector(".btn-label");
        if (restartLabel) restartLabel.textContent = v.actions.restart;
        else restart.textContent = v.actions.restart;
      }
      var hint = document.getElementById("restart-hint");
      if (hint) hint.textContent = v.actions.restartHint || "";
    }
    var actions = document.getElementById("actions");
    if (actions) {
      var wizardActive = Boolean(v && v.wizard && v.wizard.enabled && v.wizard.active);
      var noSections = Boolean(v && (!v.sections || v.sections.length === 0));
      actions.style.display = (wizardActive || noSections) ? "none" : "flex";
    }
    selectSection(activeSection);
    // A refresh re-render must not throw the user back to the top of the
    // screen. Only a real section change resets the scroll position.
    if (previousSection && previousSection === activeSection) {
      var nextPane = document.getElementById("settings-pane");
      var nextFields = document.querySelector("#settings .server-fields");
      if (nextPane) nextPane.scrollTop = previousScroll;
      if (nextFields) nextFields.scrollTop = previousFields;
      // WebKit can apply the new layout after the assignment; repeat on the
      // next frame so the position survives a taller or shorter render.
      if (window.requestAnimationFrame) {
        window.requestAnimationFrame(function () {
          if (nextPane && nextPane.isConnected) nextPane.scrollTop = previousScroll;
          if (nextFields && nextFields.isConnected) nextFields.scrollTop = previousFields;
        });
      }
    }
    var wizardOutput = document.getElementById("wizard-try-output");
    if (wizardOutput && wizardOutput.focus) wizardOutput.focus();
    else restoreFocus(host, focus);
  }
  function renderSpool(v) {
    var host = document.getElementById("settings-pane") || document.getElementById("settings");
    var current = host ? host.querySelector("[data-section='spool']") : null;
    var holder = document.createElement("div");
    holder.innerHTML = (v && v.html) || "";
    var next = holder.firstElementChild;
    if (current && next) current.replaceWith(next);
    else if (current) current.remove();
    else if (next && host) host.appendChild(next);
    selectSection(activeSection, true);
  }
  function renderWizard(v) {
    var host = document.getElementById("settings-pane") || document.getElementById("settings");
    if (!host) return;
    var current = host.querySelector("[data-section='wizard']");
    var edits = captureEdits(current);
    var focus = captureFocus(current);
    var holder = document.createElement("div");
    holder.innerHTML = (v && v.html) || "";
    var next = holder.firstElementChild;
    if (current && next) current.replaceWith(next);
    else if (current) current.remove();
    else if (next) host.insertBefore(next, host.firstChild);
    var rendered = host.querySelector("[data-section='wizard']");
    markRendered(rendered);
    restoreEdits(rendered, edits);
    var output = document.getElementById("wizard-try-output");
    if (output && output.focus) output.focus();
    else restoreFocus(rendered, focus);
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
      // Only send edited fields: untouched null/optional fields must not be
      // rewritten as empty strings by a save of an unrelated setting. A select
      // has no defaultValue, so every control carries data-default.
      var initial = el.hasAttribute("data-default") ? el.getAttribute("data-default") : el.defaultValue;
      if (el.value === initial) continue;
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
  function showMessage(message, failure) {
    var host = document.getElementById("messages");
    if (!host) return;
    host.textContent = "";
    var p = document.createElement("p");
    p.className = failure ? "failure" : "notice";
    p.textContent = message;
    host.appendChild(p);
  }
  function sendIntent(intent, button) {
    if (button && button.dataset.pending === "1") return null;
    if (button) {
      button.dataset.pending = "1";
      button.classList.add("pending");
      button.disabled = true;
    }
    function done() {
      if (!button) return;
      delete button.dataset.pending;
      button.classList.remove("pending");
      button.disabled = false;
    }
    return fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(intent)
    }).then(function (response) {
      if (response.ok) return null;
      return response.text().then(function (body) {
        throw new Error((body || "").trim() || ("HTTP " + response.status));
      });
    }).catch(function (error) {
      showMessage(String((error && error.message) || error || "request failed"), true);
    }).then(done, done);
  }
  function primeDiagnostics() {
    // Startup preload: a missing Server must not paint an error banner over
    // the Settings window, so failures stay silent here.
    fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "prime-diagnostics" })
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
  function pairRemote(button) {
    function value(id) {
      var el = document.getElementById(id);
      return el ? el.value : "";
    }
    sendIntent({
      action: "pair-remote",
      address: value("server-address"),
      code: value("server-code"),
      name: value("server-name")
    }, button);
  }
  function wizardValue(id) {
    var el = document.getElementById(id);
    return el ? el.value : "";
  }
  function revokeDevice(button) {
    var message = button.dataset.confirm || "";
    if (message && window.confirm && !window.confirm(message)) return;
    sendIntent({ action: "revoke-device", device_id: button.dataset.deviceId || "" }, button);
  }
  document.addEventListener("click", function (event) {
    var preset = event.target.closest ? event.target.closest("[data-indicator-anchor]") : null;
    if (preset) {
      var field = preset.closest("[data-field]");
      var input = field ? field.querySelector("[data-path]") : null;
      if (input) input.value = preset.dataset.indicatorAnchor || "top-center";
      var presets = field ? field.querySelectorAll("[data-indicator-anchor]") : [];
      for (var p = 0; p < presets.length; p++) {
        var active = presets[p] === preset;
        presets[p].classList.toggle("is-active", active);
        presets[p].setAttribute("aria-pressed", active ? "true" : "false");
      }
      syncIndicatorPreview(true);
      return;
    }
    var button = event.target.closest ? event.target.closest("[data-action], [data-nav]") : null;
    if (!button) return;
    if (button.dataset.nav) {
      selectSection(button.dataset.nav);
      return;
    }
    var action = button.dataset.action;
    if (action === "save") sendIntent({ action: "save", patch: collectPatch() }, button);
    else if (action === "gnome-shortcuts-add") sendIntent({ action: "gnome-shortcuts-add" }, button);
    else if (action === "gnome-shortcuts-remove") sendIntent({ action: "gnome-shortcuts-remove" }, button);
    else if (action === "restart") sendIntent({ action: "restart" }, button);
    else if (action === "test-mic") {
      var input = document.querySelector("#settings [data-path='client.audio.device']");
      var liveMeter = document.querySelector("#settings .meter[role='meter']");
      if (liveMeter) liveMeter.classList.add("is-live");
      sendIntent({ action: "test-mic", device: input ? input.value : "" }, button);
    } else if (action === "stop-mic") {
      var stoppedMeter = document.querySelector("#settings .meter[role='meter']");
      if (stoppedMeter) stoppedMeter.classList.remove("is-live");
      setLevel(0);
      sendIntent({ action: "stop-mic" }, button);
    } else if (action === "pair-remote") {
      pairRemote(button);
    } else if (action === "refresh-devices") {
      sendIntent({ action: "refresh-devices" }, button);
    } else if (action === "revoke-device") {
      revokeDevice(button);
    } else if (action === "pairing-code") {
      sendIntent({ action: "pairing-code" }, button);
    } else if (action === "use-profile") {
      sendIntent({ action: "use-profile", profile_id: button.dataset.profileId || "" }, button);
    } else if (action === "refresh-servers") {
      sendIntent({ action: "refresh-servers" }, button);
    } else if (action === "spool-retry") {
      sendIntent({ action: "spool-retry", entry_id: button.dataset.entryId || "" }, button);
    } else if (action === "spool-copy") {
      sendIntent({ action: "spool-copy", entry_id: button.dataset.entryId || "" }, button);
    } else if (action === "spool-insert") {
      sendIntent({ action: "spool-insert", entry_id: button.dataset.entryId || "" }, button);
    } else if (action === "spool-delete") {
      var deleteMessage = button.dataset.confirm || "";
      if (deleteMessage && window.confirm && !window.confirm(deleteMessage)) return;
      sendIntent({ action: "spool-delete", entry_id: button.dataset.entryId || "" }, button);
    } else if (action === "settings") {
      var pane = document.getElementById("settings-pane");
      if (pane) pane.scrollTop = 0;
    } else if (action === "copy-address") {
      sendIntent({ action: "copy-address" }, button);
    } else if (action === "exposure") {
      var expose = button.dataset.expose || "off";
      var exposureMessage = button.dataset.confirm || "";
      if (expose === "on" && exposureMessage && window.confirm && !window.confirm(exposureMessage)) return;
      sendIntent({ action: "exposure", value: expose }, button);
    } else if (action === "load-models") {
      sendIntent({ action: "load-models" }, button);
    } else if (action === "download-model") {
      sendIntent({ action: "download-model", model: button.dataset.model || "" }, button);
    } else if (action === "cancel-download") {
      sendIntent({ action: "cancel-download", model: button.dataset.model || "" }, button);
    } else if (action === "activate-model") {
      sendIntent({ action: "activate-model", model: button.dataset.model || "" }, button);
    } else if (action === "activate-local") {
      var localPath = document.getElementById("model-local-path");
      sendIntent({ action: "activate-model", path: localPath ? localPath.value : "" }, button);
    } else if (action === "delete-model") {
      var deleteMessage = button.dataset.confirm || "";
      if (deleteMessage && window.confirm && !window.confirm(deleteMessage)) return;
      sendIntent({ action: "delete-model", model: button.dataset.model || "" }, button);
    } else if (action === "idle-timeout") {
      var idle = document.getElementById("model-idle-seconds");
      sendIntent({ action: "idle-timeout", seconds: idle ? Number(idle.value) : 0 }, button);
    } else if (action === "save-server") {
      sendIntent({ action: "save-server", patch: collectServerPatch() }, button);
    } else if (action === "refresh-diagnostics") {
      sendIntent({ action: "refresh-diagnostics" }, button);
    } else if (action === "load-logs") {
      sendIntent({ action: "load-logs" }, button);
    } else if (action === "copy-diagnostics") {
      sendIntent({ action: "copy-diagnostics" }, button);
    } else if (action === "copy-logs") {
      sendIntent({ action: "copy-logs" }, button);
    } else if (action === "copy-client-logs") {
      sendIntent({ action: "copy-client-logs" }, button);
    } else if (action === "server-restart") {
      sendIntent({ action: "server-restart" }, button);
    } else if (action === "server-stop") {
      sendIntent({ action: "server-stop" }, button);
    } else if (action === "server-start") {
      sendIntent({ action: "server-start" }, button);
    } else if (action === "check-updates") {
      sendIntent({ action: "check-updates" }, button);
    } else if (action === "install-update") {
      sendIntent({ action: "install-update" }, button);
    } else if (action === "wizard-open") {
      sendIntent({ action: "wizard-open" }, button);
    } else if (action === "wizard-close") {
      sendIntent({ action: "wizard-close" }, button);
    } else if (action === "wizard-next") {
      sendIntent({
        action: "wizard-next",
        device: wizardValue("wizard-device"),
        hotkey: wizardValue("wizard-hotkey"),
        trigger: wizardValue("wizard-trigger"),
        choice: wizardValue("wizard-hardware")
      }, button);
    } else if (action === "wizard-back") {
      sendIntent({ action: "wizard-back" }, button);
    } else if (action === "wizard-skip") {
      sendIntent({ action: "wizard-skip" }, button);
    } else if (action === "wizard-skip-all") {
      sendIntent({ action: "wizard-skip-all" }, button);
    } else if (action === "wizard-restart") {
      sendIntent({ action: "wizard-restart" }, button);
    } else if (action === "wizard-finish") {
      sendIntent({ action: "wizard-finish" }, button);
    } else if (action === "wizard-refresh") {
      sendIntent({ action: "wizard-refresh" }, button);
    } else if (action === "wizard-goto") {
      sendIntent({ action: "wizard-goto", step: button.dataset.step || "" }, button);
    } else if (action === "wizard-mic-start") {
      var wizardMeter = document.querySelector("#settings .meter[role='meter']");
      if (wizardMeter) wizardMeter.classList.add("is-live");
      sendIntent({ action: "wizard-mic-start", device: wizardValue("wizard-device") }, button);
    } else if (action === "wizard-mic-stop") {
      var wizardStopped = document.querySelector("#settings .meter[role='meter']");
      if (wizardStopped) wizardStopped.classList.remove("is-live");
      setLevel(0);
      sendIntent({ action: "wizard-mic-stop" }, button);
    } else if (action === "wizard-model-download") {
      sendIntent({ action: "wizard-model-download", model: button.dataset.model || "" }, button);
    } else if (action === "wizard-model-cancel") {
      sendIntent({ action: "wizard-model-cancel", model: button.dataset.model || "" }, button);
    } else if (action === "wizard-model-activate") {
      sendIntent({ action: "wizard-model-activate", model: button.dataset.model || "" }, button);
    } else if (action === "wizard-model-defer") {
      sendIntent({ action: "wizard-model-defer" }, button);
    } else if (action === "wizard-local") {
      sendIntent({ action: "wizard-local", address: wizardValue("wizard-local-address") }, button);
    } else if (action === "wizard-pair-remote") {
      sendIntent({
        action: "wizard-pair-remote",
        address: wizardValue("wizard-server-address"),
        code: wizardValue("wizard-server-code"),
        name: wizardValue("wizard-server-name")
      }, button);
    } else if (action === "wizard-try-start") {
      var wizardOutput = document.getElementById("wizard-try-output");
      if (wizardOutput && wizardOutput.focus) wizardOutput.focus();
      sendIntent({ action: "wizard-try-start" }, button);
    } else if (action === "wizard-try-start-server") {
      sendIntent({ action: "wizard-try-start-server" }, button);
    } else if (action === "wizard-try-stop") {
      var wizardStopOutput = document.getElementById("wizard-try-output");
      if (wizardStopOutput && wizardStopOutput.focus) wizardStopOutput.focus();
      sendIntent({ action: "wizard-try-stop" }, button);
    } else if (action === "wizard-try-confirm") {
      sendIntent({ action: "wizard-try-confirm" }, button);
    } else if (action === "wizard-try-fail") {
      sendIntent({ action: "wizard-try-fail" }, button);
    }
  });
  document.addEventListener("input", function (event) {
    var target = event.target;
    if (!target || !target.closest) return;
    if (target.closest("[data-indicator-preview]")) syncIndicatorPreview(false);
  });
  document.addEventListener("change", function (event) {
    var target = event.target;
    if (!target || !target.closest) return;
    var startup = target.closest("[data-startup-component]");
    if (startup) {
      sendIntent({
        action: "set-startup",
        component: startup.dataset.startupComponent,
        enabled: startup.checked
      });
      return;
    }
    var level = target.closest("[data-log-level]");
    if (level) {
      sendIntent({ action: "set-log-level", value: level.value });
      return;
    }
    var el = target.closest("[data-path]");
    if (!el) return;
    if (el.dataset.path === "client.ui_language") {
      sendIntent({ action: "save", patch: { ui_language: el.value } });
    } else if (el.dataset.path === "client.language") {
      // The spoken language is per request. Saving it as soon as it changes
      // removes the chance that a user tests dictation with an unsaved value.
      sendIntent({ action: "save", patch: { language: el.value } });
    }
  });
  document.addEventListener("keydown", function (event) {
    var el = event.target.closest ? event.target.closest("[data-hotkey], [data-wizard-hotkey]") : null;
    if (!el) return;
    if (event.key === "Tab") return;
    event.preventDefault();
    var spec = hotkeySpec(event);
    if (!spec) return;
    el.value = spec;
    if (el.dataset.wizardHotkey) {
      sendIntent({ action: "wizard-validate-hotkey", value: spec });
    } else {
      sendIntent({ action: "validate", path: el.dataset.path, value: spec });
    }
  });
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) hideIndicatorPreview();
    else syncIndicatorPreview(true);
  });
  window.addEventListener("beforeunload", hideIndicatorPreview);
  renderStatus(__INITIAL__);
  selectSection("");
  markRendered(document.getElementById("settings"));
  // Fill the Diagnostics cache when the window opens; the first visit to the
  // screen then shows real values instead of empty placeholders.
  primeDiagnostics();
</script>
</body>
</html>
`
