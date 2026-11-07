package shell_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/api"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
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
					Title: "Интерфейс",
					Fields: []dictation.Field{
						{
							Path:   "client.theme",
							Label:  "Тема",
							Kind:   dictation.KindTheme,
							Value:  "system",
							Source: "default",
							Mode:   settings.ApplyHot,
							Options: []dictation.Option{
								{Value: "system", Label: "Системная"},
								{Value: "light", Label: "Светлая"},
								{Value: "dark", Label: "Тёмная"},
							},
						},
						{
							Path:   "client.ui_language",
							Label:  "Язык",
							Kind:   dictation.KindSelect,
							Value:  "ru",
							Source: "file", SourceLabel: "Сохранено",
							Mode: settings.ApplyHot,
							Options: []dictation.Option{
								{Value: "auto", Label: "Системный"},
								{Value: "en", Label: "Английский"},
								{Value: "ru", Label: "Русский"},
							},
						},
					},
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
					ID: "local", Name: "Local Server", URL: "https://localhost:8765", AutoPair: true, Active: true,
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
			Repair:           "Сопрячь заново",
			RepairConfirm:    "Доверять новому сертификату?",
			RemoveProfile:    "Удалить профиль",
			RemoveConfirm:    "Удалить этот профиль?",
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
			ExposureConnect:  "Адрес подключения: %s.",
			CopyAddress:      "Копировать адрес",
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

func TestPageLaysServerSettingsOutInColumns(t *testing.T) {
	page := shell.Page(shell.Snapshot{Version: "1.0.0"}, managementView())
	if !strings.Contains(page, `class="group group-wide server-settings"`) {
		t.Fatal("the Server settings form does not span the pane")
	}
	if !strings.Contains(page, `class="server-fields"`) || !strings.Contains(page, `class="server-save"`) {
		t.Fatal("the Server settings body and its Save footer are not split")
	}
	if !strings.Contains(page, ".server-fields { display:grid;") {
		t.Fatal("the Server fields are not laid out in columns")
	}
	if strings.Contains(page, ".server-fields { flex:1 1 auto;") {
		t.Fatal("the Server fields collapsed into a nested scroller")
	}
}

func TestPageDismissesNoticesAndErrors(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	for _, want := range []string{
		`var dismissedAlerts = {}`,
		`var closeNoticeLabel =`,
		`close.className = "message-close"`,
		`close.setAttribute("aria-label", closeNoticeLabel)`,
		`dismissedAlerts[item.key] = true`,
		`dismissedAlerts = {}`,
		`if (signature === lastAlerts) return;`,
		`.messages { position:relative; z-index:3;`,
		`#settings { position:relative; z-index:1;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestServerScreenSplitsCardsAndUsesIconProfileActions(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	for _, want := range []string{
		`data-section="server-pair"`,
		`data-section="server-control"`,
		`class="icon-button" data-action="refresh-servers"`,
		`class="icon-button" data-action="use-profile"`,
		`class="icon-button danger" data-action="remove-profile"`,
		`class="server-actions"`,
		`class="help-tip"`,
		`data-tip="`,
		`id="help-tip-float"`,
		`#help-tip-float.is-open { display:block; }`,
		`helpTipFloat.style.top =`,
		`var pane = target.closest(".settings-pane");`,
		`helpTipFloat.style.maxWidth = available + "px";`,
		`if (left > maxLeft) left = maxLeft;`,
		`if (left < minLeft) left = minLeft;`,
		`overflow-y:auto; overflow-x:hidden;`,
		`class="group group-wide server-settings"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSettingsPageSplitsScreensIntoANavigationMenu(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	for _, want := range []string{
		`id="settings-nav"`,
		`data-nav="general" data-targets="general"`,
		`data-nav="dictation" data-targets="dictation"`,
		`data-nav="server" data-targets="servers,server-pair,server-control,exposure,devices,server-settings"`,
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
	if strings.Index(page, `data-nav="dictation"`) > strings.Index(page, `data-nav="server"`) {
		t.Error("the navigation order is wrong")
	}
}

func TestSettingsNavigationGroupsRelatedScreens(t *testing.T) {
	view := managementView()
	view.Sections = append(view.Sections, dictation.Section{ID: "appearance", Title: "Indicator"})
	view.ModelLabels.Title = "Models"
	view.Spool.Labels.Title = "Spool"
	view.Startup.Available = true
	view.Update = shell.UpdateView{UpdateLabels: shell.UpdateLabels{Title: "Updates"}}
	page := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-nav="general" data-targets="general,startup,updates"`,
		`data-nav="dictation" data-targets="dictation"`,
		`data-nav="appearance" data-targets="appearance"`,
		`data-nav="models" data-targets="models,models-idle,models-local"`,
		`data-nav="spool" data-targets="spool"`,
		// The grouped labels follow the Russian interface language.
		`Общие</button>`,
		`Распознавание речи</button>`,
		`Индикатор на экране</button>`,
		`Сервер</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing grouped entry %q", want)
		}
	}
	if got := strings.Count(page, `data-nav="`); got != 7 {
		t.Errorf("navigation has %d entries, want 7 grouped entries", got)
	}
}

func TestModelsScreenSplitsIntoLogicalCards(t *testing.T) {
	view := sampleView()
	view.ModelLabels = shell.ModelLabels{
		Title:         "Модели",
		CatalogTitle:  "Каталог моделей",
		Refresh:       "Обновить",
		NoCatalog:     "Загрузите каталог.",
		IdleTitle:     "Выгрузка при простое",
		AdvancedTitle: "Локальная папка модели",
	}
	view.Servers.Models = &servers.ModelsView{}
	page := shell.SettingsHTML(view)

	wantNav := `data-nav="models" data-targets="models,models-idle,models-local"`
	if !strings.Contains(page, wantNav) {
		t.Errorf("models navigation does not show every card: %s", page)
	}
	// The catalog takes the full row; the two compact form cards share the
	// next one, like the Dictation and Server screens.
	for _, want := range []string{
		`class="group group-wide" data-section="models"`,
		`class="group" data-section="models-idle"`,
		`class="group" data-section="models-local"`,
		`>Каталог моделей</h2>`,
		`>Выгрузка при простое</h2>`,
		`>Локальная папка модели</h2>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Models screen missing card %q", want)
		}
	}
}

func TestDictationScreenSplitsIntoLogicalCards(t *testing.T) {
	view := sampleView()
	view.Sections = []dictation.Section{
		{ID: "dictation-hotkeys", Title: "Hotkeys"},
		{ID: "dictation-microphone", Title: "Microphone"},
		{ID: "dictation-language", Title: "Spoken language"},
		{ID: "dictation-feedback", Title: "Feedback"},
		{ID: "dictation-paste", Title: "Insertion"},
		{ID: "dictation-storage", Title: "Recordings folder"},
	}
	page := shell.SettingsHTML(view)
	wantNav := `data-nav="dictation" data-targets="dictation-hotkeys,dictation-microphone,dictation-language,dictation-feedback,dictation-paste,dictation-storage"`
	if !strings.Contains(page, wantNav) {
		t.Errorf("dictation navigation does not show every card: %s", page)
	}
	// Hotkeys and insertion need the full width; the three middle cards share
	// a row, like the General screen.
	for _, want := range []string{
		`class="group group-wide" data-section="dictation-hotkeys"`,
		`class="group" data-section="dictation-microphone"`,
		`class="group" data-section="dictation-language"`,
		`class="group" data-section="dictation-feedback"`,
		`class="group group-wide" data-section="dictation-paste"`,
		`class="group" data-section="dictation-storage"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing card %q", want)
		}
	}
	// The screen is identified by the navigation entry, not by a heading over
	// the whole group of cards.
	if !strings.Contains(page, `>Hotkeys</h2>`) {
		t.Error("the hotkeys card lost its heading")
	}
	if strings.Contains(page, `Распознавание речи</h2>`) {
		t.Error("the removed whole-screen heading is still rendered")
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
		"Интерфейс",
		"Тема",
		"Системная",
		"Светлая",
		"Тёмная",
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

func TestPageRendersTheAutostartSwitchAndModeSelector(t *testing.T) {
	view := managementView()
	view.Startup = shell.StartupView{
		Available:       true,
		Enabled:         false,
		Mode:            components.ModeClientAndServer,
		Modes:           []components.Mode{components.ModeClientAndServer, components.ModeClientOnly},
		ClientInstalled: true,
		ServerInstalled: true,
	}
	view.Labels.StartupTitle = "Autostart"
	view.Labels.StartupHelp = "Choose what starts at login."
	view.Labels.StartupSwitch = "Start at login"
	view.Labels.StartupMode = "What starts at login"
	view.Labels.StartupModeClientServer = "Client and Server"
	view.Labels.StartupModeClientServerHelp = "Starts both."
	view.Labels.StartupModeClient = "Client only"
	view.Labels.StartupModeClientHelp = "The Server starts on demand."
	page := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-section="startup"`,
		`data-nav="general" data-targets="general,startup"`,
		`data-startup-switch`,
		`data-startup-mode="client-server"`,
		`data-startup-mode="client"`,
		`id="diagnostics-log-level"`,
		`<option value="info" selected>Info</option>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, `data-startup-switch checked`) {
		t.Error("a fresh install must render autostart switched off")
	}
	if !strings.Contains(page, `data-startup-modes disabled`) {
		t.Error("the mode selector must be read-only while autostart is off")
	}
	if !strings.Contains(page, `value="client-server" data-startup-mode="client-server" checked`) {
		t.Error("the selector does not default to Client and Server")
	}
	if strings.Contains(page, `data-startup-mode="server"`) {
		t.Error("the page offers a mode whose Component is not installed")
	}

	view.Startup.Enabled = true
	page = shell.SettingsHTML(view)
	if !strings.Contains(page, `data-startup-switch checked`) {
		t.Error("an enabled autostart switch is not checked")
	}
	if strings.Contains(page, `data-startup-modes disabled`) {
		t.Error("the mode selector must be editable while autostart is on")
	}

	const hostile = `</fieldset><img src=x onerror="alert(1)">&`
	view.Labels.StartupModeClient = hostile
	page = shell.SettingsHTML(view)
	if strings.Contains(page, hostile) {
		t.Fatal("the mode selector embedded an unescaped label")
	}
	if !strings.Contains(page, "&lt;/fieldset&gt;") {
		t.Fatal("the mode selector did not escape the label's angle brackets")
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

func TestPageKeepsUnsavedEditsAcrossASettingsRefresh(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())

	start := strings.Index(page, "function renderSettings(v)")
	if start < 0 {
		t.Fatal("the page has no settings renderer")
	}
	end := strings.Index(page[start:], "function renderSpool(")
	if end < 0 {
		t.Fatal("could not delimit the settings renderer")
	}
	renderer := page[start : start+end]

	// A refresh rebuilds the fragment, so the values the user typed but has not
	// saved must be captured before the rebuild and put back afterwards. The
	// caret moves with them, or the next keystroke lands in the wrong place.
	replace := strings.Index(renderer, "host.innerHTML =")
	capture := strings.Index(renderer, "var edits = captureEdits(host);")
	caret := strings.Index(renderer, "var focus = captureFocus(host);")
	restore := strings.Index(renderer, "restoreEdits(host, edits);")
	restoreCaret := strings.Index(renderer, "restoreFocus(host, focus);")
	if capture < 0 || replace < 0 || capture > replace {
		t.Fatal("the settings renderer rebuilds the form before saving the user's edits")
	}
	if caret < 0 || caret > replace {
		t.Fatal("the settings renderer rebuilds the form before remembering the caret")
	}
	if restore < 0 || restore < replace {
		t.Fatal("the settings renderer does not restore the user's edits after the rebuild")
	}
	if restoreCaret < 0 || restoreCaret < replace {
		t.Fatal("the settings renderer does not restore the caret after the rebuild")
	}

	for _, want := range []string{
		`markRendered(document.getElementById("settings"))`,
		"function markRendered(",
		"function captureEdits(",
		"function restoreEdits(",
		"function captureFocus(",
		"function restoreFocus(",
		// A control that saves itself on change must follow Go, not the page.
		`el.dataset.path === "client.ui_language"`,
		`el.closest("[data-startup-switch], [data-startup-mode], [data-log-level]")`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not keep unsaved edits: missing %q", want)
		}
	}

	// The wizard form is replaced on its own, so it needs the same treatment.
	wizardStart := strings.Index(page, "function renderWizard(v)")
	if wizardStart < 0 {
		t.Fatal("the page has no wizard renderer")
	}
	wizardEnd := strings.Index(page[wizardStart:], "function renderMessages(")
	if wizardEnd < 0 {
		t.Fatal("could not delimit the wizard renderer")
	}
	wizard := page[wizardStart : wizardStart+wizardEnd]
	wizardReplace := strings.Index(wizard, "current.replaceWith(next)")
	wizardCapture := strings.Index(wizard, "captureEdits(current)")
	if wizardReplace < 0 || wizardCapture < 0 || wizardCapture > wizardReplace {
		t.Fatal("the wizard renderer rebuilds the step before saving the user's edits")
	}
	if strings.Index(wizard, "restoreEdits(rendered, edits)") < wizardReplace {
		t.Fatal("the wizard renderer does not restore the user's edits after the rebuild")
	}
}

func TestPageHasNoVisibleHeaderAndUpdatesTheWindowTitle(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	if strings.Contains(page, `class="page-head"`) || strings.Contains(page, `id="page-title"`) || strings.Contains(page, `id="page-subtitle"`) {
		t.Fatal("the page still renders a title block above the status card")
	}
	if !strings.Contains(page, `<title>kitsune-whisper</title>`) {
		t.Fatal("the page lost its window title")
	}
	// renderSettings receives the whole view, so a language switch must still
	// refresh the native window title.
	if !strings.Contains(page, `document.title = v.title`) {
		t.Errorf("settings renderer does not update the window title")
	}
}

func TestPageKeepsEveryErrorInTheOneAreaUnderTheStatusCard(t *testing.T) {
	view := managementView()
	view.DeviceError = "microphone list failed"
	page := shell.Page(shell.Snapshot{Error: "runtime failure"}, view)
	if strings.Contains(page, `class="device-error"`) {
		t.Fatal("the page still renders a second error line outside the message area")
	}
	if strings.Contains(page, `id="error"`) {
		t.Fatal("the page still has a separate runtime error banner")
	}
	status := strings.Index(page, `class="card status"`)
	messages := strings.Index(page, `id="messages"`)
	settings := strings.Index(page, `id="settings"`)
	if status < 0 || messages < 0 || settings < 0 || status > messages || messages > settings {
		t.Fatalf("the message area is not between the status card and the settings: status=%d messages=%d settings=%d", status, messages, settings)
	}
	for _, want := range []string{
		`runtimeError = s.error || ""`,
		`function renderAlerts()`,
		`microphone list failed`,
		`if (item.section && !visible[item.section]) return;`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestUpdatesCardShowsVersionsTheAutomaticToggleAndTheCheckButton(t *testing.T) {
	view := managementView()
	view.Update = shell.UpdateView{
		UpdateLabels: shell.UpdateLabels{
			Title:         "Обновления",
			Help:          "The Client checks GitHub Releases on its own.",
			AutoCheck:     "Проверять обновления автоматически",
			AutoCheckHelp: "Проверка запускается примерно через минуту после старта.",
			Current:       "Текущая версия",
			Latest:        "Последняя версия",
			Check:         "Проверить обновления",
		},
		CurrentVersion: "1.2.3",
		CheckEnabled:   true,
	}
	html := shell.SettingsHTML(view)
	for _, unwanted := range []string{"The Client checks GitHub Releases on its own."} {
		if strings.Contains(html, unwanted) {
			t.Errorf("the updates card still shows its explanatory text: %q", unwanted)
		}
	}
	for _, want := range []string{
		"Текущая версия", "1.2.3", "Последняя версия", "Проверить обновления",
		`data-path="client.updates.check" checked`,
		"Проверять обновления автоматически",
		"Проверка запускается примерно через минуту после старта.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the updates card is missing %q", want)
		}
	}

	view.Update.CheckEnabled = false
	html = shell.SettingsHTML(view)
	if strings.Contains(html, `data-path="client.updates.check" checked`) {
		t.Error("the toggle is checked while automatic checks are disabled")
	}
	if !strings.Contains(html, `data-path="client.updates.check"`) {
		t.Error("the updates card lost the automatic-check toggle")
	}
}

func TestUpdatesCardShowsDownloadProgressAndCancel(t *testing.T) {
	view := managementView()
	view.Update = shell.UpdateView{
		UpdateLabels: shell.UpdateLabels{
			Title:            "Обновления",
			Check:            "Проверить обновления",
			UpdateTo:         "Обновить до v%s…",
			DownloadProgress: "Загрузка %s — %d%% (%s из %s)",
			DownloadingFile:  "Загрузка %s…",
			Cancel:           "Отменить загрузку",
			Retry:            "Повторить загрузку",
			ReadyMessage:     "Готово к установке.",
		},
		CurrentVersion: "1.2.3",
		LatestVersion:  "1.3.0",
		Available:      true,
		Installable:    true,
		Downloading:    true,
		ReceivedBytes:  1_500_000,
		TotalBytes:     3_000_000,
		CurrentFile:    "kitsune-client_linux_amd64.tar.gz",
		Cancelable:     true,
	}
	html := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-update-downloading="true"`,
		`data-update-percent="50"`,
		"50%",
		"1.5 MB",
		"3.0 MB",
		"kitsune-client_linux_amd64.tar.gz",
		`data-action="cancel-update"`,
		"Отменить загрузку",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the downloading card is missing %q", want)
		}
	}

	view.Update.TotalBytes = 0
	html = shell.SettingsHTML(view)
	if !strings.Contains(html, `data-update-indeterminate="true"`) {
		t.Error("an unknown download size did not render an indeterminate meter")
	}
	if strings.Contains(html, `data-update-percent=`) {
		t.Error("an unknown download size rendered a percentage")
	}
}

func TestUpdatesCardDistinguishesReadyAndFailedDownloads(t *testing.T) {
	view := managementView()
	view.Update = shell.UpdateView{
		UpdateLabels: shell.UpdateLabels{
			Title:            "Обновления",
			Check:            "Проверить обновления",
			UpdateTo:         "Обновить до v%s…",
			ReadyMessage:     "Обновление скачано и проверено. Оно готово к установке.",
			AvailableMessage: "Доступна более новая версия.",
			Retry:            "Повторить загрузку",
			Cancel:           "Отменить загрузку",
		},
		CurrentVersion: "1.2.3",
		LatestVersion:  "1.3.0",
		Available:      true,
		Installable:    true,
		Ready:          true,
	}
	html := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-update-ready="true"`,
		"Обновление скачано и проверено. Оно готово к установке.",
		`data-action="install-update"`,
		"Обновить до v1.3.0…",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the ready card is missing %q", want)
		}
	}
	if strings.Contains(html, "Доступна более новая версия.") {
		t.Error("the ready card still shows the available message")
	}

	view.Update.Ready = false
	view.Update.Error = "checksum mismatch"
	html = shell.SettingsHTML(view)
	for _, want := range []string{
		`data-update-error="true"`,
		"checksum mismatch",
		`data-action="retry-update"`,
		"Повторить загрузку",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the failed card is missing %q", want)
		}
	}
}

func TestUpdatesCardShowsTheInstallResultAndDismiss(t *testing.T) {
	view := managementView()
	view.Update = shell.UpdateView{
		UpdateLabels: shell.UpdateLabels{
			Title:            "Обновления",
			Check:            "Проверить обновления",
			InstalledMessage: "Обновлено до v%s.",
			Dismiss:          "Скрыть",
		},
		CurrentVersion: "1.3.0",
		ResultStatus:   "success",
		ResultTag:      "v1.3.0",
		ResultMessage:  "Обновлено до v1.3.0.",
	}
	html := shell.SettingsHTML(view)
	for _, want := range []string{
		`data-update-result="success"`,
		"Обновлено до v1.3.0.",
		`data-action="dismiss-update"`,
		"Скрыть",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the result card is missing %q", want)
		}
	}
}

func TestUpdatesCardShowsTheInstallInProgressState(t *testing.T) {
	view := managementView()
	view.Update = shell.UpdateView{
		UpdateLabels: shell.UpdateLabels{
			Title:             "Обновления",
			Check:             "Проверить обновления",
			InstallingMessage: "Завершаем текущую диктовку и устанавливаем обновление…",
		},
		CurrentVersion: "1.2.3",
		LatestVersion:  "1.3.0",
		Available:      true,
		Installable:    true,
		Installing:     true,
	}
	html := shell.SettingsHTML(view)
	if !strings.Contains(html, `data-update-installing="true"`) {
		t.Error("the card does not show that the update is installing")
	}
	if !strings.Contains(html, "Завершаем текущую диктовку") {
		t.Error("the installing message is missing")
	}
	if strings.Contains(html, `data-action="install-update"`) {
		t.Error("the install button is still clickable while installing")
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

func TestPageShowsTheLANConnectAddressWithACopyAction(t *testing.T) {
	view := sampleView()
	view.Servers.Profiles[0].Health.Network = &trust.NetworkStatus{
		ExposeToNetwork: true, BindHost: "0.0.0.0", Port: 8765,
	}
	view.Servers.ConnectAddress = "192.168.3.22:8765"
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`data-exposure-connect="192.168.3.22:8765"`,
		"Адрес подключения: 192.168.3.22:8765.",
		`data-action="copy-address"`,
		"Копировать адрес",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPageHidesTheConnectAddressWhenLANExposureIsOff(t *testing.T) {
	view := sampleView()
	view.Servers.Profiles[0].Health.Network = &trust.NetworkStatus{
		BindHost: "127.0.0.1", Port: 8765,
	}
	page := shell.Page(shell.Snapshot{}, view)
	if strings.Contains(page, `data-exposure-connect`) || strings.Contains(page, `data-action="copy-address"`) {
		t.Fatal("a loopback-only Server still offers a LAN connect address")
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
		`data-action="remove-profile"`,
		`data-profile-id="remote-desktop"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The active profile is marked and is not offered a redundant switch.
	// Other profile actions (Remove profile) still name the active profile.
	if strings.Contains(page, `data-action="use-profile" data-profile-id="local"`) {
		t.Error("the active profile still renders a switch button")
	}
}

func TestPageOffersRepairOnlyForAChangedOrUnpairedCertificate(t *testing.T) {
	view := sampleView()
	view.Servers.Profiles[1].Health = trust.Status{State: trust.StateMismatch, Error: "certificate changed"}
	page := shell.Page(shell.Snapshot{}, view)
	if !strings.Contains(page, `data-action="repair-profile" data-profile-id="remote-desktop"`) {
		t.Fatal("a changed Remote certificate has no Pair again action")
	}
	if !strings.Contains(page, `data-profile-local="false"`) {
		t.Fatal("a Remote profile was rendered as the Local Server")
	}

	view.Servers.Profiles[0].Health = trust.Status{State: trust.StateMismatch}
	page = shell.Page(shell.Snapshot{}, view)
	if !strings.Contains(page, `data-profile-local="true"`) {
		t.Fatal("the Local Server did not get the one-click repair path")
	}
	if !strings.Contains(page, "Доверять новому сертификату?") {
		t.Fatal("the Local Server repair has no confirmation")
	}

	view.Servers.Profiles[1].Health = trust.Status{State: trust.StateOffline}
	page = shell.Page(shell.Snapshot{}, view)
	if strings.Contains(page, `data-action="repair-profile" data-profile-id="remote-desktop"`) {
		t.Fatal("an unreachable Server was offered a repair action")
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
		CatalogTitle:      "Каталог моделей",
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
		"Каталог моделей",
		"Рекомендация",
		"145.0 MB",
		"600.0 MB",
		"быстро",
		"хорошее",
		`data-model="base"`,
		`data-action="download-model"`,
		`title="Скачать" aria-label="Скачать"`,
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
	view.Servers.Lifecycle = &servers.ServerLifecycle{ServerLifecycle: api.ServerLifecycle{Loaded: true, ModelState: "loaded"}}
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

func TestWizardServerStepShowsOnlyTheConnectionForms(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	_ = wiz.GoTo(wizard.StepServer)
	data := wizard.Data{
		Catalog: i18n.For(i18n.Russian),
		Servers: servers.View{
			ActiveID: "local",
			Profiles: []servers.Profile{{ID: "local", Name: "Local Server", URL: "https://localhost:8765", Active: true}},
		},
	}
	page := shell.SettingsHTML(shell.SettingsView{Wizard: wiz.View(data)})
	if strings.Contains(page, `data-profile="Local Server"`) {
		t.Fatal("the Server step repeats the active profile that the status bar already names")
	}
	if !strings.Contains(page, `data-action="wizard-pair-remote"`) {
		t.Fatal("the Server step lost its remote pairing form")
	}
}

func TestWizardModelStepFitsWithoutTheDeferButton(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	_ = wiz.GoTo(wizard.StepModel)
	data := wizard.Data{
		Catalog: i18n.For(i18n.Russian),
		Servers: servers.View{Models: &servers.ModelsView{
			Device: "cpu", State: "loaded",
			Models: []servers.Model{
				{Name: "base", DiskBytes: 145_000_000, MemoryBytes: 600_000_000, Installed: true},
				{Name: "small", DiskBytes: 484_000_000, MemoryBytes: 1_300_000_000},
			},
		}},
	}
	page := shell.SettingsHTML(shell.SettingsView{Wizard: wiz.View(data)})
	if strings.Contains(page, `data-action="wizard-model-defer"`) {
		t.Fatal("the Model step still offers a second skip button")
	}
	if !strings.Contains(page, `data-action="wizard-model-download"`) {
		t.Fatal("the Model step lost its download action")
	}
}

func TestWizardRailAndFooterStayCompact(t *testing.T) {
	wiz, _ := newWizardService(t)
	_ = wiz.Open()
	page := shell.SettingsHTML(shell.SettingsView{Wizard: wiz.View(wizard.Data{Catalog: i18n.For(i18n.Russian)})})
	if strings.Contains(page, "wizard-step-status") {
		t.Fatal("the step list still prints a second status line under every step")
	}
	skipAll := strings.Index(page, `data-action="wizard-skip-all"`)
	skip := strings.Index(page, `data-action="wizard-skip"`)
	if skipAll < 0 || skip < 0 || skipAll > skip {
		t.Fatal("Skip setup must come before Skip this step")
	}
}

func TestWizardKeepsTheStepScrollAcrossARefresh(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, sampleView())
	start := strings.Index(page, "function renderWizard(v)")
	if start < 0 {
		t.Fatal("the page has no wizard renderer")
	}
	end := strings.Index(page[start:], "function renderMessages(")
	if end < 0 {
		t.Fatal("could not delimit the wizard renderer")
	}
	renderer := page[start : start+end]
	for _, want := range []string{
		`querySelector(".wizard-body")`,
		`nextBody.scrollTop = bodyScroll`,
		`nextSteps.scrollTop = stepsScroll`,
		"window.requestAnimationFrame",
	} {
		if !strings.Contains(renderer, want) {
			t.Errorf("the wizard renderer does not preserve the step scroll: missing %q", want)
		}
	}
}

func TestSettingsPaneLaysShortCardsInColumns(t *testing.T) {
	page := shell.Page(shell.Snapshot{}, managementView())
	for _, want := range []string{
		`.settings-layout:not(.settings-single) .settings-pane { flex-direction:row; flex-wrap:wrap;`,
		`.settings-layout:not(.settings-single) .settings-pane > .group-wide { flex:1 1 100%; }`,
		`.field-grid { display:grid;`,
		`class="group group-wide server-settings"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the Settings pane does not lay cards out in columns: missing %q", want)
		}
	}
}

func TestPageRendersTheThemeChoiceAndItsPalette(t *testing.T) {
	view := sampleView()
	view.Theme = "dark"
	page := shell.Page(shell.Snapshot{}, view)
	for _, want := range []string{
		`<html lang="ru" data-theme="dark">`,
		`data-theme-choice="system"`,
		`data-theme-choice="light"`,
		`data-theme-choice="dark"`,
		`data-path="client.theme"`,
		`function applyTheme(value)`,
		`applyTheme(v.theme);`,
		`el.dataset.path === "client.theme"`,
		`sendIntent({ action: "save", patch: { theme: themeValue } });`,
		`:root[data-theme="dark"]`,
		`:root[data-theme="system"]`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
