package ui

import "os"

const (
	colorReset  = "\x1b[0m"
	colorGreen  = "\x1b[32m"
	colorRed    = "\x1b[31m"
	colorYellow = "\x1b[33m"
	colorCyan   = "\x1b[36m"
	colorGray   = "\x1b[90m"
	colorBold   = "\x1b[1m"
)

// colorEnabled is decided once at package init: colors only wrap output
// when stdout is a real terminal and NO_COLOR is not set. Piped, redirected
// and cron runs get plain text, so log files stay grep-able.
var colorEnabled = detectColorEnabled()

func detectColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(os.Stdout)
}

func colorize(enabled bool, code, s string) string {
	if !enabled {
		return s
	}
	return code + s + colorReset
}

func Green(s string) string { return colorize(colorEnabled, colorGreen, s) }
func Red(s string) string   { return colorize(colorEnabled, colorRed, s) }
func Yellow(s string) string {
	return colorize(colorEnabled, colorYellow, s)
}
func Cyan(s string) string { return colorize(colorEnabled, colorCyan, s) }
func Gray(s string) string { return colorize(colorEnabled, colorGray, s) }
func Bold(s string) string { return colorize(colorEnabled, colorBold, s) }
