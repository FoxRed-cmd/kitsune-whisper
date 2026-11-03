package shell_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

func sampleView() shell.SettingsView {
	return shell.SettingsView{
		View: dictation.View{
			Language:    i18n.Russian,
			Title:       "kitsune-whisper",
			ConfigTitle: "Файл настроек",
			ConfigPath:  "/home/user/.config/kitsune-whisper/kitsune.yaml",
			Subtitle:    "Приватное распознавание речи в локальной сети",
			Actions: dictation.Actions{
				Save:              "Сохранить",
				Restart:           "Перезапустить клиент",
				Test:              "Проверить",
				Stop:              "Остановить",
				Level:             "Уровень входа",
				RestartHint:       "Чтобы применить изменения с пометкой «Нужен перезапуск», перезапустите клиент.",
				HotkeyPlaceholder: "Нажмите сочетание",
			},
			Sections: []dictation.Section{
				{
					ID:    "general",
					Title: "Общие",
					Fields: []dictation.Field{{
						Path:   "client.ui_language",
						Label:  "Язык интерфейса",
						Kind:   dictation.KindSelect,
						Value:  "ru",
						Source: "file", SourceLabel: "Сохранено",
						Mode: settings.ApplyHot,
						Options: []dictation.Option{
							{Value: "auto", Label: "Системный"},
							{Value: "en", Label: "Английский"},
							{Value: "ru", Label: "Русский"},
						},
					}},
				},
				{
					ID:    "dictation",
					Title: "Распознавание речи",
					Fields: []dictation.Field{
						{
							Path:   "client.hotkey",
							Label:  "Горячая клавиша распознавания",
							Help:   "Нажмите сочетание в поле.",
							Kind:   dictation.KindHotkey,
							Value:  "Ctrl+Alt+D",
							Source: "file", SourceLabel: "Сохранено",
							Mode: settings.ApplyHot,
						},
						{
							Path:   "client.cancel_hotkey",
							Label:  "Горячая клавиша отмены",
							Help:   "Отменяет текущую запись.",
							Kind:   dictation.KindHotkey,
							Value:  "Ctrl+Alt+Q",
							Source: "file", SourceLabel: "Сохранено",
							Mode: settings.ApplyHot,
						},
						{
							Path:    "client.hotkey_backend",
							Label:   "Бэкенд сеанса",
							Kind:    dictation.KindSelect,
							Value:   "x11",
							Source:  "default",
							Mode:    settings.ApplyRestart,
							Badge:   "Нужен перезапуск",
							Options: []dictation.Option{{Value: "auto", Label: "Автоматически"}, {Value: "x11", Label: "X11"}},
						},
						{
							Path:        "client.audio.device",
							Label:       "Микрофон",
							Kind:        dictation.KindMicrophone,
							Value:       "1",
							Source:      "environment",
							SourceLabel: "Окружение",
							Locked:      true,
							LockReason:  "Заблокировано переменной окружения KITSUNE_CLIENT_AUDIO__DEVICE",
							Mode:        settings.ApplyHot,
							Options:     []dictation.Option{{Value: "", Label: "Системный по умолчанию"}, {Value: "1", Label: "USB Yeti"}},
						},
						{
							Path:   "client.feedback.earcons",
							Label:  "Звуковые сигналы",
							Kind:   dictation.KindToggle,
							Value:  "true",
							Source: "file", SourceLabel: "Сохранено",
							Mode: settings.ApplyHot,
						},
					},
				},
			},
		},
		Servers: servers.View{
			Profiles: []servers.Profile{
				{
					ID: "local", Name: "Local Server", URL: "https://localhost:8765", Active: true,
					Health: trust.Status{State: trust.StateOnline},
				},
				{
					ID: "remote-desktop", Name: "Desktop", URL: "https://desktop.local:8765",
					Health: trust.Status{State: trust.StateOffline, Error: "connection refused"},
				},
			},
			ActiveID: "local",
			Devices: []servers.Device{
				{ID: "dev-1", Name: "Laptop", CreatedAt: 1.0, LastSeen: ptrFloat(1_700_000_000)},
				{ID: "dev-2", Name: "Tablet", CreatedAt: 2.0},
			},
			PairingCode: &servers.PairingCode{Code: "ABCD2345", ExpiresAt: 1234},
		},
		Labels: shell.ServerLabels{
			Title:         "Серверы и устройства",
			Profiles:      "Профили серверов",
			Active:        "Активный",
			Health:        "Состояние",
			RefreshHealth: "Обновить состояние",
			Use:           "Выбрать",
			StateLabels: map[string]string{
				"unknown": "Проверка…", "online": "Доступен", "offline": "Недоступен",
				"unauthenticated": "Не сопряжён", "unpinned": "Не закреплён", "mismatch": "Изменён",
			},
			PairTitle:        "Добавить удалённый сервер",
			PairHelp:         "Введите адрес и код.",
			Address:          "Адрес сервера",
			Code:             "Код сопряжения",
			Name:             "Имя устройства",
			NameHelp:         "Имя в списке устройств.",
			Pair:             "Сопрячь",
			Devices:          "Устройства",
			DevicesFor:       "Устройства активного профиля.",
			Refresh:          "Обновить устройства",
			GenerateCode:     "Создать код сопряжения",
			CodeHelp:         "Код одноразовый.",
			CodeExpires:      "действует 10 минут",
			NoDevices:        "Нет устройств.",
			LastSeen:         "Последняя активность",
			Never:            "Никогда",
			Revoke:           "Отозвать",
			RevokeConfirm:    "Отозвать устройство?",
			ThisDevice:       "Это устройство",
			NoServers:        "Нет профилей.",
			ExposureTitle:    "Доступ из локальной сети",
			ExposureHelp:     "Разрешить клиентам из сети.",
			ExposureOn:       "Разрешить доступ из сети",
			ExposureOff:      "Запретить доступ из сети",
			ExposureConfirm:  "Разрешить доступ из сети?",
			ExposureOnState:  "включён",
			ExposureOffState: "выключен",
			ExposureLiveOn:   "Сервер сейчас доступен из локальной сети.",
			ExposureLiveOff:  "Сервер сейчас доступен только на этом компьютере.",
			ExposureListen:   "Адрес прослушивания: %s.",
			ExposurePending:  "Перезапустите сервер, чтобы применить изменение.",
		},
	}
}

func ptrFloat(value float64) *float64 { return &value }

func TestPageUsesTheFullWindowWidthAndPreloadsDiagnostics(t *testing.T) {
	page := shell.Page(shell.Snapshot{Version: "1.0.0"}, sampleView())
	if strings.Contains(page, "max-width:1200px") {
		t.Fatal("the page still centres its content in a fixed-width column")
	}
	if !strings.Contains(page, `action: "prime-diagnostics"`) {
		t.Fatal("the page does not preload Diagnostics when it opens")
	}
	if !strings.Contains(page, ".settings-pane [data-section][hidden] { display:none !important; }") {
		t.Fatal("an author display rule can override [hidden] and show every screen at once")
	}
}

func TestPageFitsAServerSettingsFormToThePaneHeight(t *testing.T) {
	page := shell.Page(shell.Snapshot{Version: "1.0.0"}, managementView())
	if !strings.Contains(page, `class="group server-settings"`) {
		t.Fatal("the Server settings form does not fill the pane")
	}
	if !strings.Contains(page, `class="server-fields"`) || !strings.Contains(page, `class="server-save"`) {
		t.Fatal("the Server settings body and its Save footer are not split")
	}
	if !strings.Contains(page, ".server-fields { flex:1 1 auto; min-height:0; overflow-y:auto;") {
		t.Fatal("the Server fields are not the scrolling part")
	}
}

func TestSettingsPageSplitsScreensIntoANavigationMenu(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	for _, want := range []string{
		`id="settings-nav"`,
		`data-nav="general"`,
		`data-nav="dictation"`,
		`data-nav="servers" data-targets="servers,devices,exposure"`,
		`data-nav="server" data-targets="server-settings"`,
		`data-nav="diagnostics" data-targets="diagnostics"`,
		`id="settings-pane"`,
		`function selectSection(`,
		`function renderSettings(`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	nav := strings.Index(page, `id="settings-nav"`)
	pane := strings.Index(page, `id="settings-pane"`)
	general := strings.Index(page, `data-section="general"`)
	if nav < 0 || pane < 0 || general < 0 || nav > pane || pane > general {
		t.Fatalf("navigation, pane, and content are out of order: nav=%d pane=%d content=%d", nav, pane, general)
	}
	if strings.Contains(page, `data-nav="wizard"`) {
		t.Error("the wizard launcher is listed without a wizard view")
	}
	if strings.Index(page, `data-nav="servers"`) > strings.Index(page, `data-nav="server"`) {
		t.Error("the navigation order is wrong")
	}
}

func TestSettingsPageRendersIndicatorPlacementAndLivePreview(t *testing.T) {
	view := managementView()
	view.Sections = append(view.Sections, dictation.Section{
		ID:    "appearance",
		Title: "On-screen indicator",
		Fields: []dictation.Field{
			{
				Path:    "client.feedback.indicator_anchor",
				Label:   "Position on screen",
				Kind:    dictation.KindPosition,
				Value:   "bottom-left",
				Preview: true,
				Options: []dictation.Option{
					{Value: "top-left", Label: "Top left"},
					{Value: "top-center", Label: "Top centre"},
					{Value: "bottom-left", Label: "Bottom left"},
				},
			},
			{
				Path:    "client.feedback.indicator_offset_x",
				Label:   "Horizontal offset (px)",
				Kind:    dictation.KindNumber,
				Value:   "12",
				Preview: true,
			},
		},
	})
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`data-nav="appearance" data-targets="appearance"`,
		`data-section="appearance"`,
		`class="position-grid"`,
		`data-indicator-anchor="bottom-left" aria-pressed="true"`,
		`data-path="client.feedback.indicator_anchor" data-indicator-preview="true" value="bottom-left"`,
		`type="number" step="1" min="-4096" max="4096" id="field-client-feedback-indicator-offset-x" data-path="client.feedback.indicator_offset_x" data-indicator-preview="true"`,
		`function syncIndicatorPreview(`,
		`action: "preview-indicator"`,
		`action: "hide-indicator-preview"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSettingsPageUsesOneScrollingPane(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	if !strings.Contains(page, ".settings-nav { display:flex; flex-direction:column; gap:2px; overflow-y:auto;") {
		t.Error("the navigation menu does not scroll on its own")
	}
	if !strings.Contains(page, ".settings-pane { display:flex; flex-direction:column; gap:10px; min-height:0; overflow-y:auto;") {
		t.Error("the content pane does not scroll on its own")
	}
	if !strings.Contains(page, "body { margin:0; font: 13px/1.45 system-ui, -apple-system, \"Segoe UI\", sans-serif; background:var(--bg); color:var(--fg); overflow:hidden;") {
		t.Error("the page itself still scrolls instead of the panes")
	}
}

func TestPageFailuresAreShownAboveTheScrollingContent(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	messages := strings.Index(page, `id="messages"`)
	settings := strings.Index(page, `id="settings"`)
	if messages < 0 || settings < 0 || messages > settings {
		t.Fatalf("the message area is not above the settings: messages=%d settings=%d", messages, settings)
	}
	for _, want := range []string{
		`function showMessage(`,
		`button.dataset.pending = "1"`,
		`button.classList.add("pending")`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPageRendersTheInitialSnapshot(t *testing.T) {
	page := shell.Page(shell.Snapshot{
		Version: "v1.2.3",
		Profile: "Local Server",
		Running: true,
		Phase:   runtime.PhaseIdle,
		State:   shell.StateIdle,
	}, sampleView())
	for _, want := range []string{"v1.2.3", "Local Server", "renderStatus(", "labels.phase", "labels.profile"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(page, `lang="ru"`) {
		t.Error("page does not carry the interface language")
	}
}

func TestPageRendersTheDictationScreens(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	for _, want := range []string{
		"Общие",
		"Распознавание речи",
		"Язык интерфейса",
		"Горячая клавиша распознавания",
		"Нажмите сочетание",
		"Микрофон",
		"Звуковые сигналы",
		"Нужен перезапуск",
		"Заблокировано переменной окружения KITSUNE_CLIENT_AUDIO__DEVICE",
		"Окружение",
		"Сохранить",
		"Перезапустить клиент",
		"Уровень входа",
		"Файл настроек",
		"/home/user/.config/kitsune-whisper/kitsune.yaml",
		`data-hotkey="true"`,
		`data-action="test-mic"`,
		`data-action="stop-mic"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The selected option is marked, and the locked field cannot be edited.
	if !strings.Contains(page, `<option value="ru" selected>Русский</option>`) {
		t.Error("page did not mark the selected language")
	}
	if !strings.Contains(page, `id="field-client-audio-device"`) || !strings.Contains(page, `disabled`) {
		t.Error("locked microphone field is editable")
	}
	// Toggled values render as checked checkboxes.
	if !strings.Contains(page, `type="checkbox"`) || !strings.Contains(page, `checked`) {
		t.Error("earcons toggle is not checked")
	}
}

func TestServersHTMLShowsASoftStoppedServerAsStopped(t *testing.T) {
	view := managementView()
	for i := range view.Servers.Profiles {
		if view.Servers.Profiles[i].ID == view.Servers.ActiveID {
			view.Servers.Profiles[i].Health = trust.Status{
				State: trust.StateOnline, Stopped: true, ModelState: "stopped",
			}
		}
	}
	view.Labels.StateLabels = map[string]string{
		"unknown": "Проверка…", "online": "Доступен", "stopped": "Остановлен",
	}
	page := shell.SettingsHTML(view)
	if !strings.Contains(page, `data-health="stopped"`) || !strings.Contains(page, "Остановлен") {
		t.Fatalf("a soft-stopped Server is still rendered as online: %s", page)
	}
}

func TestPageRendersStartupSwitchesAndTheUnifiedLogLevel(t *testing.T) {
	view := managementView()
	view.Startup = shell.StartupView{Available: true, Client: true, Server: false, ServerAvailable: false}
	view.Labels.StartupTitle = "Autostart"
	view.Labels.StartupHelp = "Start both at login."
	view.Labels.StartupClient = "Client at login"
	view.Labels.StartupClientHelp = "Starts hidden."
	view.Labels.StartupServer = "Server at login"
	view.Labels.StartupServerHelp = "Starts the local Server."
	page := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-section="startup"`,
		`data-nav="startup"`,
		`data-startup-component="client"`,
		`data-startup-component="server"`,
		`id="diagnostics-log-level"`,
		`<option value="info" selected>Info</option>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(page, `id="startup-client" data-startup-component="client" checked`) {
		t.Error("the Client switch is not enabled")
	}
	if strings.Contains(page, `id="startup-client" data-startup-component="client" checked disabled`) {
		t.Error("the Client switch is disabled even though the controller manages it")
	}
	if !strings.Contains(page, `id="startup-server" data-startup-component="server" disabled`) {
		t.Error("the unavailable Server switch is not disabled")
	}
}

func TestSettingsHTMLRendersEveryFieldKind(t *testing.T) {
	html := shell.SettingsHTML(sampleView())
	if !strings.Contains(html, `<select id="field-client-ui-language"`) {
		t.Error("select field is not rendered as a select")
	}
	if !strings.Contains(html, `<input type="text" id="field-client-hotkey"`) {
		t.Error("hotkey field is not rendered as a text input")
	}
	if !strings.Contains(html, `<input type="checkbox" id="field-client-feedback-earcons"`) {
		t.Error("toggle field is not rendered as a checkbox")
	}
	if !strings.Contains(html, `value="Ctrl+Alt+D"`) {
		t.Error("field values are not rendered")
	}
	if !strings.Contains(html, `<option value="1" selected>USB Yeti</option>`) {
		t.Error("microphone selection is not rendered")
	}
	if !strings.Contains(html, `<div class="meter" role="meter"`) {
		t.Error("the microphone level meter is missing")
	}
}

func TestSettingsHTMLEscapesHostileText(t *testing.T) {
	const hostile = `</script><img src=x onerror="alert(1)">&`
	view := dictation.View{
		Language: i18n.English,
		Actions:  dictation.Actions{Save: "Save", Restart: "Restart", HotkeyPlaceholder: "Press"},
		Sections: []dictation.Section{{
			ID:    "dictation",
			Title: hostile,
			Fields: []dictation.Field{{
				Path:       "client.spool_dir",
				Label:      hostile,
				Kind:       dictation.KindText,
				Value:      hostile,
				LockReason: hostile,
				Locked:     true,
			}},
		}},
	}
	html := shell.SettingsHTML(shell.SettingsView{View: view})
	if strings.Contains(html, hostile) {
		t.Fatal("settings fragment embedded unescaped text")
	}
	if strings.Contains(html, "<img") {
		t.Fatal("settings fragment allowed an image injection")
	}
	if !strings.Contains(html, "&lt;/script&gt;") {
		t.Fatal("settings fragment did not escape the closing tag")
	}
}

func TestPageHidesTheActionsWithoutSettings(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, shell.SettingsView{})
	if !strings.Contains(page, `<footer id="actions" style="display:none">`) {
		t.Fatal("a status-only page offers empty save and restart buttons")
	}
	withSettings := shell.Page(shell.Snapshot{}, sampleView())
	if strings.Contains(withSettings, `<footer id="actions" style="display:none">`) {
		t.Fatal("the actions are hidden despite a settings view")
	}
}

func TestPageUsesTheTranslatedWizardError(t *testing.T) {
	page := shell.Page(shell.Snapshot{Error: "raw failure", WizardError: "translated failure"}, sampleView())
	if !strings.Contains(page, `wizardError.textContent = s.wizardError || ""`) {
		t.Fatal("the wizard error is not rendered from the translated snapshot field")
	}
	if strings.Contains(page, `wizardError.textContent = s.error || ""`) {
		t.Fatal("the wizard error still renders the raw runtime error")
	}
}

func TestWizardTryStopRefocusesTheOutputField(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	start := strings.Index(page, `action === "wizard-try-stop"`)
	if start < 0 {
		t.Fatal("the page has no try-stop branch")
	}
	end := strings.Index(page[start:], "} else if")
	if end < 0 {
		t.Fatal("could not delimit the try-stop branch")
	}
	branch := page[start : start+end]
	if !strings.Contains(branch, `document.getElementById("wizard-try-output")`) ||
		!strings.Contains(branch, ".focus()") {
		t.Fatalf("the try-stop branch does not refocus the output field: %s", branch)
	}
}

func TestPageKeepsErrorTextInsideTheScript(t *testing.T) {
	const hostile = `</script><script>alert("x")</script>&<`
	page := shell.Page(shell.Snapshot{
		State: shell.StateError,
		Error: hostile,
	}, shell.SettingsView{})

	// The literal terminator from the snapshot must not appear inside the
	// embedded JSON: the only closing script tags are the page's own.
	if strings.Count(page, "</script>") != 1 {
		t.Fatalf("page has %d closing script tags, want 1", strings.Count(page, "</script>"))
	}
	if strings.Contains(page, hostile) {
		t.Fatal("page embedded the unescaped error text")
	}
	if !strings.Contains(page, `\u003c`) {
		t.Fatal("page did not escape angle brackets inside the JSON payload")
	}

	// The escaped payload must still be valid JSON.
	start := strings.LastIndex(page, "renderStatus(") + len("renderStatus(")
	end := strings.Index(page[start:], ");")
	if start < len("renderStatus(") || end < 0 {
		t.Fatal("page has no initial renderStatus call")
	}
	var decoded shell.Snapshot
	if err := json.Unmarshal([]byte(page[start:start+end]), &decoded); err != nil {
		t.Fatalf("initial snapshot is not valid JSON: %v", err)
	}
	if decoded.Error != hostile {
		t.Fatalf("decoded error = %q, want %q", decoded.Error, hostile)
	}
}

func TestPageIntentBridgePostsToOneEndpoint(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	if !strings.Contains(page, `fetch("/kitsune/intent"`) {
		t.Fatal("the page does not post intents to the settings service")
	}
	if !strings.Contains(page, `collectPatch()`) {
		t.Fatal("the page does not collect the edited values")
	}
	if !strings.Contains(page, `action: "validate"`) {
		t.Fatal("the hotkey recorder does not ask Go to validate")
	}
}

func TestPageUpdatesTheWindowChromeOnALanguageSwitch(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	if !strings.Contains(page, `<h1 id="page-title">kitsune-whisper</h1>`) {
		t.Fatal("the page title has no element the settings renderer can update")
	}
	if !strings.Contains(page, `<p class="sub" id="page-subtitle">Приватное распознавание речи в локальной сети</p>`) {
		t.Fatal("the page subtitle is not rendered with its id")
	}
	// renderSettings receives the whole view, so it must refresh the title and
	// subtitle rather than leaving the initial language on screen.
	for _, want := range []string{
		`document.title = v.title`,
		`text("page-title", v.title)`,
		`text("page-subtitle", v.subtitle)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("settings renderer does not update the window chrome: missing %q", want)
		}
	}
}

func TestPageShowsTheLiveListenerAndAPendingRestart(t *testing.T) {
	view := sampleView()
	view.Servers.Profiles[0].Health.Network = &trust.NetworkStatus{
		ExposeToNetwork: true, BindHost: "0.0.0.0", Port: 8765,
	}
	view.Servers.Config = &servers.ServerConfig{
		Config: map[string]any{"expose_to_network": false},
	}
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`data-exposure-live="on"`,
		"Сервер сейчас доступен из локальной сети.",
		`data-exposure-listen="0.0.0.0:8765"`,
		"Адрес прослушивания: 0.0.0.0:8765.",
		`data-exposure-pending="true"`,
		"Перезапустите сервер, чтобы применить изменение.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPageShowsLoopbackOnlyWhenLANExposureIsOff(t *testing.T) {
	view := sampleView()
	view.Servers.Profiles[0].Health.Network = &trust.NetworkStatus{
		BindHost: "127.0.0.1", Port: 8765,
	}
	view.Servers.Config = &servers.ServerConfig{
		Config: map[string]any{"expose_to_network": false},
	}
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`data-exposure-live="off"`,
		"Сервер сейчас доступен только на этом компьютере.",
		`data-exposure-listen="127.0.0.1:8765"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, `data-exposure-pending="true"`) {
		t.Fatal("an unchanged loopback listener is reported as pending a restart")
	}
}

func TestPageRendersServersDevicesAndPairing(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	for _, want := range []string{
		"Серверы и устройства",
		"Local Server",
		"Desktop",
		"https://localhost:8765",
		`data-action="pair-remote"`,
		`id="server-address"`,
		`id="server-code"`,
		"Устройства",
		"Laptop",
		"Tablet",
		"Никогда",
		"ABCD2345",
		`data-action="refresh-devices"`,
		`data-action="pairing-code"`,
		`data-action="revoke-device"`,
		`data-action="exposure"`,
		`data-expose="on"`,
		`data-device-id="dev-1"`,
		`data-confirm="Отозвать устройство?"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPageDoesNotOfferRevokeForTheLocalClientOnTheLocalServer(t *testing.T) {
	view := sampleView()
	view.Servers.Devices = []servers.Device{
		{ID: "self", Name: "Local client", Current: true},
		{ID: "laptop", Name: "Laptop"},
	}

	page := shell.Page(shell.Snapshot{}, view)
	if strings.Contains(page, `data-device-id="self"`) {
		t.Fatal("the Local Client's own Device still has a Revoke button on the Local Server")
	}
	if !strings.Contains(page, `data-device-id="laptop"`) {
		t.Fatal("another Device of the Local Server lost its Revoke button")
	}
	if !strings.Contains(page, "Это устройство") {
		t.Fatal("the Local Client's own Device is not marked as this Device")
	}
}

func TestPageRendersProfileHealthAndTheSwitchAction(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	for _, want := range []string{
		"Состояние",
		"Доступен",
		"Недоступен",
		"Обновить состояние",
		"Выбрать",
		`data-action="refresh-servers"`,
		`data-action="use-profile"`,
		`data-profile-id="remote-desktop"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The active profile is marked and is not offered a redundant switch.
	if strings.Contains(page, `data-profile-id="local"`) {
		t.Error("the active profile still renders a switch button")
	}
}

func TestPageRendersTheSpoolScreen(t *testing.T) {
	view := sampleView()
	const entryID = "utterance-20261004T120000.000000-001.wav"
	view.Spool = shell.SpoolView{
		Entries:  []spool.Entry{{ID: entryID, SavedAt: 1_700_000_000, Duration: 2.5}},
		Selected: entryID,
		Text:     "hello <world>",
		Labels: shell.SpoolLabels{
			Title:         "Сохранённые записи",
			Help:          "Повторите запись на активном сервере.",
			Empty:         "Неудавшихся записей нет.",
			SavedAt:       "Сохранено",
			Duration:      "Длительность",
			Text:          "Транскрипция",
			EmptyText:     "Сервер не вернул текст.",
			Retry:         "Повторить",
			Copy:          "Скопировать текст",
			Insert:        "Вставить текст",
			Delete:        "Удалить",
			DeleteConfirm: "Удалить запись?",
		},
	}
	page := shell.Page(shell.Snapshot{State: shell.StateError, Error: "server unreachable", ErrorAction: shell.ActionSettings}, view)
	for _, want := range []string{
		"Сохранённые записи",
		"Повторите запись на активном сервере.",
		"Сохранено",
		"Длительность",
		"Транскрипция",
		"hello &lt;world&gt;",
		`data-action="spool-retry"`,
		`data-entry-id="` + entryID + `"`,
		`data-action="spool-copy"`,
		`data-action="spool-insert"`,
		`data-action="spool-delete"`,
		`data-confirm="Удалить запись?"`,
		`data-action="settings"`,
		"renderSpool(",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPageShowsAnEmptySpool(t *testing.T) {
	view := sampleView()
	view.Spool = shell.SpoolView{Labels: shell.SpoolLabels{
		Title: "Сохранённые записи", Empty: "Неудавшихся записей нет.", Retry: "Повторить", Delete: "Удалить",
	}}
	page := shell.Page(shell.Snapshot{}, view)
	if !strings.Contains(page, "Неудавшихся записей нет.") {
		t.Fatal("the empty Spool is not explained")
	}
	if strings.Contains(page, `data-action="spool-retry"`) {
		t.Fatal("the empty Spool renders action buttons")
	}
}

func TestServerAndSpoolFieldsEscapeHostileValues(t *testing.T) {
	const hostile = `</section><img src=x onerror="alert(1)">&`
	view := sampleView()
	view.Servers.Profiles = []servers.Profile{{ID: hostile, Name: hostile, URL: hostile}}
	view.Servers.Devices = []servers.Device{{ID: hostile, Name: hostile}}
	view.Servers.PairingCode = &servers.PairingCode{Code: hostile}
	view.Spool = shell.SpoolView{
		Entries:  []spool.Entry{{ID: hostile, Duration: 1}},
		Selected: hostile,
		Text:     hostile,
		Labels: shell.SpoolLabels{
			Title: hostile, Help: hostile, Empty: hostile, Text: hostile,
			EmptyText: hostile, Retry: hostile, Copy: hostile, Insert: hostile,
			Delete: hostile, DeleteConfirm: hostile,
		},
	}
	html := shell.SettingsHTML(view)
	if strings.Contains(html, "<img") {
		t.Fatal("the Settings screens allowed an image injection")
	}
	if !strings.Contains(html, "&lt;/section&gt;") {
		t.Fatal("the Settings screens did not escape a hostile value")
	}
}

func TestPageRendersTheModelCatalogProgressAndIdleTimeout(t *testing.T) {
	view := sampleView()
	view.ModelLabels = shell.ModelLabels{
		Title:             "Модели",
		Help:              "Скачайте модель.",
		Refresh:           "Обновить модели",
		NoCatalog:         "Загрузите каталог.",
		Name:              "Модель",
		Size:              "Загрузка",
		Memory:            "Память",
		Speed:             "Скорость",
		Quality:           "Качество",
		Recommended:       "Рекомендуется",
		Installed:         "Скачана",
		Active:            "Активная",
		State:             "Состояние модели",
		StateLoaded:       "загружена",
		Download:          "Скачать",
		Cancel:            "Отменить загрузку",
		Activate:          "Сделать активной",
		Delete:            "Удалить",
		DeleteConfirm:     "Удалить модель?",
		Queued:            "В очереди",
		Downloading:       "Загрузка",
		Done:              "Скачана",
		Failed:            "Ошибка загрузки",
		IdleTitle:         "Выгрузка при простое",
		IdleHelp:          "Выгружать после простоя.",
		IdleLabel:         "Секунд простоя",
		IdleSave:          "Сохранить тайм-аут",
		AdvancedTitle:     "Дополнительно",
		AdvancedHelp:      "Локальная папка.",
		LocalPath:         "Папка CTranslate2",
		LocalPathHint:     "Абсолютный путь.",
		UseLocal:          "Использовать эту папку",
		SpeedFast:         "быстро",
		QualityGood:       "хорошее",
		RecommendedDevice: "Рекомендация",
	}
	view.Servers.Models = &servers.ModelsView{
		Device: "cpu", Recommended: "base", State: "loaded",
		ActiveModel: "small", IdleUnloadSeconds: 600,
		Models: []servers.Model{
			{Name: "base", DiskBytes: 145_000_000, MemoryBytes: 600_000_000, Speed: "fast", Quality: "good", Recommended: true},
			{Name: "small", DiskBytes: 484_000_000, MemoryBytes: 1_300_000_000, Speed: "fast", Quality: "good", Installed: true},
			{Name: "tiny", DiskBytes: 75_000_000, MemoryBytes: 450_000_000, Speed: "fast", Quality: "basic", Installed: true},
			{Name: "medium", DiskBytes: 1_530_000_000, MemoryBytes: 3_000_000_000,
				Installed: true,
				Download:  &servers.ModelDownload{Model: "medium", State: "downloading", ReceivedBytes: 5_000_000, TotalBytes: 10_000_000}},
		},
	}

	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		"Модели",
		"Рекомендация",
		"145.0 MB",
		"600.0 MB",
		"быстро",
		"хорошее",
		`data-model="base"`,
		`data-action="download-model"`,
		`data-action="activate-model"`,
		`data-action="delete-model"`,
		`data-confirm="Удалить модель?"`,
		`data-action="load-models"`,
		`data-action="idle-timeout"`,
		`id="model-idle-seconds"`,
		`value="600"`,
		`id="model-local-path"`,
		`data-action="activate-local"`,
		"Загрузка 5.0 MB",
		"Скачана",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Models screen missing %q", want)
		}
	}
}

func TestModelsScreenEscapesHostileValues(t *testing.T) {
	const hostile = `</section><img src=x onerror="alert(1)">&`
	view := sampleView()
	view.Servers.Models = &servers.ModelsView{
		ActiveModel: hostile,
		ActivePath:  hostile,
		Error:       hostile,
		Models: []servers.Model{{
			Name: hostile, Installed: true,
			Download: &servers.ModelDownload{State: "failed", Error: hostile},
		}},
	}
	html := shell.SettingsHTML(view)
	if strings.Contains(html, "<img") {
		t.Fatal("Models screen allowed an image injection")
	}
	if !strings.Contains(html, "&lt;/section&gt;") {
		t.Fatal("Models screen did not escape a hostile value")
	}
}

func managementView() shell.SettingsView {
	view := sampleView()
	view.Servers.Config = &servers.ServerConfig{
		Path: "/home/kim/.config/kitsune-whisper/kitsune.yaml",
		Config: map[string]any{
			"host":       "127.0.0.1",
			"port":       float64(8765),
			"processing": map[string]any{"api_key": "***"},
		},
		Fields: map[string]servers.ConfigField{
			"server.host": {
				Value: "127.0.0.1", Source: "file", Mode: "restart",
			},
			"server.port": {
				Value: float64(8765), Source: "file", Mode: "restart",
			},
			"server.decode.language": {
				Value: "auto", Source: "default", Mode: "hot",
			},
			"server.processing.api_key": {
				Value: "***", Source: "file", Mode: "hot",
			},
			"server.processing.extra_body": {
				Value: map[string]any{"temperature": float64(0)}, Source: "file", Mode: "hot",
			},
			"server.expose_to_network": {
				Value: false, Source: "file", Locked: true, Mode: "restart",
				Reason: "security change: use POST /admin/exposure",
			},
		},
		Redacted: []string{"server.processing.api_key"},
	}
	view.Servers.Logs = &servers.ServerLog{
		Path:  "/home/kim/.local/state/kitsune-whisper/server.log",
		Lines: []string{"line 4", "line 5"},
	}
	view.Servers.Lifecycle = &servers.ServerLifecycle{Loaded: true, ModelState: "loaded"}
	view.Diagnostics = shell.DiagnosticsView{
		ClientVersion: "1.2.3",
		ServerVersion: "0.9.0",
		Profile:       "Local Server",
		ServerState:   "Online",
		ModelState:    "loaded",
		ConfigPath:    "/home/kim/.config/kitsune-whisper/kitsune.yaml",
		ConfigText:    "server.port: 8765\nserver.token: ***",
		Redacted:      []string{"server.token"},
		LogPath:       "/home/kim/.local/state/kitsune-whisper/server.log",
		LogText:       "line 4\nline 5",
		ClientLogPath: "/home/kim/.cache/kitsune-whisper/client.log",
		ClientLogText: "client line 1\nclient line 2",
		LogLevel:      "info",
		CopyText:      "kitsune-whisper diagnostics\nclient version: 1.2.3\nline 5\n",
	}
	view.Labels = shell.ServerLabels{
		Title:            "Servers",
		ManagementTitle:  "Server management",
		ManagementHelp:   "Restart, stop, or start.",
		ModelState:       "Model",
		RestartServer:    "Restart Server",
		StopServer:       "Stop Server",
		StartServer:      "Start Server",
		SettingsTitle:    "Server settings",
		SettingsHelp:     "Validated patches.",
		SaveSettings:     "Save Server settings",
		AdvancedValue:    "This value is a structured setting.",
		ModeRestart:      "Restart required",
		ModeHot:          "Applies live",
		Locked:           "Locked: %s",
		DiagnosticsTitle: "Diagnostics",
		DiagnosticsHelp:  "Versions and logs.",
		ClientVersion:    "Client version",
		ServerVersion:    "Server version",
		ServerState:      "Server state",
		DiagnosticsFor:   "Server profile",
		ConfigPath:       "Configuration file",
		ConfigTitle:      "Redacted configuration",
		Redacted:         "Redacted: %s",
		Unavailable:      "Not available",
		LogsTitle:        "Server log",
		LogsHelp:         "The last lines.",
		LogPath:          "Log file",
		NoLogs:           "No log lines.",
		LoadLogs:         "Load log tail",
		CopyLogs:         "Copy log",
		ClientLogsTitle:  "Client log",
		ClientLogPath:    "Client log path",
		CopyClientLogs:   "Copy Client log",
		LogLevelTitle:    "Log level",
		LogLevelHelp:     "One level for both.",
		CopyDiagnostics:  "Copy diagnostics",
		RefreshDiag:      "Refresh diagnostics",
		FieldLabels: map[string]shell.ServerFieldLabel{
			"server.host":            {Label: "Listen address", Help: "The address the Server binds."},
			"server.port":            {Label: "Port", Help: "The TCP port."},
			"server.decode.language": {Label: "Spoken language", Help: "Automatic detects it."},
		},
		SettingsGroups: []shell.ServerSettingsGroup{
			{Title: "Network", Fields: []string{"server.host", "server.port"}},
			{Title: "Speech recognition", Fields: []string{"server.decode.language"}},
		},
		OtherTitle: "Other",
		BoolOn:     "On",
		BoolOff:    "Off",
		Choices: map[string][]dictation.Option{
			"server.log_level": {
				{Value: "debug", Label: "Debug"},
				{Value: "info", Label: "Info"},
				{Value: "warning", Label: "Warning"},
				{Value: "error", Label: "Error"},
			},
		},
		LanguageOptions: []dictation.Option{
			{Value: "auto", Label: "Automatic"},
			{Value: "en", Label: "English"},
			{Value: "ru", Label: "Russian"},
		},
	}
	return view
}

func TestPageRendersServerManagementSettingsAndDiagnostics(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	for _, want := range []string{
		"Server management",
		`data-action="server-restart"`,
		`data-action="server-stop"`,
		`data-action="server-start"`,
		"Server settings",
		"Listen address",
		"The address the Server binds.",
		`data-action="save-server"`,
		`data-server-path="server.host"`,
		`data-server-path="server.port"`,
		`data-server-path="server.decode.language"`,
		"Spoken language",
		"Automatic detects it.",
		"Network",
		"Speech recognition",
		"Restart required",
		"Applies live",
		"Diagnostics",
		"Client version",
		"0.9.0",
		"Redacted: server.token",
		`id="diagnostics-config"`,
		`id="diagnostics-logs"`,
		`id="diagnostics-client-logs"`,
		`id="diagnostics-copy"`,
		"line 5",
		"client line 2",
		"Log level",
		"kitsune-whisper diagnostics",
		`data-action="refresh-diagnostics"`,
		`data-action="copy-diagnostics"`,
		`data-action="load-logs"`,
		`data-action="copy-logs"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, forbidden := range []string{"navigator.clipboard", "execCommand"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("page still contains clipboard logic %q", forbidden)
		}
	}
	for _, want := range []string{
		`sendIntent({ action: "copy-diagnostics" }, button)`,
		`sendIntent({ action: "copy-logs" }, button)`,
		`sendIntent({ action: "copy-client-logs" }, button)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing copy intent %q", want)
		}
	}
}

func TestDiagnosticsPageEscapesHostileConfiguration(t *testing.T) {
	const hostile = `</pre><img src=x onerror="alert(1)">&`
	view := managementView()
	view.Diagnostics.ConfigText = hostile
	view.Diagnostics.LogText = hostile
	html := shell.SettingsHTML(view)
	if strings.Contains(html, "<img") {
		t.Fatal("diagnostics allowed an image injection")
	}
	if !strings.Contains(html, "&lt;/pre&gt;") {
		t.Fatal("diagnostics did not escape a hostile value")
	}
}

func TestServerSettingsHideTextProcessingAndSecuritySwitches(t *testing.T) {
	html := shell.SettingsHTML(managementView())
	for _, hidden := range []string{
		`data-server-path="server.processing.api_key"`,
		`data-server-advanced="server.processing.extra_body"`,
		`data-server-path="server.expose_to_network"`,
		`data-server-path="server.allow_anonymous"`,
	} {
		if strings.Contains(html, hidden) {
			t.Errorf("the Server settings form still offers %s", hidden)
		}
	}
}
