package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	StyleTitle       = lipgloss.NewStyle().Bold(true)
	StyleDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	StyleOK          = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	StyleErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	StyleWarn        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	StyleBarFull     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	StyleBarEmpty    = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	StyleTabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	StyleTabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	StyleStarting    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	// StyleUpdate highlights the release notice above the footer: bold and
	// bright, so it cannot be mistaken for the dim status/hint/footer text
	// it sits between. The color adapts to light terminals.
	StyleUpdate = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "213"})
)

// renderRow draws one live progress line, shaped like the plain-CLI rows so
// the board feels like the same tool. Label and bar widths come from the
// terminal layout; maxWidth (<=0 = unlimited) lets the ETA be dropped when
// the line would not fit.
func renderRow(r *Row, labelW, barW, maxWidth int) string {
	current := r.Current()
	elapsed := time.Since(r.Start).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(current) / elapsed
	}

	label := r.Label
	if len(label) > labelW {
		label = label[:labelW-1] + "…"
	}
	label = fmt.Sprintf("%-*s", labelW, label)

	var bar string
	percent := 100.0
	if r.Total > 0 {
		pct := float64(current) / float64(r.Total)
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		percent = pct * 100
		filled := int(percent / 100 * float64(barW))
		bar = StyleBarFull.Render(strings.Repeat("█", filled)) +
			StyleBarEmpty.Render(strings.Repeat("░", barW-filled))
	} else {
		bar = StyleBarEmpty.Render(strings.Repeat("·", barW))
	}

	// Row cells besides label+bar: marker(2) + the percent/counters/speed
	// block (41) and, when it fits, an ETA (~9 more).
	const fixedCells = 41
	const etaCells = 9

	var eta string
	if r.Total > 0 && speed > 0 && current < r.Total {
		secs := float64(r.Total-current) / speed
		fits := maxWidth <= 0 || 2+labelW+barW+fixedCells+etaCells <= maxWidth
		if secs >= 1 && fits {
			eta = "  ETA " + humanETA(secs)
		}
	}

	return fmt.Sprintf("%s %s %5.1f%%  %9s/%-9s %9s/s%s",
		StyleTitle.Render(label), bar, percent,
		humanBytes(current), humanBytes(r.Total), humanBytes(int64(speed)), eta)
}

// lipglossWidth measures a styled string's visible width.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func humanETA(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", int(seconds))
	}
	minutes := int(seconds / 60)
	secs := int(seconds) % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%ds", minutes, secs)
	}
	hours := minutes / 60
	mins := minutes % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
}
