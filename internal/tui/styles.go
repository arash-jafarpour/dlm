package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	rowLabelWidth = 26
	rowBarWidth   = 24
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
)

// renderRow draws one live progress line, shaped like the plain-CLI rows so
// the board feels like the same tool.
func renderRow(r *Row) string {
	current := r.Current()
	elapsed := time.Since(r.Start).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(current) / elapsed
	}

	label := r.Label
	if len(label) > rowLabelWidth {
		label = label[:rowLabelWidth-1] + "…"
	}
	label = fmt.Sprintf("%-*s", rowLabelWidth, label)

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
		filled := int(percent / 100 * rowBarWidth)
		bar = StyleBarFull.Render(strings.Repeat("█", filled)) +
			StyleBarEmpty.Render(strings.Repeat("░", rowBarWidth-filled))
	} else {
		bar = StyleBarEmpty.Render(strings.Repeat("·", rowBarWidth))
	}

	var eta string
	if r.Total > 0 && speed > 0 && current < r.Total {
		if secs := float64(r.Total-current) / speed; secs >= 1 {
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
