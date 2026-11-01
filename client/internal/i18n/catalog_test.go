package i18n

import "testing"

// A partial catalog stands in for a translation that misses a key; the built-in
// catalogs are kept key-for-key identical by the external tests.
func TestCatalogTranslatesAndFallsBackToEnglish(t *testing.T) {
	catalog := Catalog{lang: Russian, messages: map[string]string{
		"greeting": "Привет, %s",
	}}
	if got := catalog.T("greeting", "мир"); got != "Привет, мир" {
		t.Fatalf("translated message = %q", got)
	}
	// A key missing from Russian must fall back to the English catalog.
	if got := catalog.T("tray.quit"); got != For(English).T("tray.quit") {
		t.Fatalf("missing-key fallback = %q, want the English string", got)
	}
	// A key missing everywhere is reported as the key, never as empty text.
	if got := catalog.T("not.a.real.key"); got != "not.a.real.key" {
		t.Fatalf("unknown key = %q, want the key itself", got)
	}
	if catalog.Lang() != Russian {
		t.Fatalf("lang = %q, want Russian", catalog.Lang())
	}
}
