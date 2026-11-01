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
			Subtitle:    "Приватная диктовка в локальной сети",
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
					Title: "Диктовка",
					Fields: []dictation.Field{
						{
							Path:   "client.hotkey",
							Label:  "Горячая клавиша диктовки",
							Help:   "Нажмите сочетание в поле.",
							Kind:   dictation.KindHotkey,
							Value:  "Ctrl+Alt+D",
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
			NoServers:        "Нет профилей.",
			ExposureTitle:    "Доступ из локальной сети",
			ExposureHelp:     "Разрешить клиентам из сети.",
			ExposureOn:       "Разрешить доступ из сети",
			ExposureOff:      "Запретить доступ из сети",
			ExposureConfirm:  "Разрешить доступ из сети?",
			ExposureOnState:  "включён",
			ExposureOffState: "выключен",
		},
	}
}

func ptrFloat(value float64) *float64 { return &value }

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
		"Диктовка",
		"Язык интерфейса",
		"Горячая клавиша диктовки",
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
	if !strings.Contains(page, `<p class="sub" id="page-subtitle">Приватная диктовка в локальной сети</p>`) {
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

func TestServerFieldsEscapeHostileValues(t *testing.T) {
	const hostile = `</section><img src=x onerror="alert(1)">&`
	view := sampleView()
	view.Servers.Profiles = []servers.Profile{{ID: hostile, Name: hostile, URL: hostile}}
	view.Servers.Devices = []servers.Device{{ID: hostile, Name: hostile}}
	view.Servers.PairingCode = &servers.PairingCode{Code: hostile}
	html := shell.SettingsHTML(view)
	if strings.Contains(html, "<img") {
		t.Fatal("Servers screen allowed an image injection")
	}
	if !strings.Contains(html, "&lt;/section&gt;") {
		t.Fatal("Servers screen did not escape a hostile value")
	}
}
