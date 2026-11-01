//go:build !windows

package i18n

// systemLocale is empty on Unix: the locale arrives through the environment
// (LC_ALL, LANGUAGE, LANG), which DetectWith already reads.
func systemLocale() string { return "" }
