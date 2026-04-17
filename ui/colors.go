package ui

const (
	colorReset  = "\x1b[0m"
	colorGreen  = "\x1b[32m"
	colorRed    = "\x1b[31m"
	colorYellow = "\x1b[33m"
	colorCyan   = "\x1b[36m"
	colorGray   = "\x1b[90m"
	colorBold   = "\x1b[1m"
)

func Green(s string) string {
	return colorGreen + s + colorReset
}

func Red(s string) string {
	return colorRed + s + colorReset
}

func Yellow(s string) string {
	return colorYellow + s + colorReset
}

func Cyan(s string) string {
	return colorCyan + s + colorReset
}

func Gray(s string) string {
	return colorGray + s + colorReset
}

func Bold(s string) string {
	return colorBold + s + colorReset
}
