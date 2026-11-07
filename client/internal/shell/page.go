package shell

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/humanize"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
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
	closeLabel := i18n.For(settings.Language).T("notice.close")
	return strings.NewReplacer(
		"__LANG__", html.EscapeString(language),
		"__THEME__", html.EscapeString(themeAttribute(settings.Theme)),
		"__TITLE__", html.EscapeString(title),
		"__SETTINGS__", SettingsHTML(settings),
		"__ERRORS__", jsonForScript(collectViewErrors(settings)),
		"__SAVE__", html.EscapeString(settings.Actions.Save),
		"__RESTART__", html.EscapeString(settings.Actions.Restart),
		"__RESTART_HINT__", html.EscapeString(settings.Actions.RestartHint),
		"__CLOSE_LABEL__", jsonForScript(closeLabel),
		"__ACTIONS_STYLE__", actionsStyle,
		"__INITIAL__", jsonForScript(initial),
	).Replace(pageHTML)
}

// themeAttribute normalizes the theme the page paints with. An unknown or
// empty value follows the desktop, matching the built-in default.
func themeAttribute(value string) string {
	switch value {
	case "light", "dark":
		return value
	default:
		return "system"
	}
}

// navEntry is one Settings navigation item: a translated label and the section
// ids it shows in the content pane. A section may be listed under one entry
// only, so the pane never shows the same form twice.
type navEntry struct {
	Key     string
	Label   string
	Targets []string
}

// navEntries lists the Settings screens in navigation order. Related screens
// share one entry so the menu stays short: everyday preferences, dictation,
// the Server, its Models, saved recordings, and Diagnostics. An entry is
// omitted when the build does not render its content, so the menu never offers
// an empty screen.
func navEntries(view SettingsView) []navEntry {
	catalog := i18n.For(view.Language)
	var entries []navEntry

	general := sectionTargets(view, "general")
	if view.Startup.Available {
		general = append(general, "startup")
	}
	if view.Update.Title != "" {
		general = append(general, "updates")
	}
	if view.Wizard.Enabled && !view.Wizard.Active && view.Wizard.Labels.ReopenTitle != "" {
		general = append(general, "wizard-launcher")
	}
	if len(general) > 0 {
		entries = append(entries, navEntry{Key: "general", Label: catalog.T("section.general"), Targets: general})
	}

	if dictationTargets := dictationSectionTargets(view); len(dictationTargets) > 0 {
		entries = append(entries, navEntry{
			Key:     "dictation",
			Label:   catalog.T("section.dictation"),
			Targets: dictationTargets,
		})
	}
	if appearance := sectionTargets(view, "appearance"); len(appearance) > 0 {
		entries = append(entries, navEntry{
			Key:     "appearance",
			Label:   catalog.T("section.appearance"),
			Targets: appearance,
		})
	}

	var server []string
	if view.Labels.Title != "" {
		server = append(server, "servers", "server-pair", "server-control", "exposure", "devices")
	}
	if view.Labels.SettingsTitle != "" {
		server = append(server, "server-settings")
	}
	if len(server) > 0 {
		entries = append(entries, navEntry{Key: "server", Label: catalog.T("nav.server"), Targets: server})
	}

	if view.ModelLabels.Title != "" {
		entries = append(entries, navEntry{
			Key:     "models",
			Label:   view.ModelLabels.Title,
			Targets: []string{"models", "models-idle", "models-local"},
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
	return entries
}

// sectionIsWide marks the long Settings screens from the rendered section
// list that need every column. The hand-built screens set group-wide where
// they are rendered. Short cards share a row.
func sectionIsWide(id string) bool {
	switch id {
	case "dictation", "dictation-hotkeys", "dictation-paste", "appearance":
		return true
	}
	return false
}

// sectionTargets returns id when the view rendered that section.
func sectionTargets(view SettingsView, id string) []string {
	for _, section := range view.Sections {
		if section.ID == id && section.Title != "" {
			return []string{id}
		}
	}
	return nil
}

// dictationSectionTargets collects every card of the Dictation screen. The
// screen is one navigation entry made of several cards, each a section of its
// own, so the entry must show all of them at once.
func dictationSectionTargets(view SettingsView) []string {
	var targets []string
	for _, section := range view.Sections {
		if section.Title == "" {
			continue
		}
		if section.ID == "dictation" || strings.HasPrefix(section.ID, "dictation-") {
			targets = append(targets, section.ID)
		}
	}
	return targets
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
		classes := "group"
		if sectionIsWide(section.ID) {
			classes += " group-wide"
		}
		out.WriteString(`<section class="` + classes + `" data-section="` + escape(section.ID) + `">`)
		out.WriteString(`<h2>` + sectionIcon(section.ID) + escape(section.Title) + `</h2>`)
		out.WriteString(`<div class="field-grid">`)
		for _, field := range section.Fields {
			writeField(&out, field, view.View)
		}
		// GNOME's system keybindings belong to the hotkeys they bind. The offer
		// goes below the whole hotkey card, so it never splits the row of
		// hotkey, cancel and backend fields.
		if (section.ID == "dictation" || section.ID == "dictation-hotkeys") && view.Gnome.Visible {
			out.WriteString(gnomeShortcutsHTML(view.Gnome))
		}
		out.WriteString(`</div></section>`)
	}
	out.WriteString(serversHTML(view.Servers, view.Labels, view.ModelLabels, view.ModelState))
	out.WriteString(spoolHTML(view.Spool))
	out.WriteString(startupHTML(view.Startup, view.Labels))
	out.WriteString(updatesHTML(view.Update))
	out.WriteString(serverSettingsHTML(view.Servers, view.Labels))
	out.WriteString(diagnosticsHTML(view.Diagnostics, view.Labels))
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

// cardHelpTip puts a card's explanatory paragraph behind a question mark next
// to its heading. The text stays available on hover and keyboard focus without
// pushing the card's controls down.
func cardHelpTip(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return `<span class="help-tip" tabindex="0" role="note" aria-label="` + escape(text) +
		`" data-tip="` + escape(text) + `">?</span>`
}

// serversHTML renders the Server profiles and the active profile's Devices.
// Inputs have stable ids so the page can collect one pairing request without
// rebuilding any other screen.
func serversHTML(view servers.View, labels ServerLabels, modelLabels ModelLabels, modelState string) string {
	var out strings.Builder
	out.WriteString(`<section class="group group-wide" data-section="servers">`)
	out.WriteString(`<div class="card-head"><h2>` + pageIcon("server") + escape(labels.Title) + `</h2>`)
	out.WriteString(`<button type="button" class="icon-button" data-action="refresh-servers" title="` +
		escape(labels.RefreshHealth) + `" aria-label="` + escape(labels.RefreshHealth) + `">` +
		pageIcon("refresh") + `</button></div>`)
	if len(view.Profiles) == 0 {
		out.WriteString(`<p class="help">` + escape(labels.NoServers) + `</p>`)
	} else {
		out.WriteString(`<div class="profile-list">`)
		for _, profile := range view.Profiles {
			healthKey := string(profile.Health.State)
			if profile.Health.State == trust.StateOnline && serverSoftStopped(profile.Health) {
				healthKey = "stopped"
			}
			health := labels.StateLabels[healthKey]
			if health == "" {
				health = labels.StateLabels[string(trust.StateUnknown)]
			}
			out.WriteString(`<div class="profile-row" data-profile="` + escape(profile.ID) + `">`)
			out.WriteString(`<div class="profile-main"><div class="profile-title"><strong>` + escape(profile.Name) + `</strong>`)
			if profile.Active {
				out.WriteString(`<span class="badge active">` + escape(labels.Active) + `</span>`)
			}
			out.WriteString(`</div><code class="profile-url">` + escape(profile.URL) + `</code></div>`)
			out.WriteString(`<span class="health" data-health="` + escape(healthKey) + `" title="` +
				escape(labels.Health+": "+health) + `">` + escape(health) + `</span>`)
			out.WriteString(`<div class="profile-actions">`)
			if !profile.Active {
				out.WriteString(`<button type="button" class="icon-button" data-action="use-profile" data-profile-id="` +
					escape(profile.ID) + `" title="` + escape(labels.Use) + `" aria-label="` + escape(labels.Use) + `">` +
					pageIcon("check") + `</button>`)
			}
			if serverProfileNeedsRepair(profile.Health.State) {
				confirm := ""
				if profile.AutoPair {
					// A Local Server re-pairs without a code, so the click is
					// the only confirmation before the new certificate is
					// pinned. A Remote Server still needs a fresh Pairing code.
					confirm = labels.RepairConfirm
				}
				out.WriteString(`<button type="button" class="icon-button" data-action="repair-profile" data-profile-id="` +
					escape(profile.ID) + `" data-profile-url="` + escape(profile.URL) +
					`" data-profile-local="` + boolText(profile.AutoPair) +
					`" data-confirm="` + escape(confirm) + `" title="` + escape(labels.Repair) +
					`" aria-label="` + escape(labels.Repair) + `">` + pageIcon("refresh") + `</button>`)
			}
			if len(view.Profiles) > 1 {
				out.WriteString(`<button type="button" class="icon-button danger" data-action="remove-profile" data-profile-id="` +
					escape(profile.ID) + `" data-confirm="` + escape(labels.RemoveConfirm) +
					`" title="` + escape(labels.RemoveProfile) + `" aria-label="` + escape(labels.RemoveProfile) +
					`">` + pageIcon("trash") + `</button>`)
			}
			out.WriteString(`</div></div>`)
		}
		out.WriteString(`</div>`)
	}
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="server-pair">`)
	out.WriteString(`<h2>` + pageIcon("plus") + escape(labels.PairTitle) + cardHelpTip(labels.PairHelp) + `</h2>`)
	out.WriteString(`<div class="card-fields">`)
	out.WriteString(`<div class="field"><label for="server-address">` + escape(labels.Address) + `</label>`)
	out.WriteString(`<input type="text" id="server-address" autocomplete="off" spellcheck="false" placeholder="desktop.local:8765"></div>`)
	out.WriteString(`<div class="field"><label for="server-code">` + escape(labels.Code) + `</label>`)
	out.WriteString(`<input type="text" id="server-code" autocomplete="off" spellcheck="false" maxlength="16"></div>`)
	out.WriteString(`<div class="field"><label for="server-name">` + escape(labels.Name) + `</label>`)
	out.WriteString(`<input type="text" id="server-name" autocomplete="off" spellcheck="false">`)
	out.WriteString(`<p class="help">` + escape(labels.NameHelp) + `</p></div>`)
	out.WriteString(`</div>`)
	out.WriteString(`<button type="button" class="primary card-action" data-action="pair-remote">` + escape(labels.Pair) + `</button>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="server-control">`)
	out.WriteString(`<h2>` + pageIcon("power") + escape(labels.ManagementTitle) + cardHelpTip(labels.ManagementHelp) + `</h2>`)
	if view.Lifecycle != nil {
		out.WriteString(`<p class="stat-line"><span>` + escape(labels.ModelState) + `</span><strong data-model-state="` +
			escape(view.Lifecycle.ModelState) + `">` + escape(valueOr(modelState, view.Lifecycle.ModelState)) + `</strong></p>`)
	}
	out.WriteString(`<div class="server-actions">`)
	out.WriteString(`<button type="button" data-action="server-restart">` + pageIcon("restart") + escape(labels.RestartServer) + `</button>`)
	out.WriteString(`<button type="button" data-action="server-stop">` + pageIcon("stop") + escape(labels.StopServer) + `</button>`)
	out.WriteString(`<button type="button" data-action="server-start">` + pageIcon("play") + escape(labels.StartServer) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="exposure">`)
	out.WriteString(`<h2>` + pageIcon("globe") + escape(labels.ExposureTitle) + cardHelpTip(labels.ExposureHelp) + `</h2>`)
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
		out.WriteString(`<button type="button" class="icon-button" data-action="copy-address" title="` +
			escape(labels.CopyAddress) + `" aria-label="` + escape(labels.CopyAddress) + `">` +
			pageIcon("paste") + `</button>`)
		out.WriteString(`</div>`)
	}
	if live == nil {
		if view.Exposure != nil {
			state := labels.ExposureOffState
			if view.Exposure.ExposeToNetwork {
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
	out.WriteString(`<div class="actions-inline actions-equal">`)
	out.WriteString(`<button type="button" data-action="exposure" data-expose="on" data-confirm="` + escape(labels.ExposureConfirm) + `">` + escape(labels.ExposureOn) + `</button>`)
	out.WriteString(`<button type="button" data-action="exposure" data-expose="off">` + escape(labels.ExposureOff) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group group-wide" data-section="devices">`)
	devicesHelp := strings.TrimSpace(labels.DevicesFor)
	if help := strings.TrimSpace(labels.CodeHelp); help != "" {
		if devicesHelp != "" {
			devicesHelp += "\n\n"
		}
		devicesHelp += help
	}
	out.WriteString(`<div class="card-head"><h2>` + pageIcon("monitor") + escape(labels.Devices) + cardHelpTip(devicesHelp) + `</h2>`)
	out.WriteString(`<div class="card-head-actions">`)
	out.WriteString(`<button type="button" class="icon-button" data-action="refresh-devices" title="` +
		escape(labels.Refresh) + `" aria-label="` + escape(labels.Refresh) + `">` + pageIcon("refresh") + `</button>`)
	out.WriteString(`<button type="button" data-action="pairing-code">` + pageIcon("plus") + escape(labels.GenerateCode) + `</button>`)
	out.WriteString(`</div></div>`)
	if view.PairingCode != nil {
		out.WriteString(`<p class="pair-code" data-code="` + escape(view.PairingCode.Code) + `"><strong>` + escape(view.PairingCode.Code) + `</strong> · ` + escape(labels.CodeExpires) + `</p>`)
		if view.PairingCode.Fingerprint != "" {
			out.WriteString(`<p class="help">` + escape(labels.Fingerprint) + `: <code>` + escape(view.PairingCode.Fingerprint) + `</code></p>`)
		}
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
	out.WriteString(modelsHTML(view.Models, modelLabels))
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
		return view.Exposure.ExposeToNetwork != live.ExposeToNetwork
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
	out.WriteString(`<section class="group group-wide" data-section="spool">`)
	out.WriteString(`<h2>` + pageIcon("spool") + escape(view.Labels.Title) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.Help) + `</p>`)
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

// modelsHTML renders the Model catalog, its download and activation actions,
// the idle timeout, and the local CTranslate2 directory as separate cards.
func modelsHTML(view *servers.ModelsView, labels ModelLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group group-wide" data-section="models">`)
	out.WriteString(`<div class="card-head"><h2>` + pageIcon("models") + escape(labels.CatalogTitle) +
		cardHelpTip(labels.Help) + `</h2>`)
	out.WriteString(`<button type="button" class="icon-button" data-action="load-models" title="` +
		escape(labels.Refresh) + `" aria-label="` + escape(labels.Refresh) + `">` +
		pageIcon("refresh") + `</button></div>`)
	if view == nil {
		out.WriteString(`<p class="help">` + escape(labels.NoCatalog) + `</p>`)
		out.WriteString(`</section>`)
		return out.String()
	}
	out.WriteString(`<p class="model-state">` + escape(labels.State) + `: <strong>` + escape(modelStateLabel(labels, view.State)) + `</strong>`)
	if view.ActiveModel != "" {
		out.WriteString(` · ` + escape(labels.Active) + `: <code>` + escape(view.ActiveModel) + `</code>`)
	}
	if view.ActivePath != "" {
		out.WriteString(` (<code>` + escape(view.ActivePath) + `</code>)`)
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
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="models-idle">`)
	out.WriteString(`<h2>` + pageIcon("power") + escape(labels.IdleTitle) + cardHelpTip(labels.IdleHelp) + `</h2>`)
	out.WriteString(`<div class="field"><div class="field-head"><label for="model-idle-seconds">` + escape(labels.IdleLabel) + `</label></div>`)
	out.WriteString(`<div class="inline-field">`)
	out.WriteString(`<input type="number" min="0" step="30" id="model-idle-seconds" value="` + escape(strconv.Itoa(view.IdleUnloadSeconds)) + `">`)
	out.WriteString(`<button type="button" data-action="idle-timeout">` + escape(labels.IdleSave) + `</button>`)
	out.WriteString(`</div></div>`)
	out.WriteString(`</section>`)

	out.WriteString(`<section class="group" data-section="models-local">`)
	out.WriteString(`<h2>` + pageIcon("folder") + escape(labels.AdvancedTitle) + cardHelpTip(labels.AdvancedHelp) + `</h2>`)
	out.WriteString(`<div class="field"><label for="model-local-path">` + escape(labels.LocalPath) + `</label>`)
	out.WriteString(`<input type="text" id="model-local-path" autocomplete="off" spellcheck="false">`)
	out.WriteString(`<p class="help">` + escape(labels.LocalPathHint) + `</p></div>`)
	out.WriteString(`<button type="button" class="card-action" data-action="activate-local">` + escape(labels.UseLocal) + `</button>`)
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

// modelActions renders the one control a catalog row offers. The actions are
// icon buttons with tooltips, so the catalog stays compact; an active model
// needs no action because its badge already sits beside the name.
func modelActions(model servers.Model, view *servers.ModelsView, labels ModelLabels) string {
	if servers.DownloadInProgress(model.Download) {
		return `<button type="button" class="icon-button" data-action="cancel-download" data-model="` +
			escape(model.Name) + `" title="` + escape(labels.Cancel) + `" aria-label="` +
			escape(labels.Cancel) + `">` + pageIcon("close") + `</button>`
	}
	if !model.Installed {
		return `<button type="button" class="icon-button" data-action="download-model" data-model="` +
			escape(model.Name) + `" title="` + escape(labels.Download) + `" aria-label="` +
			escape(labels.Download) + `">` + pageIcon("download") + `</button>`
	}
	if model.Name == view.ActiveModel {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<button type="button" class="icon-button" data-action="activate-model" data-model="` +
		escape(model.Name) + `" title="` + escape(labels.Activate) + `" aria-label="` +
		escape(labels.Activate) + `">` + pageIcon("check") + `</button>`)
	out.WriteString(` <button type="button" class="icon-button danger" data-action="delete-model" data-model="` +
		escape(model.Name) + `" data-confirm="` + escape(labels.DeleteConfirm) + `" title="` +
		escape(labels.Delete) + `" aria-label="` + escape(labels.Delete) + `">` + pageIcon("trash") + `</button>`)
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

// startupHTML renders the single autostart switch with the mode selector
// beneath it. The switch is the only state the platform holds; while it is off
// the selector is read-only and shows the default mode, so turning the switch
// on starts exactly what the user sees. Go changes the registrations and
// re-renders the section.
func startupHTML(view StartupView, labels ServerLabels) string {
	if !view.Available {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group group-wide" data-section="startup">`)
	out.WriteString(`<h2>` + pageIcon("power") + escape(labels.StartupTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(labels.StartupHelp) + `</p>`)
	out.WriteString(`<div class="field field-toggle" data-startup="switch">`)
	out.WriteString(`<label class="toggle-row" for="startup-enabled">`)
	out.WriteString(`<span class="toggle-label">` + escape(labels.StartupSwitch) + `</span>`)
	input := `<input type="checkbox" class="switch" id="startup-enabled" data-startup-switch`
	if view.Enabled {
		input += ` checked`
	}
	out.WriteString(input + `>`)
	out.WriteString(`</label>`)
	out.WriteString(`</div>`)
	out.WriteString(`<fieldset class="field startup-modes" data-startup-modes`)
	if !view.Enabled {
		out.WriteString(` disabled`)
	}
	out.WriteString(`>`)
	out.WriteString(`<legend>` + escape(labels.StartupMode) + `</legend>`)
	for _, mode := range view.Modes {
		label, help := startupModeText(labels, mode)
		out.WriteString(`<label class="startup-mode">`)
		input := `<input type="radio" name="startup-mode" value="` + escape(string(mode)) +
			`" data-startup-mode="` + escape(string(mode)) + `"`
		if mode == view.Mode {
			input += ` checked`
		}
		out.WriteString(input + `>`)
		out.WriteString(`<span class="startup-mode-text">`)
		out.WriteString(`<span class="startup-mode-label">` + escape(label) + `</span>`)
		if help != "" {
			out.WriteString(`<span class="help">` + escape(help) + `</span>`)
		}
		out.WriteString(`</span>`)
		out.WriteString(`</label>`)
	}
	out.WriteString(`</fieldset>`)
	out.WriteString(`</section>`)
	return out.String()
}

// startupModeKeys maps an Autostart mode to its catalog keys. The rendered
// selector and the change notice share this one mapping; each resolves the
// keys through the translated table it already holds.
func startupModeKeys(mode components.Mode) (labelKey, helpKey string) {
	switch mode {
	case components.ModeClientAndServer:
		return "startup.mode.clientServer", "startup.mode.clientServerHelp"
	case components.ModeClientOnly:
		return "startup.mode.client", "startup.mode.clientHelp"
	case components.ModeServerOnly:
		return "startup.mode.server", "startup.mode.serverHelp"
	default:
		return "", ""
	}
}

// startupModeText returns the translated label and explanation of one mode.
func startupModeText(labels ServerLabels, mode components.Mode) (label, help string) {
	labelKey, helpKey := startupModeKeys(mode)
	if labelKey == "" {
		return string(mode), ""
	}
	translated := map[string]string{
		"startup.mode.clientServer":     labels.StartupModeClientServer,
		"startup.mode.clientServerHelp": labels.StartupModeClientServerHelp,
		"startup.mode.client":           labels.StartupModeClient,
		"startup.mode.clientHelp":       labels.StartupModeClientHelp,
		"startup.mode.server":           labels.StartupModeServer,
		"startup.mode.serverHelp":       labels.StartupModeServerHelp,
	}
	return translated[labelKey], translated[helpKey]
}

// updatesHTML renders the release check and the verified installer action.
func updatesHTML(view UpdateView) string {
	if view.Title == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="updates">`)
	out.WriteString(`<h2>` + pageIcon("download") + escape(view.Title) + `</h2>`)
	out.WriteString(`<dl class="diagnostics-versions">`)
	out.WriteString(`<div><dt>` + escape(view.Current) + `</dt><dd>` + escape(view.CurrentVersion) + `</dd></div>`)
	out.WriteString(`<div><dt>` + escape(view.Latest) + `</dt><dd>` + escape(valueOr(view.LatestVersion, "—")) + `</dd></div>`)
	out.WriteString(`</dl>`)
	out.WriteString(`<div class="field field-toggle" data-field="client.updates.check">`)
	out.WriteString(`<label class="toggle-row" for="field-client-updates-check">`)
	out.WriteString(`<span class="toggle-label">` + escape(view.AutoCheck) + `</span>`)
	out.WriteString(`<input type="checkbox" id="field-client-updates-check" class="switch" data-path="client.updates.check"`)
	if view.CheckEnabled {
		out.WriteString(` checked`)
	}
	out.WriteString(`>`)
	out.WriteString(`</label>`)
	out.WriteString(`<p class="help">` + escape(view.AutoCheckHelp) + `</p>`)
	out.WriteString(`</div>`)
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

// serverSettingsHTML renders the active Server's effective configuration as an
// editable form. Every field carries its absolute “server.*“ path; Go sends
// the collected patch to the Admin API, which validates it and leaves the file
// untouched on failure. Locked fields (environment or command-line overrides)
// are read-only.
func serverSettingsHTML(view servers.View, labels ServerLabels) string {
	var out strings.Builder
	out.WriteString(`<section class="group group-wide server-settings" data-section="server-settings">`)
	out.WriteString(`<h2>` + pageIcon("gear") + escape(labels.SettingsTitle) + cardHelpTip(labels.SettingsHelp) + `</h2>`)
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
	out.WriteString(`<section class="group group-wide" data-section="diagnostics">`)
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

// serverProfileNeedsRepair reports whether a profile is reachable but has no
// usable credential, so the page offers an explicit pairing again action.
func serverProfileNeedsRepair(state trust.ServerState) bool {
	switch state {
	case trust.StateMismatch, trust.StateUnpinned, trust.StateUnauthenticated:
		return true
	default:
		return false
	}
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
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
	case dictation.KindTheme:
		writeTheme(out, id, field, disabled)
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

// writeTheme renders the theme as a three-way segmented picker. A hidden
// input carries the value for the generic form collector, while the buttons
// apply the palette immediately and save the choice in one intent.
func writeTheme(out *strings.Builder, id string, field dictation.Field, disabled string) {
	out.WriteString(`<div class="theme-options" role="group" aria-label="` + escape(field.Label) + `">`)
	for _, option := range field.Options {
		active := ""
		pressed := "false"
		if option.Value == field.Value {
			active = " is-active"
			pressed = "true"
		}
		out.WriteString(`<button type="button" class="theme-option` + active +
			`" data-theme-choice="` + escape(option.Value) + `" aria-pressed="` + pressed +
			`"` + disabled + `>` + themeIcon(option.Value) +
			`<span>` + escape(option.Label) + `</span></button>`)
	}
	out.WriteString(`</div>`)
	out.WriteString(`<input type="hidden" id="` + escape(id) + `" data-path="` + escape(field.Path) +
		`" value="` + escape(field.Value) + `"` + disabled + `>`)
}

// themeIcon draws the segmented picker's glyph. The paths are inline so the
// page keeps no image assets and inherits the current colour.
func themeIcon(value string) string {
	const prefix = `<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">`
	switch value {
	case "light":
		return prefix + `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M19.1 4.9l-1.4 1.4M6.3 17.7l-1.4 1.4"/></svg>`
	case "dark":
		return prefix + `<path d="M20.5 14.6A8.5 8.5 0 0 1 9.4 3.5a8.5 8.5 0 1 0 11.1 11.1z"/></svg>`
	default:
		return prefix + `<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>`
	}
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
<html lang="__LANG__" data-theme="__THEME__">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<style>
  :root {
    color-scheme: light;
    --bg:#f7f2eb; --bg-soft:#efe8de; --card:#fffdf9; --card-2:#f8f2e9;
    --fg:#2a211b; --muted:#7c6f63; --line:#e3d9cb; --line-soft:#eee6da;
    --accent:#c2703f; --accent-strong:#a95a2d; --accent-soft:#c2703f14; --on-accent:#fffaf5;
    --ok:#3f7d4a; --warn:#a9661d; --danger:#b34a49; --knob:#fffdf9; --radius:9px;
    --shadow:0 1px 2px rgba(60,42,28,.07);
  }
  /* The dark scheme is a warm charcoal rather than the cold blue-black of the
     first palette: amber accent, paper-white text, and brown-grey hairlines. */
  :root[data-theme="dark"] {
    color-scheme: dark;
    --bg:#17130f; --bg-soft:#211b16; --card:#1d1814; --card-2:#29221b;
    --fg:#f2ebe2; --muted:#b0a08f; --line:#3b3128; --line-soft:#2c251e;
    --accent:#e9a878; --accent-strong:#f3bc94; --accent-soft:#e9a8781f; --on-accent:#24170c;
    --ok:#92d18d; --warn:#f2c14e; --danger:#e2725b; --knob:#f2ebe2;
    --shadow:0 1px 2px rgba(0,0,0,.28);
  }
  @media (prefers-color-scheme: dark) {
    :root[data-theme="system"] {
      color-scheme: dark;
      --bg:#17130f; --bg-soft:#211b16; --card:#1d1814; --card-2:#29221b;
      --fg:#f2ebe2; --muted:#b0a08f; --line:#3b3128; --line-soft:#2c251e;
      --accent:#e9a878; --accent-strong:#f3bc94; --accent-soft:#e9a8781f; --on-accent:#24170c;
      --ok:#92d18d; --warn:#f2c14e; --danger:#e2725b; --knob:#f2ebe2;
      --shadow:0 1px 2px rgba(0,0,0,.28);
    }
  }
  * { box-sizing: border-box; }
  html, body { height:100%; }
  body { margin:0; font: 13px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); overflow:hidden; -webkit-font-smoothing:antialiased; }
  main { height:100vh; padding:14px 18px 10px; display:flex; flex-direction:column; gap:10px; }
  .icon { width:15px; height:15px; flex:0 0 auto; fill:none; stroke:currentColor; stroke-width:1.7; stroke-linecap:round; stroke-linejoin:round; }
  .card, .group { background:var(--card); border:1px solid var(--line); border-radius:var(--radius); }
  .group { margin:0; padding:14px 16px; }
  .status { display:flex; align-items:center; gap:8px 16px; flex-wrap:wrap; padding:8px 12px; flex:0 0 auto; }
  .status > .state { margin-left:auto; }
  .status-meta { display:flex; gap:8px 16px; flex-wrap:wrap; margin:0; }
  .status-meta > div { display:flex; align-items:baseline; gap:5px; }
  .status-meta dt { color:var(--muted); font-size:11px; }
  .status-meta dd { margin:0; font-size:12px; overflow-wrap:anywhere; }
  .settings-layout { flex:1 1 auto; min-height:0; display:grid; grid-template-columns:206px minmax(0,1fr); gap:12px; }
  #settings { position:relative; z-index:1; flex:1 1 auto; min-height:0; display:flex; flex-direction:column; }
  .settings-single { grid-template-columns:minmax(0,1fr); }
  .settings-nav { display:flex; flex-direction:column; gap:2px; overflow-y:auto; overflow-x:hidden; min-height:0; padding:2px 4px 2px 0; scrollbar-width:thin; }
  .nav-item { display:flex; align-items:center; justify-content:flex-start; gap:8px; font:inherit; text-align:left; border:1px solid transparent; background:transparent; color:var(--muted); border-radius:7px; padding:6px 9px; cursor:pointer; transition:background .12s, color .12s; }
  .nav-item .icon { width:14px; height:14px; opacity:.85; }
  .nav-item:hover { background:var(--bg-soft); color:var(--fg); }
  .nav-item.is-active { background:var(--accent-soft); color:var(--accent); font-weight:600; }
  .nav-item.is-active .icon { opacity:1; }
  .settings-pane { display:flex; flex-direction:column; gap:10px; min-height:0; overflow-y:auto; overflow-x:hidden; padding:2px 6px 8px 2px; scrollbar-width:thin; }
  /* Short cards share a line and grow to fill it, so two cards split the row
     and three split it three ways. Wide content marks itself .group-wide and
     always starts a line of its own. Cards on one line stretch to one height. */
  .settings-layout:not(.settings-single) .settings-pane { flex-direction:row; flex-wrap:wrap; align-content:flex-start; align-items:stretch; }
  .settings-layout:not(.settings-single) .settings-pane > .group { flex:1 1 300px; min-width:0; }
  .settings-layout:not(.settings-single) .settings-pane > .group-wide { flex:1 1 100%; }
  .settings-layout:not(.settings-single) .settings-pane > .config-path { flex:1 1 100%; }
  /* Three short General-screen cards share one row; the taller autostart
     card follows at full width instead of stretching a nearly empty card
     beside it. */
  .settings-pane > [data-section='updates'] { order:1; }
  .settings-pane > [data-section='wizard-launcher'] { order:2; }
  .settings-pane > [data-section='startup'] { order:3; }
  .settings-pane > .config-path { order:4; }
  /* The General screen's action buttons fill their card and share one height,
     so the update check and the wizard entry point do not look mismatched. */
  .settings-pane > [data-section='updates'] .actions-inline > button {
    flex:1 1 0;
    min-width:0;
    justify-content:center;
    min-height:32px;
  }
  .settings-pane > [data-section='wizard-launcher'] > button {
    width:100%;
    margin-top:10px;
    justify-content:center;
    min-height:32px;
  }
  /* The microphone test and stop buttons share one width, like the other
     card actions. */
  .settings-pane > [data-section='dictation-microphone'] .mic-actions > button {
    flex:1 1 0;
    min-width:0;
    justify-content:center;
    min-height:32px;
  }
  /* A card with many short fields uses two columns of fields, so it stays
     compact without turning every input into a half-width control. */
  .field-grid { display:grid; grid-template-columns:repeat(auto-fit, minmax(240px, 1fr)); gap:2px 20px; align-items:start; }
  .field-grid > .field { border-top:none; padding:6px 0; }
  .field-grid > .gnome-offer { grid-column:1 / -1; margin:6px 0 2px; }
  @media (max-width: 900px) {
    .server-fields { grid-template-columns:minmax(0, 1fr); }
  }
  /* Author display rules (notably the flex Server form) otherwise beat the
     user-agent [hidden] rule, so the Server screen stayed visible on every
     other screen. */
  .settings-pane [data-section][hidden] { display:none !important; }
  .settings-pane > .group { flex:0 0 auto; }
  .server-fields { display:grid; grid-template-columns:repeat(2, minmax(0, 1fr)); gap:2px 20px; align-items:start; }
  .server-fields > h3 { grid-column:1 / -1; }
  .server-fields > .field { border-top:none; padding:6px 0; }
  .server-save { display:flex; justify-content:flex-end; margin-top:10px; padding-top:10px; border-top:1px solid var(--line-soft); }
  .settings-single .settings-pane { overflow:hidden; }
  .settings-single .wizard { flex:1 1 auto; display:grid; grid-template-columns:224px minmax(0,1fr); min-height:0; padding:0; overflow:hidden; }
  /* The rail carries the sequence; the main column carries one task. The
     heading and the rail actions stay put: only a long step list scrolls. */
  .wizard-rail { display:flex; flex-direction:column; gap:12px; min-height:0; padding:14px 12px; background:var(--card-2); border-right:1px solid var(--line-soft); overflow:hidden; }
  .wizard-rail-head { flex:0 0 auto; }
  .wizard-rail-head h2 { margin:0 0 3px; }
  .wizard-subtitle { margin:1px 0 0; color:var(--muted); font-size:11px; line-height:1.35; }
  .wizard-main { display:flex; flex-direction:column; min-width:0; min-height:0; padding:14px 18px 12px; }
  .wizard-body { flex:1 1 auto; min-height:0; overflow-y:auto; padding:0 4px 6px 0; scrollbar-width:thin; }
  .wizard-content { max-width:720px; margin:0 auto; }
  .group h2 { display:flex; align-items:center; gap:7px; font-size:14px; margin:0 0 10px; letter-spacing:-.005em; }
  .group h2 .icon { color:var(--accent); }
  .group h3 { display:flex; align-items:center; gap:6px; font-size:10.5px; margin:14px 0 5px; color:var(--muted); text-transform:uppercase; letter-spacing:.07em; }
  .card-head { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:8px; }
  .card-head h2 { margin:0; }
  .card-head-actions { display:flex; align-items:center; gap:6px; flex-wrap:wrap; }
  /* A card explanation lives behind the question mark beside its heading, so
     a long paragraph never pushes the actual controls down. */
  .help-tip { position:relative; display:inline-flex; align-items:center; justify-content:center; width:15px; height:15px; flex:0 0 auto; border:1px solid var(--line); border-radius:50%; color:var(--muted); font-size:10px; font-weight:600; line-height:1; cursor:help; user-select:none; }
  .help-tip:hover, .help-tip:focus-visible { color:var(--accent); border-color:var(--accent); background:var(--accent-soft); outline:none; }
  /* One floating node is positioned in script, so a hint picks its side from
     the space actually left in the settings pane instead of overflowing it. */
  #help-tip-float { position:fixed; z-index:80; display:none; width:max-content; max-width:min(320px, calc(100vw - 24px)); padding:8px 10px; border:1px solid var(--line); border-radius:7px; background:var(--card); color:var(--fg); font-size:11.5px; font-weight:400; line-height:1.4; letter-spacing:normal; text-align:left; white-space:pre-line; overflow-wrap:anywhere; box-shadow:0 10px 28px rgba(0,0,0,.35); pointer-events:none; }
  #help-tip-float.is-open { display:block; }
  .icon-button { width:30px; height:30px; min-height:30px; padding:0; justify-content:center; flex:0 0 auto; }
  .icon-button.danger { color:var(--danger); }
  .icon-button.danger:hover { background:color-mix(in srgb, var(--danger) 12%, transparent); border-color:color-mix(in srgb, var(--danger) 28%, transparent); }
  .profile-list { display:flex; flex-direction:column; gap:6px; }
  .profile-row { display:grid; grid-template-columns:minmax(0,1fr) auto auto; align-items:center; gap:10px; padding:8px 10px; border:1px solid var(--line-soft); border-radius:7px; background:var(--card-2); }
  .profile-main { min-width:0; display:flex; flex-direction:column; gap:1px; }
  .profile-title { display:flex; align-items:center; gap:7px; flex-wrap:wrap; }
  .profile-url { color:var(--muted); overflow-wrap:anywhere; font-size:11.5px; }
  .profile-actions { display:flex; align-items:center; gap:4px; }
  .profile-row .health { white-space:nowrap; }
  .card-fields { display:flex; flex-direction:column; gap:0; }
  .card-fields > .field { border-top:none; padding:5px 0; }
  .card-action { width:100%; justify-content:center; margin-top:8px; }
  .stat-line { display:flex; align-items:center; justify-content:space-between; gap:8px; margin:8px 0 0; padding:7px 9px; border-radius:6px; background:var(--card-2); font-size:12px; }
  .stat-line span { color:var(--muted); }
  .actions-equal > button { flex:1 1 0; min-width:0; justify-content:center; }
  /* Three long lifecycle labels never fit side by side in a half-width
     card. A full-width stack keeps every icon and label on one line. */
  .server-actions { display:flex; flex-direction:column; gap:8px; margin-top:8px; }
  .server-actions > button { width:100%; justify-content:flex-start; white-space:nowrap; }
  .state { display:flex; align-items:center; gap:8px; }
  .dot { width:10px; height:10px; border-radius:50%; background:var(--muted); box-shadow:0 0 0 3px color-mix(in srgb, var(--muted) 25%, transparent); }
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
  .field input[type="text"], .field input[type="number"], .wizard textarea { width:100%; min-height:30px; padding:6px 9px; border:1px solid var(--line); border-radius:6px; background:var(--card-2); color:var(--fg); font:inherit; font-size:12.5px; line-height:1.35; transition:border-color .12s, box-shadow .12s; }
  .field select { width:100%; min-height:30px; padding:6px 30px 6px 9px; border:1px solid var(--line); border-radius:6px; background-color:var(--card-2); background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%239a8c7d' stroke-width='2.2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E"); background-repeat:no-repeat; background-position:right 9px center; background-size:12px 12px; color:var(--fg); font:inherit; font-size:12.5px; line-height:1.35; appearance:none; -webkit-appearance:none; cursor:pointer; transition:border-color .12s, box-shadow .12s; }
  .field input[type="text"]:focus, .field input[type="number"]:focus, .field select:focus, .wizard textarea:focus { outline:none; border-color:var(--accent); box-shadow:0 0 0 3px var(--accent-soft); }
  .field input[type="number"] { -moz-appearance:textfield; appearance:textfield; }
  .field input[type="number"]::-webkit-outer-spin-button, .field input[type="number"]::-webkit-inner-spin-button { -webkit-appearance:none; margin:0; }
  .field .switch { appearance:none; -webkit-appearance:none; width:36px; height:20px; margin:0; padding:0; border:1px solid var(--line); border-radius:999px; background-color:var(--bg-soft); background-image:radial-gradient(circle, var(--knob) 0 6.5px, transparent 7px); background-size:14px 14px; background-repeat:no-repeat; background-position:2px center; cursor:pointer; flex:0 0 auto; box-shadow:inset 0 0 0 1px rgba(0,0,0,.04); transition:background-color .15s, border-color .15s, background-position .15s; }
  .field .switch:checked { background-color:var(--accent); border-color:transparent; background-position:18px center; }
  .field .switch:disabled { opacity:.55; cursor:not-allowed; }
  .field-toggle { padding:10px 0; }
  .startup-modes { border:0; margin:6px 0 0; padding:0; min-width:0; }
  .startup-modes legend { padding:0; margin-bottom:5px; color:var(--muted); font-size:11.5px; }
  .startup-modes:disabled { opacity:.55; }
  .startup-mode { display:flex; align-items:flex-start; gap:8px; padding:7px 0; border-top:1px solid var(--line-soft); cursor:pointer; }
  .startup-mode:first-of-type { border-top:none; }
  .startup-mode input { margin:3px 0 0; flex:0 0 auto; accent-color:var(--accent); }
  .startup-mode-text { display:flex; flex-direction:column; gap:2px; }
  .startup-mode-label { font-weight:600; font-size:12.5px; }
  .startup-mode .help { margin:0; }
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
  /* A three-way segmented picker: the selected tile is raised out of the
     track, so the choice reads at a glance without opening a select. */
  .theme-options { display:grid; grid-template-columns:repeat(3, minmax(0,1fr)); gap:4px; padding:3px; border:1px solid var(--line); border-radius:8px; background:var(--bg-soft); }
  .theme-option { display:flex; flex-direction:column; align-items:center; justify-content:center; gap:5px; min-height:54px; padding:6px 4px; border:1px solid transparent; border-radius:6px; background:transparent; color:var(--muted); font-size:11.5px; font-weight:500; }
  .theme-option .icon { width:18px; height:18px; }
  .theme-option:hover { background:var(--card-2); color:var(--fg); }
  .theme-option.is-active { background:var(--card); border-color:var(--line); color:var(--fg); font-weight:600; box-shadow:var(--shadow); }
  .theme-option.is-active .icon { color:var(--accent); }
  .theme-option:disabled { opacity:.55; cursor:not-allowed; }
  .toggle-row { display:flex; align-items:center; justify-content:space-between; gap:12px; cursor:pointer; }
  .toggle-label { display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-weight:600; font-size:12.5px; }
  .inline-field { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
  .inline-field input[type="number"] { width:104px; flex:0 0 104px; }
  .field input[disabled], .field select[disabled], .field button[disabled] { opacity:.55; cursor:not-allowed; }
  .help { color:var(--muted); margin:4px 0 0; font-size:11.5px; }
  .lock { color:var(--warn); margin:4px 0 0; font-size:11.5px; }
  .badge { font-size:10.5px; padding:1px 7px; border-radius:999px; background:color-mix(in srgb, var(--warn) 16%, transparent); color:var(--warn); }
  .badge.hot { background:color-mix(in srgb, var(--ok) 14%, transparent); color:var(--ok); }
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
  .gnome-offer { margin:6px 0 2px; padding:10px 12px; border:1px solid var(--line); border-radius:8px; background:var(--card-2); }
  .gnome-head { display:flex; align-items:center; gap:7px; margin-bottom:2px; }
  .gnome-head .icon { color:var(--accent); }
  .gnome-head h3 { margin:0; font-size:12.5px; font-weight:600; color:var(--fg); text-transform:none; letter-spacing:0; }
  .gnome-offer .help { margin-top:2px; }
  .gnome-offer .actions-inline { margin:8px 0 0; }
  /* The shortcut offer is a wizard panel already; an extra margin and a
     smaller padding made it start lower and end higher than the card beside
     it. */
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
  /* Catalog row actions are icon buttons, so every row keeps one narrow
     action column while a status badge stays beside the model name. */
  .models .model-actions .icon-button { width:30px; justify-content:center; }
  .model-state { margin:8px 0 0; }
  .messages { position:relative; z-index:3; display:flex; flex-direction:column; gap:5px; flex:0 0 auto; width:100%; max-height:20vh; overflow-y:auto; background:var(--bg); }
  .messages:empty { display:none; }
  .messages .notice, .messages .failure { display:flex; align-items:flex-start; gap:8px; }
  .message-text { min-width:0; overflow-wrap:anywhere; }
  .message-close { width:20px; height:20px; min-height:0; flex:0 0 auto; padding:0; margin:1px 0 0; border:0; border-radius:5px; background:transparent; color:inherit; opacity:.65; }
  .message-close:hover { opacity:1; background:color-mix(in srgb, currentColor 12%, transparent); }
  .message-close .icon { width:12px; height:12px; }
  .notice { color:var(--ok); margin:0; padding:6px 10px; border-radius:7px; background:color-mix(in srgb, var(--ok) 12%, transparent); font-size:12px; }
  .failure { color:var(--danger); margin:0; padding:6px 10px; border-radius:7px; background:color-mix(in srgb, var(--danger) 10%, transparent); font-size:12px; }
  .failure:empty { display:none; }
  .health { font-size:11px; color:var(--muted); }
  .health[data-health="online"] { color:var(--ok); }
  .health[data-health="stopped"] { color:var(--warn); }
  .health[data-health="offline"], .health[data-health="unauthenticated"], .health[data-health="mismatch"] { color:var(--danger); }
  .wizard-progress { color:var(--muted); font-size:11.5px; margin:0 0 3px; }
  .wizard-steps { flex:1 1 auto; min-height:0; list-style:none; display:flex; flex-direction:column; gap:1px; padding:0 2px 0 0; margin:0; overflow-y:auto; overflow-x:hidden; scrollbar-width:thin; }
  .wizard-steps li { position:relative; display:flex; align-items:center; gap:9px; min-height:28px; padding:3px 7px; border-radius:7px; color:var(--muted); }
  .wizard-steps li:not(:last-child)::before { content:""; position:absolute; left:15px; top:22px; bottom:-1px; width:1px; background:var(--line); }
  .wizard-step-number { position:relative; z-index:1; display:inline-flex; align-items:center; justify-content:center; flex:0 0 18px; width:18px; height:18px; border-radius:999px; border:1px solid var(--line); background:var(--card); font-size:10px; }
  .wizard-step-title { min-width:0; font-size:12px; font-weight:600; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .wizard-steps li[data-status="current"] { background:var(--accent-soft); color:var(--accent); }
  .wizard-steps li[data-status="current"] .wizard-step-number { background:var(--accent); color:var(--on-accent); border-color:transparent; }
  .wizard-steps li[data-status="done"] .wizard-step-number { background:color-mix(in srgb, var(--ok) 14%, transparent); color:var(--ok); border-color:transparent; }
  .wizard-steps li[data-status="skipped"] .wizard-step-title { text-decoration:line-through; }
  .wizard-rail-actions { flex:0 0 auto; display:flex; flex-direction:column; align-items:stretch; gap:1px; padding-top:8px; border-top:1px solid var(--line); }
  .wizard-rail-actions button { justify-content:flex-start; }
  button.ghost { background:transparent; border-color:transparent; color:var(--muted); padding:4px 6px; }
  button.ghost:hover { background:var(--bg-soft); color:var(--fg); }
  .wizard h3 { display:flex; align-items:center; gap:7px; font-size:15px; margin:0 0 4px; color:var(--fg); letter-spacing:-.005em; text-transform:none; }
  .wizard-lede { color:var(--muted); font-size:12px; margin:0 0 10px; max-width:66ch; }
  .wizard h4 { font-size:12.5px; margin:0 0 5px; color:var(--fg); }
  .wizard-panel { padding:12px 14px; border:1px solid var(--line); border-radius:9px; background:var(--card-2); }
  .wizard-panel > :last-child { margin-bottom:0; }
  .wizard-columns { display:grid; grid-template-columns:repeat(auto-fit, minmax(250px, 1fr)); gap:12px; align-items:stretch; }
  .wizard-panel .field { border-top:none; padding:5px 0; }
  .wizard-panel .field + .field { border-top:1px solid var(--line-soft); padding-top:9px; }
  /* Actions that sit directly in a panel, and any action stack, fill the
     card width so two stacked buttons never look like different sizes. */
  .wizard-panel > button, .wizard-actions > button, .shortcut-offer .actions-inline > button { width:100%; justify-content:center; }
  .wizard-panel .actions-inline { display:flex; align-items:stretch; gap:6px; }
  .wizard-panel .actions-inline > button { flex:1 1 0; justify-content:center; }
  .shortcut-offer .actions-inline { flex-direction:column; }
  .shortcut-offer .actions-inline > button { flex:0 0 auto; }
  .wizard-actions { display:flex; flex-direction:column; align-items:stretch; gap:6px; margin:10px 0 0; }
  /* Paired forms share a height; pin their primary buttons to one line so the
     two cards read as one row instead of two unrelated stacks. */
  .wizard-panel-stack { display:flex; flex-direction:column; }
  .wizard-panel-stack > button:last-child { margin-top:auto; }
  /* The transcript shares the card height with the controls beside it. */
  .wizard-panel-fill { display:flex; flex-direction:column; }
  .wizard-panel-fill > .field { flex:1 1 auto; display:flex; flex-direction:column; min-height:0; }
  .wizard-panel-fill textarea { flex:1 1 auto; min-height:112px; }
  .wizard-facts { list-style:none; display:grid; grid-template-columns:repeat(2, minmax(0,1fr)); gap:8px; padding:0; margin:0; }
  .wizard-facts li { padding:9px 11px; border:1px solid var(--line); border-radius:8px; background:var(--card-2); font-size:12.5px; font-weight:600; overflow-wrap:anywhere; }
  .wizard-table { margin-top:8px; border:1px solid var(--line); border-radius:9px; overflow-x:auto; }
  .wizard-table .models { margin:0; }
  .wizard-table .models th, .wizard-table .models td { padding:5px 10px; }
  .wizard-try-status { display:flex; align-items:center; gap:7px; flex-wrap:wrap; margin:0 0 10px; font-size:12px; color:var(--muted); }
  .wizard-try-label { opacity:.75; }
  .wizard-sep { opacity:.5; }
  .wizard-panel .help { max-width:62ch; }
  .wizard-missing { margin:6px 0; padding-left:18px; font-size:12px; }
  .wizard-footer { flex:0 0 auto; display:flex; align-items:center; gap:8px; flex-wrap:wrap; width:100%; max-width:720px; margin:10px auto 0; padding-top:10px; border-top:1px solid var(--line-soft); }
  .wizard-footer-actions { display:flex; align-items:center; gap:4px; flex-wrap:wrap; margin-left:auto; }
  .wizard-footer-actions .primary { margin-left:8px; }
  .wizard textarea { resize:vertical; }
  @media (max-height: 600px) {
    /* Seven single-line steps fit even the shortest window: drop the
       explanatory subtitle and tighten the rail instead of scrolling it. */
    .wizard-rail { gap:8px; padding:10px 12px; }
    .wizard-subtitle { display:none; }
    .wizard-rail-actions { padding-top:6px; }
  }
  @media (max-width: 820px) {
    .settings-single .wizard { grid-template-columns:minmax(0,1fr); grid-template-rows:auto minmax(0,1fr); }
    .wizard-rail { flex-direction:row; flex-wrap:wrap; align-items:center; gap:8px 12px; padding:10px 12px; border-right:none; border-bottom:1px solid var(--line-soft); overflow:visible; }
    .wizard-rail-head { display:flex; align-items:baseline; gap:8px; flex-wrap:wrap; }
    .wizard-rail-head h2 { margin:0; }
    .wizard-subtitle { display:none; }
    .wizard-steps { flex:0 0 auto; flex-direction:row; flex-wrap:wrap; gap:2px 4px; padding:0; overflow:visible; }
    .wizard-steps li:not(:last-child)::before { display:none; }
    .wizard-rail-actions { flex:0 0 auto; margin-top:0; margin-left:auto; flex-direction:row; align-items:center; padding-top:0; border-top:none; }
    .wizard-rail-actions button { width:auto; }
    .wizard-facts { grid-template-columns:minmax(0,1fr); }
  }
  footer { display:flex; gap:8px; align-items:center; flex:0 0 auto; padding-top:10px; border-top:1px solid var(--line); }
  button { display:inline-flex; align-items:center; justify-content:center; gap:6px; min-height:30px; font:inherit; font-size:12.5px; padding:5px 10px; border-radius:6px; border:1px solid var(--line); background:var(--card-2); color:var(--fg); cursor:pointer; transition:background .12s, border-color .12s, opacity .12s; }
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
  <section class="card status" aria-live="polite">
    <dl class="status-meta">
      <div><dt id="label-phase">—</dt><dd id="phase">idle</dd></div>
      <div><dt id="label-running">—</dt><dd id="running">—</dd></div>
      <div><dt id="label-server">—</dt><dd id="server">—</dd></div>
      <div><dt id="label-server-status">—</dt><dd id="server-status">—</dd></div>
      <div><dt id="label-version">—</dt><dd id="version">—</dd></div>
    </dl>
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">—</strong></div>
  </section>
  <div class="messages" id="messages"></div>
  <div id="settings">__SETTINGS__</div>
  <footer id="actions"__ACTIONS_STYLE__>
    <button type="button" class="primary" data-action="save" id="save-button"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M5 12.5l4.3 4.3L19 6.8"/></svg><span class="btn-label">__SAVE__</span></button>
    <button type="button" data-action="restart" id="restart-button"><svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M20 12a8 8 0 1 1-2.6-5.9"/><path d="M20 3.8v4.6h-4.6"/></svg><span class="btn-label">__RESTART__</span></button>
    <span class="hint" id="restart-hint">__RESTART_HINT__</span>
  </footer>
</main>
<div id="help-tip-float" role="tooltip" aria-hidden="true"></div>
<script>
  function text(id, value) {
    var el = document.getElementById(id);
    if (el) el.textContent = value || "";
  }
  // The server renders the initial choice into data-theme; every later push
  // and every click re-applies it here. "system" leaves the palette to the
  // prefers-color-scheme rule, so a desktop switch needs no round trip.
  function applyTheme(value) {
    document.documentElement.setAttribute("data-theme",
      value === "light" || value === "dark" ? value : "system");
  }
  // The status card is followed by one message area. Runtime failures, card
  // failures and intent results all land here; cards never repeat an error of
  // their own. viewErrors carry the screen they belong to, so an error for a
  // hidden card stays hidden until the user opens that screen.
  var runtimeError = "";
  var viewErrors = __ERRORS__;
  var actionMessages = { notices: [], failures: [] };
  var dismissedAlerts = {};
  var closeNoticeLabel = __CLOSE_LABEL__;
  var lastAlerts = null;
  // Help hints live in one fixed node. A pseudo-element could not measure the
  // pane, so it guessed a side and spilled over the edge at some widths.
  var helpTipFloat = document.getElementById("help-tip-float");
  function hideHelpTip() {
    if (!helpTipFloat) return;
    helpTipFloat.classList.remove("is-open");
    helpTipFloat.setAttribute("aria-hidden", "true");
  }
  function placeHelpTip(target) {
    if (!helpTipFloat || !target) return;
    var text = target.getAttribute("data-tip") || "";
    if (!text) return;
    helpTipFloat.textContent = text;
    helpTipFloat.style.maxWidth = "";
    helpTipFloat.classList.add("is-open");
    helpTipFloat.setAttribute("aria-hidden", "false");
    var r = target.getBoundingClientRect();
    var pane = target.closest(".settings-pane");
    var bounds = pane ? pane.getBoundingClientRect() : {
      left: 0,
      right: window.innerWidth
    };
    var margin = 12;
    var gap = 8;
    var available = Math.max(80, bounds.right - bounds.left - margin * 2);
    var box = helpTipFloat.getBoundingClientRect();
    if (box.width > available) {
      helpTipFloat.style.maxWidth = available + "px";
      box = helpTipFloat.getBoundingClientRect();
    }
    var top = r.bottom + gap;
    // Flip above the icon when the hint would leave the window at the bottom.
    if (top + box.height > window.innerHeight - margin && r.top - gap - box.height >= margin) {
      top = r.top - gap - box.height;
    }
    if (top + box.height > window.innerHeight - margin) {
      top = Math.max(margin, window.innerHeight - margin - box.height);
    }
    // Centre on the icon, then slide back inside the settings pane. The pane
    // is narrower than the window, so clamping to it keeps the hint off the
    // navigation on the left and off the edge on the right.
    var left = r.left + r.width / 2 - box.width / 2;
    var minLeft = bounds.left + margin;
    var maxLeft = bounds.right - margin - box.width;
    if (left > maxLeft) left = maxLeft;
    if (left < minLeft) left = minLeft;
    helpTipFloat.style.top = Math.round(top) + "px";
    helpTipFloat.style.left = Math.round(left) + "px";
  }
  function helpTipTarget(node) {
    return node && node.closest ? node.closest(".help-tip") : null;
  }
  document.addEventListener("mouseover", function (event) {
    var target = helpTipTarget(event.target);
    if (target) placeHelpTip(target);
  });
  document.addEventListener("mouseout", function (event) {
    var target = helpTipTarget(event.target);
    if (target && !target.contains(event.relatedTarget)) hideHelpTip();
  });
  document.addEventListener("focusin", function (event) {
    var target = helpTipTarget(event.target);
    if (target) placeHelpTip(target);
  });
  document.addEventListener("focusout", function (event) {
    if (helpTipTarget(event.target)) hideHelpTip();
  });
  document.addEventListener("scroll", hideHelpTip, true);
  window.addEventListener("resize", hideHelpTip);
  function visibleSections() {
    var visible = {};
    var sections = document.querySelectorAll("#settings-pane [data-section]");
    for (var i = 0; i < sections.length; i++) {
      if (!sections[i].hidden) visible[sections[i].dataset.section] = true;
    }
    return visible;
  }
  function alertItems() {
    var items = [];
    var shown = {};
    function add(message, kind) {
      message = message || "";
      var key = kind + "\n" + message;
      if (!message || shown[key] || dismissedAlerts[key]) return;
      shown[key] = true;
      items.push({ kind: kind, message: message, key: key });
    }
    (actionMessages.notices || []).forEach(function (line) { add(line, "notice"); });
    if (runtimeError) add(runtimeError, "failure");
    var visible = visibleSections();
    (viewErrors || []).forEach(function (item) {
      if (!item || !item.message) return;
      if (item.section && !visible[item.section]) return;
      add(item.message, "failure");
    });
    (actionMessages.failures || []).forEach(function (line) { add(line, "failure"); });
    return items;
  }
  function alertSignature(items) {
    var parts = [];
    for (var i = 0; i < items.length; i++) {
      parts.push(items[i].kind + "\n" + items[i].message);
    }
    return parts.join("\n\u001e\n");
  }
  function renderAlerts() {
    var host = document.getElementById("messages");
    if (!host) return;
    var items = alertItems();
    var signature = alertSignature(items);
    // A status tick must not clear and rebuild an unchanged banner. The
    // embedded WebKit compositor paints the moving scroll layer over the
    // navigation when this list churns on every health probe.
    if (signature === lastAlerts) return;
    lastAlerts = signature;
    host.textContent = "";
    for (var i = 0; i < items.length; i++) {
      (function (item) {
        var p = document.createElement("p");
        p.className = item.kind;
        var label = document.createElement("span");
        label.className = "message-text";
        label.textContent = item.message;
        var close = document.createElement("button");
        close.type = "button";
        close.className = "message-close";
        close.title = closeNoticeLabel;
        close.setAttribute("aria-label", closeNoticeLabel);
        close.innerHTML = '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M6 6l12 12M18 6L6 18"/></svg>';
        close.addEventListener("click", function () {
          dismissedAlerts[item.key] = true;
          p.remove();
          lastAlerts = alertSignature(alertItems());
        });
        p.appendChild(label);
        p.appendChild(close);
        host.appendChild(p);
      })(items[i]);
    }
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
    runtimeError = s.error || "";
    renderAlerts();
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
      renderAlerts();
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
    renderAlerts();
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
  // The indicator settings can share a navigation entry with other screens,
  // so preview visibility follows the rendered section, not the entry key.
  function indicatorSectionVisible() {
    var section = document.querySelector("#settings-pane [data-section='appearance']");
    return Boolean(section && !section.hidden);
  }
  function sendIndicatorPreview() {
    var position = indicatorPreviewPosition();
    if (!position || !indicatorSectionVisible() || !previewWanted || document.hidden) return;
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
    if (!indicatorSectionVisible() || document.hidden) {
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
    if (el.closest("[data-startup-switch], [data-startup-mode], [data-log-level]")) return true;
    return el.dataset.path === "client.theme" || el.dataset.path === "client.ui_language" || el.dataset.path === "client.language" || el.dataset.path === "client.updates.check";
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
    // The hovered question mark is about to be replaced, so its hint must go
    // with it; a retained node would float over the rebuilt screen.
    hideHelpTip();
    var host = document.getElementById("settings");
    var pane = document.getElementById("settings-pane");
    var fields = document.querySelector("#settings .server-fields");
    var wizardBody = document.querySelector("#settings .wizard-body");
    var previousSection = activeSection;
    var previousScroll = pane ? pane.scrollTop : 0;
    var previousFields = fields ? fields.scrollTop : 0;
    var previousWizardBody = wizardBody ? wizardBody.scrollTop : 0;
    var edits = captureEdits(host);
    var focus = captureFocus(host);
    if (host) host.innerHTML = (v && v.html) || "";
    if (host) {
      markRendered(host);
      restoreEdits(host, edits);
    }
    viewErrors = (v && v.errors) || [];
    if (v) applyTheme(v.theme);
    if (v && v.language) document.documentElement.lang = v.language;
    if (v && v.title) document.title = v.title;
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
      var nextWizardBody = document.querySelector("#settings .wizard-body");
      if (nextPane) nextPane.scrollTop = previousScroll;
      if (nextFields) nextFields.scrollTop = previousFields;
      if (nextWizardBody) nextWizardBody.scrollTop = previousWizardBody;
      // WebKit can apply the new layout after the assignment; repeat on the
      // next frame so the position survives a taller or shorter render.
      if (window.requestAnimationFrame) {
        window.requestAnimationFrame(function () {
          if (nextPane && nextPane.isConnected) nextPane.scrollTop = previousScroll;
          if (nextFields && nextFields.isConnected) nextFields.scrollTop = previousFields;
          if (nextWizardBody && nextWizardBody.isConnected) nextWizardBody.scrollTop = previousWizardBody;
        });
      }
    }
    var wizardOutput = document.getElementById("wizard-try-output");
    if (wizardOutput && wizardOutput.focus) wizardOutput.focus();
    else restoreFocus(host, focus);
    revealWizardStep();
  }
  // The step list gets its own scrollbar in a short window. Keep the current
  // step in view instead of leaving the user at the top of an older list.
  function revealWizardStep() {
    var current = document.querySelector(".wizard-steps li[data-status='current']");
    if (!current) return;
    var list = current.closest(".wizard-steps");
    if (!list || list.scrollHeight <= list.clientHeight) return;
    var step = current.getBoundingClientRect();
    var frame = list.getBoundingClientRect();
    if (step.top < frame.top) list.scrollTop -= frame.top - step.top;
    else if (step.bottom > frame.bottom) list.scrollTop += step.bottom - frame.bottom;
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
    // A download progress tick re-renders the whole wizard. Remember where the
    // step content was scrolled so the table does not jump back to the top.
    var body = current ? current.querySelector(".wizard-body") : null;
    var bodyScroll = body ? body.scrollTop : 0;
    var steps = current ? current.querySelector(".wizard-steps") : null;
    var stepsScroll = steps ? steps.scrollTop : 0;
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
    var nextBody = rendered ? rendered.querySelector(".wizard-body") : null;
    var nextSteps = rendered ? rendered.querySelector(".wizard-steps") : null;
    if (nextBody) nextBody.scrollTop = bodyScroll;
    if (nextSteps) nextSteps.scrollTop = stepsScroll;
    if (window.requestAnimationFrame) {
      window.requestAnimationFrame(function () {
        if (nextBody && nextBody.isConnected) nextBody.scrollTop = bodyScroll;
        if (nextSteps && nextSteps.isConnected) nextSteps.scrollTop = stepsScroll;
      });
    }
    revealWizardStep();
  }
  function renderMessages(m) {
    actionMessages = {
      notices: (m && m.notices) || [],
      failures: (m && m.failures) || []
    };
    dismissedAlerts = {};
    lastAlerts = null;
    renderAlerts();
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
    actionMessages = {
      notices: failure ? [] : [String(message || "")],
      failures: failure ? [String(message || "")] : []
    };
    dismissedAlerts = {};
    lastAlerts = null;
    renderAlerts();
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
  var repairProfile = { id: "", url: "" };
  function prepareRemoteRepair(button) {
    repairProfile.id = button.dataset.profileId || "";
    repairProfile.url = button.dataset.profileUrl || "";
    var address = document.getElementById("server-address");
    if (address) address.value = repairProfile.url;
    var code = document.getElementById("server-code");
    if (code) {
      code.value = "";
      code.focus();
    }
  }
  function pairRemote(button) {
    function value(id) {
      var el = document.getElementById(id);
      return el ? el.value : "";
    }
    var address = value("server-address");
    var intent = {
      action: "pair-remote",
      address: address,
      code: value("server-code"),
      name: value("server-name")
    };
    if (repairProfile.id && repairProfile.url === address) {
      intent.profile_id = repairProfile.id;
    }
    sendIntent(intent, button);
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
    var themeChoice = event.target.closest ? event.target.closest("[data-theme-choice]") : null;
    if (themeChoice) {
      var themeField = themeChoice.closest("[data-field]");
      var themeInput = themeField ? themeField.querySelector("[data-path='client.theme']") : null;
      if (themeInput && themeInput.disabled) return;
      var themeValue = themeChoice.dataset.themeChoice || "system";
      if (themeInput && themeInput.value === themeValue) return;
      if (themeInput) themeInput.value = themeValue;
      var themeChoices = themeField ? themeField.querySelectorAll("[data-theme-choice]") : [];
      for (var t = 0; t < themeChoices.length; t++) {
        var selected = themeChoices[t] === themeChoice;
        themeChoices[t].classList.toggle("is-active", selected);
        themeChoices[t].setAttribute("aria-pressed", selected ? "true" : "false");
      }
      applyTheme(themeValue);
      sendIntent({ action: "save", patch: { theme: themeValue } });
      return;
    }
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
    } else if (action === "repair-profile") {
      if (button.dataset.profileLocal === "true") {
        var repairMessage = button.dataset.confirm || "";
        if (repairMessage && window.confirm && !window.confirm(repairMessage)) return;
        sendIntent({ action: "repair-local", profile_id: button.dataset.profileId || "" }, button);
      } else {
        prepareRemoteRepair(button);
      }
    } else if (action === "remove-profile") {
      var removeMessage = button.dataset.confirm || "";
      if (removeMessage && window.confirm && !window.confirm(removeMessage)) return;
      sendIntent({ action: "remove-profile", profile_id: button.dataset.profileId || "" }, button);
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
  function startupSelectedMode() {
    var checked = document.querySelector("[data-startup-mode]:checked");
    return checked ? checked.dataset.startupMode : "";
  }
  document.addEventListener("change", function (event) {
    var target = event.target;
    if (!target || !target.closest) return;
    var startupSwitch = target.closest("[data-startup-switch]");
    if (startupSwitch) {
      sendIntent({
        action: "set-startup",
        enabled: startupSwitch.checked,
        mode: startupSelectedMode()
      });
      return;
    }
    var startupMode = target.closest("[data-startup-mode]");
    if (startupMode) {
      sendIntent({
        action: "set-startup",
        enabled: true,
        mode: startupMode.dataset.startupMode
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
    } else if (el.dataset.path === "client.updates.check") {
      // The setting is hot-applied, so the toggle takes effect as soon as it
      // changes instead of waiting for the global Save button.
      sendIntent({ action: "save", patch: { updates: { check: el.checked } } });
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
  revealWizardStep();
  // Fill the Diagnostics cache when the window opens; the first visit to the
  // screen then shows real values instead of empty placeholders.
  primeDiagnostics();
</script>
</body>
</html>
`
