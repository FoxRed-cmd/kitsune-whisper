// Package i18n holds the Client's user-interface languages and message
// catalogs.
//
// English and Russian are supported. The language follows the operating
// system locale unless the user picks one explicitly; an unsupported or
// missing locale falls back to English. Catalogs are plain values, so the
// Settings window and the Tray can switch language at runtime without
// restarting the process.
package i18n

import (
	"fmt"
	"sort"
	"strings"
)

// Lang is a user-interface language.
type Lang string

const (
	// Auto follows the operating system locale.
	Auto Lang = "auto"
	// English is the fallback language.
	English Lang = "en"
	// Russian is the second supported language.
	Russian Lang = "ru"
)

// Supported lists the languages a user can pick, in display order.
func Supported() []Lang { return []Lang{English, Russian} }

// ParseLang reports whether value names a supported language. "auto" is
// accepted as a setting but is not itself a concrete language.
func ParseLang(value string) (Lang, bool) {
	switch Lang(strings.ToLower(strings.TrimSpace(value))) {
	case Auto:
		return Auto, true
	case English:
		return English, true
	case Russian:
		return Russian, true
	}
	return "", false
}

// Detect resolves the operating system locale from the environment and the
// platform's user locale. See DetectWith.
func Detect(env map[string]string) Lang { return DetectWith(env, systemLocale()) }

// DetectWith resolves the operating system locale from the given environment
// (LC_ALL, LANGUAGE, then LANG) and, when those name no supported language,
// the platform's user locale. A C/POSIX locale is English. Anything
// unsupported falls back to English.
func DetectWith(env map[string]string, userLocale string) Lang {
	for _, key := range []string{"LC_ALL", "LANGUAGE", "LANG"} {
		value := strings.TrimSpace(env[key])
		if value == "" {
			continue
		}
		if key == "LANGUAGE" {
			// GNU LANGUAGE is a colon-separated preference list.
			for _, item := range strings.Split(value, ":") {
				if lang, ok := parseLocale(item); ok {
					return lang
				}
			}
			continue
		}
		if lang, ok := parseLocale(value); ok {
			return lang
		}
	}
	if lang, ok := parseLocale(userLocale); ok {
		return lang
	}
	return English
}

// Resolve maps the ui_language setting to a concrete language: "auto" (or an
// empty/unknown value) follows the system locale, and an explicit "en"/"ru"
// wins.
func Resolve(setting string, env map[string]string) Lang {
	return ResolveWith(setting, env, systemLocale())
}

// ResolveWith is Resolve with an explicit platform locale, for tests.
func ResolveWith(setting string, env map[string]string, userLocale string) Lang {
	switch Lang(strings.ToLower(strings.TrimSpace(setting))) {
	case English:
		return English
	case Russian:
		return Russian
	default:
		return DetectWith(env, userLocale)
	}
}

// parseLocale maps one locale name such as "ru_RU.UTF-8" or "ru-RU" to a
// supported language. C and POSIX are treated as English, as the C library
// does.
func parseLocale(value string) (Lang, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if idx := strings.IndexAny(value, ".@"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.ReplaceAll(value, "-", "_")
	language := strings.ToLower(strings.SplitN(value, "_", 2)[0])
	switch language {
	case "en", "c", "posix":
		return English, true
	case "ru":
		return Russian, true
	}
	return "", false
}

// Catalog is one language's messages. The zero value is an empty English
// catalog; use For to get a built-in one.
type Catalog struct {
	lang     Lang
	messages map[string]string
}

// For returns the built-in catalog for lang, defaulting to English.
func For(lang Lang) Catalog {
	if lang == Russian {
		return Catalog{lang: Russian, messages: russian}
	}
	return Catalog{lang: English, messages: english}
}

// New builds a catalog from explicit messages. It exists so tests and future
// languages can construct a catalog without touching the built-in tables.
func New(lang Lang, messages map[string]string) Catalog {
	copied := make(map[string]string, len(messages))
	for key, value := range messages {
		copied[key] = value
	}
	return Catalog{lang: lang, messages: copied}
}

// Lang reports the catalog's language.
func (c Catalog) Lang() Lang { return c.lang }

// T returns the message for key. A key missing from this catalog falls back
// to English and, failing that, to the key itself, so the UI never shows an
// empty label. Arguments are formatted with fmt.Sprintf.
func (c Catalog) T(key string, args ...any) string {
	text, ok := c.messages[key]
	if !ok {
		text, ok = english[key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// Has reports whether this catalog itself defines key. It deliberately does
// not count the English fallback, so completeness tests are meaningful.
func (c Catalog) Has(key string) bool {
	_, ok := c.messages[key]
	return ok
}

// Keys returns the catalog's keys, sorted.
func (c Catalog) Keys() []string {
	keys := make([]string, 0, len(c.messages))
	for key := range c.messages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
