package shell

import (
	"fmt"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

// WizardHTML renders the active Setup wizard, or "" when it is hidden.
// UpdateWizard uses it to refresh only this section.
func WizardHTML(view wizard.View) string {
	if !view.Enabled || !view.Active {
		return ""
	}
	return wizardHTML(view)
}

// wizardHTML renders the wizard screen: step navigation, the current step, and
// the navigation footer. Every interpolated value is escaped; the page holds
// no validation or routing.
func wizardHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<section class="group wizard" data-section="wizard">`)
	out.WriteString(`<div class="wizard-head">`)
	out.WriteString(`<p class="wizard-progress" data-wizard-progress>` + escape(view.Progress) + `</p>`)
	out.WriteString(`<h2>` + pageIcon("wand") + escape(view.Labels.Title) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.Subtitle) + `</p>`)
	out.WriteString(wizardStepsHTML(view))
	out.WriteString(`</div>`)
	// The step body is the only scrollable part, so the navigation buttons
	// below it stay in the same place no matter how tall the step is.
	out.WriteString(`<div class="wizard-body">`)
	switch view.Step {
	case wizard.StepWelcome:
		out.WriteString(wizardWelcomeHTML(view))
	case wizard.StepMicrophone:
		out.WriteString(wizardMicHTML(view))
	case wizard.StepHotkey:
		out.WriteString(wizardHotkeyHTML(view))
	case wizard.StepHardware:
		out.WriteString(wizardHardwareHTML(view))
	case wizard.StepModel:
		out.WriteString(wizardModelHTML(view))
	case wizard.StepServer:
		out.WriteString(wizardServerHTML(view))
	case wizard.StepTry:
		out.WriteString(wizardTryHTML(view))
	}
	out.WriteString(`</div>`)
	out.WriteString(wizardFooterHTML(view))
	out.WriteString(`</section>`)
	return out.String()
}

func wizardStepsHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<ol class="wizard-steps">`)
	for _, step := range view.Steps {
		status := view.Labels.StepPending
		switch step.Status {
		case wizard.StatusDone:
			status = view.Labels.StepDone
		case wizard.StatusCurrent:
			status = view.Labels.StepCurrent
		case wizard.StatusSkipped:
			status = view.Labels.StepSkipped
		}
		out.WriteString(`<li data-step="` + escape(string(step.ID)) + `" data-status="` + escape(string(step.Status)) + `">`)
		out.WriteString(`<span class="wizard-step-number">` + fmt.Sprint(step.Number) + `</span>`)
		out.WriteString(`<span class="wizard-step-title">` + escape(step.Title) + `</span>`)
		out.WriteString(`<span class="wizard-step-status">` + escape(status) + `</span></li>`)
	}
	out.WriteString(`</ol>`)
	return out.String()
}

func wizardWelcomeHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<h3>` + escape(view.Labels.WelcomeTitle) + `</h3>`)
	out.WriteString(`<p>` + escape(view.Labels.WelcomeIntro) + `</p>`)
	out.WriteString(`<dl class="wizard-summary">`)
	for _, row := range [][2]string{
		{view.Labels.WelcomeClient, view.Version},
		{view.Labels.WelcomeProfile, view.ProfileName},
		{view.Labels.WelcomeServer, view.ServerState},
		{view.Labels.WelcomeModel, valueOr(view.ModelName, view.Labels.ModelNone)},
	} {
		value := valueOr(row[1], "—")
		out.WriteString(`<dt>` + escape(fmt.Sprintf(row[0], value)) + `</dt><dd>` + escape(value) + `</dd>`)
	}
	out.WriteString(`</dl>`)
	out.WriteString(`<div class="actions-inline"><button type="button" data-action="wizard-refresh">` +
		escape(view.Labels.WelcomeCheck) + `</button></div>`)
	return out.String()
}

func wizardMicHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<h3>` + escape(view.Labels.MicTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.MicHelp) + `</p>`)
	if len(view.Devices) == 0 {
		out.WriteString(`<p class="help">` + escape(view.Labels.MicNoDevices) + `</p>`)
	} else {
		out.WriteString(`<div class="field"><label for="wizard-device">` + escape(view.Labels.MicDevice) + `</label>`)
		out.WriteString(`<select id="wizard-device">`)
		for _, option := range view.Devices {
			out.WriteString(`<option value="` + escape(option.Value) + `"`)
			if option.Value == view.SelectedDevice {
				out.WriteString(` selected`)
			}
			out.WriteString(`>` + escape(option.Label) + `</option>`)
		}
		out.WriteString(`</select></div>`)
	}
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" data-action="wizard-mic-start">` + escape(view.Labels.MicTest) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-mic-stop">` + escape(view.Labels.MicStop) + `</button>`)
	out.WriteString(`</div>`)
	out.WriteString(`<div class="meter" role="meter" aria-label="` + escape(view.Labels.MicTest) +
		`" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"><div class="meter-fill"></div></div>`)
	if view.DeviceError != "" {
		out.WriteString(`<p class="failure">` + escape(fmt.Sprintf(view.Labels.MicListError, view.DeviceError)) + `</p>`)
	}
	return out.String()
}

func wizardHotkeyHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<h3>` + escape(view.Labels.HotkeyTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.HotkeyHelp) + `</p>`)
	out.WriteString(`<div class="field"><label for="wizard-hotkey">` + escape(view.Labels.HotkeyLabel) + `</label>`)
	out.WriteString(`<input type="text" id="wizard-hotkey" data-wizard-hotkey="true" autocomplete="off" spellcheck="false" value="` +
		escape(view.Hotkey) + `" placeholder="` + escape(view.Labels.HotkeyPlaceholder) + `"></div>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.HotkeyConflictNote) + `</p>`)
	if offer := view.ShortcutOffer; offer.Enabled {
		out.WriteString(`<div class="shortcut-offer" data-wizard-shortcuts>`)
		out.WriteString(`<h4>` + escape(offer.Title) + `</h4>`)
		out.WriteString(`<p class="help">` + escape(offer.Help) + `</p>`)
		if offer.Error != "" {
			out.WriteString(`<p class="failure">` + escape(offer.Error) + `</p>`)
		}
		out.WriteString(`<div class="mic-actions">`)
		if offer.Add != "" {
			out.WriteString(`<button type="button" class="primary" data-action="gnome-shortcuts-add">` + escape(offer.Add) + `</button>`)
		}
		if offer.Installed && offer.Remove != "" {
			out.WriteString(`<button type="button" data-action="gnome-shortcuts-remove">` + escape(offer.Remove) + `</button>`)
		}
		out.WriteString(`</div></div>`)
	}
	return out.String()
}

func wizardHardwareHTML(view wizard.View) string {
	var out strings.Builder
	hardware := view.Hardware
	out.WriteString(`<h3>` + escape(view.Labels.HardwareTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.HardwareHelp) + `</p>`)
	if hardware.Detected != "" {
		out.WriteString(`<p class="notice">` + escape(hardware.Detected) + `</p>`)
	}
	if hardware.ResolvedText != "" {
		out.WriteString(`<p class="help">` + escape(hardware.ResolvedText) + `</p>`)
	}
	if hardware.PinnedText != "" {
		out.WriteString(`<p class="help">` + escape(hardware.PinnedText) + `</p>`)
	}
	out.WriteString(`<p class="help">` + escape(hardware.RecommendText) + `</p>`)
	out.WriteString(`<div class="field"><label for="wizard-hardware">` + escape(view.Labels.HardwareTitle) + `</label>`)
	out.WriteString(`<select id="wizard-hardware">`)
	for _, option := range hardware.Options {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == hardware.Recommend {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	out.WriteString(`</select></div>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.HardwareRestart) + `</p>`)
	return out.String()
}

func wizardModelHTML(view wizard.View) string {
	var out strings.Builder
	model := view.Model
	out.WriteString(`<h3>` + escape(view.Labels.ModelTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.ModelHelp) + `</p>`)
	if !model.Available {
		if model.Error != "" {
			out.WriteString(`<p class="failure">` + escape(model.Error) + `</p>`)
		}
		out.WriteString(`<p class="help">` + escape(view.Labels.ModelNoCatalog) + `</p>`)
		out.WriteString(`<div class="actions-inline"><button type="button" data-action="wizard-refresh">` +
			escape(view.Labels.WelcomeCheck) + `</button></div>`)
		return out.String()
	}
	if model.Recommended != "" {
		device := model.Device
		if device == "" {
			device = "—"
		}
		out.WriteString(`<p class="notice">` + escape(fmt.Sprintf(view.Labels.ModelRecommended, device, model.Recommended)) + `</p>`)
	}
	if model.StateLabel != "" {
		out.WriteString(`<p class="help">` + escape(view.Labels.ModelDevice) + `: ` + escape(model.Device) +
			` · ` + escape(model.StateLabel) + `</p>`)
	}
	if model.ActiveModel != "" {
		out.WriteString(`<p class="help">` + escape(fmt.Sprintf(view.Labels.ModelActive, model.ActiveModel)) + `</p>`)
	} else {
		out.WriteString(`<p class="help">` + escape(view.Labels.ModelNone) + `</p>`)
	}
	if model.Error != "" {
		out.WriteString(`<p class="failure">` + escape(model.Error) + `</p>`)
	}
	if model.Warning != "" {
		out.WriteString(`<p class="failure">` + escape(model.Warning) + `</p>`)
	}
	if len(model.Rows) == 0 {
		out.WriteString(`<p class="help">` + escape(view.Labels.ModelNoCatalog) + `</p>`)
	} else {
		out.WriteString(`<table class="models"><thead><tr><th>` + escape(view.Labels.ModelTitle) +
			`</th><th>` + escape(view.Labels.ModelSize) + `</th><th>` + escape(view.Labels.ModelMemory) +
			`</th><th></th></tr></thead><tbody>`)
		for _, row := range model.Rows {
			out.WriteString(`<tr data-model="` + escape(row.Name) + `"><td><strong>` + escape(row.Name) + `</strong>`)
			if row.Recommended {
				out.WriteString(` <span class="badge">` + escape(view.Labels.ModelBadge) + `</span>`)
			}
			if row.Installed {
				out.WriteString(` <span class="badge">` + escape(view.Labels.ModelInstalled) + `</span>`)
			}
			out.WriteString(`</td><td>` + escape(row.Size) + `</td><td>` + escape(row.Memory) + `</td><td class="model-actions">`)
			out.WriteString(wizardModelActions(view, row))
			out.WriteString(`</td></tr>`)
			if row.Download != nil {
				out.WriteString(`<tr class="model-progress"><td colspan="4">` + escape(row.Download.Label) + `</td></tr>`)
			}
		}
		out.WriteString(`</tbody></table>`)
	}
	out.WriteString(`<div class="actions-inline"><button type="button" data-action="wizard-model-defer">` +
		escape(view.Labels.ModelDefer) + `</button></div>`)
	if view.DeferredModel != "" {
		out.WriteString(`<p class="help">` + escape(fmt.Sprintf(view.Labels.ModelDeferred, view.DeferredModel)) + `</p>`)
	}
	return out.String()
}

func wizardModelActions(view wizard.View, row wizard.ModelRow) string {
	if row.Download != nil && row.Download.InProgress {
		return `<button type="button" data-action="wizard-model-cancel" data-model="` +
			escape(row.Name) + `">` + escape(view.Labels.ModelCancel) + `</button>`
	}
	if !row.Installed {
		return `<button type="button" data-action="wizard-model-download" data-model="` +
			escape(row.Name) + `">` + escape(fmt.Sprintf(view.Labels.ModelDownload, row.Name)) + `</button>`
	}
	if row.Active {
		return `<span class="badge active">` + escape(view.Labels.ModelIsActive) + `</span>`
	}
	return `<button type="button" data-action="wizard-model-activate" data-model="` +
		escape(row.Name) + `">` + escape(view.Labels.ModelActivate) + `</button>`
}

func wizardServerHTML(view wizard.View) string {
	var out strings.Builder
	server := view.Server
	out.WriteString(`<h3>` + escape(view.Labels.ServerTitle) + `</h3>`)
	if len(server.Profiles) > 0 {
		out.WriteString(`<h4>` + escape(view.Labels.ServerProfiles) + `</h4>`)
		for _, profile := range server.Profiles {
			out.WriteString(`<div class="row" data-profile="` + escape(profile.Name) + `"><strong>` + escape(profile.Name) + `</strong>`)
			if profile.Active {
				out.WriteString(` <span class="badge">` + escape(view.Labels.ServerActive) + `</span>`)
			}
			out.WriteString(` <span class="health">` + escape(profile.Health) + `</span>`)
			out.WriteString(` <code>` + escape(profile.URL) + `</code></div>`)
		}
	}
	out.WriteString(`<h4>` + escape(view.Labels.ServerLocalTitle) + `</h4>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.ServerLocalHelp) + `</p>`)
	out.WriteString(`<div class="field"><label for="wizard-local-address">` + escape(view.Labels.ServerLocalAddress) + `</label>`)
	out.WriteString(`<input type="text" id="wizard-local-address" autocomplete="off" spellcheck="false" value="` +
		escape(server.LocalAddress) + `"></div>`)
	out.WriteString(`<button type="button" data-action="wizard-local">` + escape(view.Labels.ServerUseLocal) + `</button>`)
	out.WriteString(`<h4>` + escape(view.Labels.ServerRemoteTitle) + `</h4>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.ServerRemoteHelp) + `</p>`)
	out.WriteString(`<div class="field"><label for="wizard-server-address">` + escape(view.Labels.ServerAddress) + `</label>`)
	out.WriteString(`<input type="text" id="wizard-server-address" autocomplete="off" spellcheck="false" placeholder="desktop.local:8765"></div>`)
	out.WriteString(`<div class="field"><label for="wizard-server-code">` + escape(view.Labels.ServerCode) + `</label>`)
	out.WriteString(`<input type="text" id="wizard-server-code" autocomplete="off" spellcheck="false" maxlength="16"></div>`)
	out.WriteString(`<div class="field"><label for="wizard-server-name">` + escape(view.Labels.ServerDeviceName) + `</label>`)
	out.WriteString(`<input type="text" id="wizard-server-name" autocomplete="off" spellcheck="false"></div>`)
	out.WriteString(`<button type="button" class="primary" data-action="wizard-pair-remote">` + escape(view.Labels.ServerPair) + `</button>`)
	if server.ActiveID == "" {
		out.WriteString(`<p class="failure">` + escape(view.Labels.ServerNoProfile) + `</p>`)
	}
	return out.String()
}

func wizardTryHTML(view wizard.View) string {
	var out strings.Builder
	try := view.Try
	out.WriteString(`<h3>` + escape(view.Labels.TryTitle) + `</h3>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.TryHelp) + `</p>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.TryTitle) + `: ` +
		`<span data-wizard-phase>` + escape(try.PhaseLabel) + `</span> · ` +
		`<span data-wizard-try-state>` + escape(try.StateLabel) + `</span></p>`)
	switch {
	case try.Passed:
		out.WriteString(`<p class="notice">` + escape(view.Labels.TryPassed) + `</p>`)
	case !try.Ready:
		out.WriteString(`<h4>` + escape(view.Labels.TryMissing) + `</h4><ul class="wizard-missing">`)
		for _, reason := range try.Reasons {
			out.WriteString(`<li>` + escape(reason.Text))
			if reason.Start {
				out.WriteString(` <button type="button" data-action="wizard-try-start-server">` +
					escape(view.Labels.TryStartServer) + `</button>`)
			}
			if reason.Step != "" {
				out.WriteString(` <button type="button" data-action="wizard-goto" data-step="` +
					escape(string(reason.Step)) + `">` + escape(view.Labels.GotoStep) + `</button>`)
			}
			out.WriteString(`</li>`)
		}
		out.WriteString(`</ul>`)
	default:
		out.WriteString(`<p class="notice">` + escape(view.Labels.TryReady) + `</p>`)
	}
	out.WriteString(`<div class="field"><label for="wizard-try-output">` + escape(view.Labels.TryTitle) + `</label>`)
	out.WriteString(`<textarea id="wizard-try-output" rows="4" spellcheck="false"></textarea></div>`)
	out.WriteString(`<p class="failure" data-wizard-error>` + escape(try.RuntimeError) + `</p>`)
	out.WriteString(`<div class="actions-inline">`)
	out.WriteString(`<button type="button" class="primary" data-action="wizard-try-start">` + escape(view.Labels.TryStart) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-try-stop">` + escape(view.Labels.TryStop) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-try-confirm">` + escape(view.Labels.TryConfirm) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-try-fail">` + escape(view.Labels.TryRetry) + `</button>`)
	out.WriteString(`</div>`)
	if try.Passed {
		out.WriteString(`<p class="help">` + escape(view.Labels.TryFinish) + `</p>`)
	}
	return out.String()
}

func wizardFooterHTML(view wizard.View) string {
	var out strings.Builder
	out.WriteString(`<div class="wizard-footer">`)
	out.WriteString(`<button type="button" data-action="wizard-back">` + escape(view.Labels.Back) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-skip">` + escape(view.Labels.Skip) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-skip-all">` + escape(view.Labels.SkipAll) + `</button>`)
	next := view.Labels.Next
	if view.Step == wizard.StepTry {
		next = view.Labels.Finish
	}
	out.WriteString(`<button type="button" class="primary" data-action="wizard-next">` + escape(next) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-close">` + escape(view.Labels.Close) + `</button>`)
	out.WriteString(`<button type="button" data-action="wizard-restart">` + escape(view.Labels.Restart) + `</button>`)
	out.WriteString(`</div>`)
	return out.String()
}

// wizardLauncherHTML renders the always-available Settings entry point.
func wizardLauncherHTML(view wizard.View) string {
	if !view.Enabled {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<section class="group" data-section="wizard-launcher">`)
	out.WriteString(`<h2>` + pageIcon("wand") + escape(view.Labels.ReopenTitle) + `</h2>`)
	out.WriteString(`<p class="help">` + escape(view.Labels.ReopenHelp) + `</p>`)
	if view.DeferredModel != "" {
		out.WriteString(`<p class="help">` + escape(fmt.Sprintf(view.Labels.ModelDeferred, view.DeferredModel)) + `</p>`)
	}
	label := fmt.Sprintf(view.Labels.Resume, view.StepNumber, view.StepCount)
	if view.Completed {
		label = view.Labels.RunAgain
	}
	out.WriteString(`<button type="button" data-action="wizard-open">` + escape(label) + `</button>`)
	out.WriteString(`</section>`)
	return out.String()
}
