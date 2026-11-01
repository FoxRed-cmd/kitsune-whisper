package i18n_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
)

func TestDetectFollowsTheEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want i18n.Lang
	}{
		{"russian lang", map[string]string{"LANG": "ru_RU.UTF-8"}, i18n.Russian},
		{"russian dash locale", map[string]string{"LANG": "ru-RU"}, i18n.Russian},
		{"english lang", map[string]string{"LANG": "en_US.UTF-8"}, i18n.English},
		{"unsupported language falls back", map[string]string{"LANG": "de_DE.UTF-8"}, i18n.English},
		{"no locale", map[string]string{}, i18n.English},
		{"lc_all wins over lang", map[string]string{"LC_ALL": "ru_RU.UTF-8", "LANG": "en_US.UTF-8"}, i18n.Russian},
		{"language wins over lang", map[string]string{"LANGUAGE": "ru:en", "LANG": "en_US.UTF-8"}, i18n.Russian},
		{"language list tries each entry", map[string]string{"LANGUAGE": "de:ru:en"}, i18n.Russian},
		{"posix locale is english", map[string]string{"LC_ALL": "C", "LANGUAGE": "ru"}, i18n.English},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := i18n.DetectWith(tc.env, ""); got != tc.want {
				t.Fatalf("DetectWith(%v) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

func TestDetectFallsBackToTheSystemLocale(t *testing.T) {
	if got := i18n.DetectWith(nil, "ru-RU"); got != i18n.Russian {
		t.Fatalf("system locale ru-RU = %q, want Russian", got)
	}
	if got := i18n.DetectWith(map[string]string{"LANG": "de_DE.UTF-8"}, "ru-RU"); got != i18n.Russian {
		t.Fatalf("system locale after an unsupported LANG = %q, want Russian", got)
	}
	if got := i18n.DetectWith(map[string]string{"LANG": "en_GB.UTF-8"}, "ru-RU"); got != i18n.English {
		t.Fatalf("an explicit supported LANG must win: got %q", got)
	}
}

func TestResolveLanguageSetting(t *testing.T) {
	env := map[string]string{"LANG": "ru_RU.UTF-8"}
	cases := map[string]i18n.Lang{
		"auto":      i18n.Russian,
		"":          i18n.Russian,
		"en":        i18n.English,
		"ru":        i18n.Russian,
		"EN":        i18n.English,
		"automatic": i18n.Russian,
	}
	for setting, want := range cases {
		if got := i18n.ResolveWith(setting, env, ""); got != want {
			t.Errorf("ResolveWith(%q) = %q, want %q", setting, got, want)
		}
	}
}

func TestCatalogTranslatesAndFallsBackToEnglish(t *testing.T) {
	catalog := i18n.New(i18n.Russian, map[string]string{
		"greeting": "Привет, %s",
		"shared":   "по-русски",
	})
	if got := catalog.T("greeting", "мир"); got != "Привет, мир" {
		t.Fatalf("translated message = %q", got)
	}
	// A key missing from Russian must fall back to the English catalog.
	if got := catalog.T("tray.quit"); got != i18n.For(i18n.English).T("tray.quit") {
		t.Fatalf("missing-key fallback = %q, want the English string", got)
	}
	// A key missing everywhere is reported as the key, never as empty text.
	if got := catalog.T("not.a.real.key"); got != "not.a.real.key" {
		t.Fatalf("unknown key = %q, want the key itself", got)
	}
}

func TestCatalogExposesItsLanguageAndKeys(t *testing.T) {
	catalog := i18n.For(i18n.Russian)
	if catalog.Lang() != i18n.Russian {
		t.Fatalf("lang = %q", catalog.Lang())
	}
	keys := catalog.Keys()
	if len(keys) == 0 {
		t.Fatal("catalog has no keys")
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatal("catalog keys are not sorted")
	}
	if !catalog.Has(keys[0]) {
		t.Fatalf("Has(%q) = false for a reported key", keys[0])
	}
	if catalog.Has("not.a.real.key") {
		t.Fatal("Has reported a key the catalog does not have")
	}
}

func TestEnglishAndRussianCatalogsCoverTheSameKeys(t *testing.T) {
	english := i18n.For(i18n.English)
	russian := i18n.For(i18n.Russian)
	enKeys := english.Keys()
	ruKeys := russian.Keys()
	if len(enKeys) != len(ruKeys) {
		t.Fatalf("catalog key counts differ: en=%d ru=%d", len(enKeys), len(ruKeys))
	}
	for i := range enKeys {
		if enKeys[i] != ruKeys[i] {
			t.Fatalf("catalog keys differ at %d: en=%q ru=%q", i, enKeys[i], ruKeys[i])
		}
	}
	for _, key := range enKeys {
		if strings.TrimSpace(english.T(key)) == "" {
			t.Errorf("English message %q is empty", key)
		}
		if strings.TrimSpace(russian.T(key)) == "" {
			t.Errorf("Russian message %q is empty", key)
		}
	}
}

func TestRussianCatalogTranslatesTheSettingsScreen(t *testing.T) {
	russian := i18n.For(i18n.Russian)
	cases := map[string]string{
		"section.dictation":    "Диктовка",
		"field.hotkey":         "Горячая клавиша диктовки",
		"action.save":          "Сохранить",
		"action.restart":       "Перезапустить клиент",
		"badge.restart":        "Нужен перезапуск",
		"tray.quit":            "Выход",
		"tray.start":           "Начать диктовку",
		"error.hotkey.invalid": "«%s» — недопустимое сочетание клавиш. Используйте модификаторы и клавишу, например Ctrl+Shift+Space.",
	}
	for key, want := range cases {
		if got := russian.T(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
