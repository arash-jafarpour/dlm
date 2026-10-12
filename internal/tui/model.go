// Package tui implements the interactive `dlm ui` board: a live view over a
// queue run with per-file progress rows, built on bubbletea. It only ever
// runs on a real terminal; the plain snapshot path lives in cmd.
package tui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arash-jafarpour/dlm/ui"
)

// Tab identifies the board's current panel.
type Tab int

const (
	TabDownloads Tab = iota
	TabQueue
	TabCompleted
	TabSettings
)

var tabNames = []string{"downloads", "queue", "completed", "settings"}

// RowStatus is a row's lifecycle state.
type RowStatus int

const (
	RowRunning RowStatus = iota
	RowSaved
	RowFailed
)

// Row is one file's slot in the downloads panel. Byte counts are updated
// from worker goroutines through atomics; every other field is written only
// from the model's Update (single goroutine).
type Row struct {
	Label  string
	Total  int64
	Start  time.Time
	Status RowStatus
	Detail string

	current atomic.Int64
}

func newRow(label string, total, offset int64) *Row {
	r := &Row{
		Label: ui.CleanFileName(label),
		Total: total,
		Start: time.Now(),
	}
	r.current.Store(offset)
	return r
}

// Add implements downloader.ProgressBar; safe from chunk goroutines.
func (r *Row) Add(n int) { r.current.Add(int64(n)) }

// Done implements downloader.ProgressBar; the model freezes the row when
// the file's completion event arrives.
func (r *Row) Done() {}

func (r *Row) Current() int64 { return r.current.Load() }

// Options carries the static snapshots the board shows on its non-live tabs
// plus the run shape for the header.
type Options struct {
	QueueURLs []string // pending queue as loaded at start
	Completed []string // completed-history lines
	Settings  string   // settings tab body (the snapshot text)
	Jobs      int
}

// Model is the board's bubbletea model.
type Model struct {
	opts Options
	tab  Tab

	rows     []*Row
	static   []string // lines without a live row (already-complete, etc.)
	warnings []string // most recent warnings, capped

	doneCount   int
	failedCount int
	runFinished bool

	width  int
	height int
}

func NewModel(opts Options) Model {
	return Model{opts: opts, width: 100, height: 30}
}

// ---------------------------------------------------------------------------
// Messages (exported so cmd can send them from the engine side)
// ---------------------------------------------------------------------------

type tickMsg struct{}

// FileStartedMsg announces a row for a file whose transfer began.
type FileStartedMsg struct{ Row *Row }

// FileDoneMsg settles a file. Row is nil when the download failed before
// producing a row (e.g. metadata failure); Text then carries the line.
type FileDoneMsg struct {
	Row    *Row
	Text   string
	Failed bool
}

// WarnMsg is a transient note (retry chatter) shown above the footer.
type WarnMsg struct{ Text string }

// RunFinishedMsg marks the queue run as complete (all files settled).
type RunFinishedMsg struct{}

// ---------------------------------------------------------------------------
// Model plumbing
// ---------------------------------------------------------------------------

func (m Model) Init() tea.Cmd { return tickCmd() }

func tickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		return m, tickCmd() // periodic repaint: byte counters are atomics

	case FileStartedMsg:
		m.rows = append(m.rows, msg.Row)
		return m, nil

	case FileDoneMsg:
		if msg.Row == nil {
			if msg.Text != "" {
				m.static = append(m.static, msg.Text)
			}
		} else if msg.Failed {
			msg.Row.Status = RowFailed
			msg.Row.Detail = msg.Text
		} else {
			msg.Row.Status = RowSaved
			msg.Row.Detail = msg.Text
		}
		if msg.Failed {
			m.failedCount++
		} else {
			m.doneCount++
		}
		return m, nil

	case WarnMsg:
		m.warnings = append(m.warnings, msg.Text)
		if len(m.warnings) > 3 {
			m.warnings = m.warnings[len(m.warnings)-3:]
		}
		return m, nil

	case RunFinishedMsg:
		m.runFinished = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.tab = (m.tab + 1) % Tab(len(tabNames))
		case "shift+tab", "left", "h":
			m.tab = (m.tab + Tab(len(tabNames)) - 1) % Tab(len(tabNames))
		case "1", "2", "3", "4":
			m.tab = Tab(msg.String()[0] - '1')
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	// Header
	header := StyleTitle.Render("dlm") + StyleDim.Render("  interactive ui")
	workerWord := "workers"
	if m.opts.Jobs == 1 {
		workerWord = "worker"
	}
	right := StyleDim.Render(fmt.Sprintf("%d %s · %d files", m.opts.Jobs, workerWord, len(m.opts.QueueURLs)))
	b.WriteString(padBetween(header, right, m.width) + "\n")

	// Tab bar
	var tabs []string
	for i, name := range tabNames {
		if Tab(i) == m.tab {
			tabs = append(tabs, StyleTabActive.Render(name))
		} else {
			tabs = append(tabs, StyleTabInactive.Render(name))
		}
	}
	b.WriteString(strings.Join(tabs, StyleDim.Render(" · ")) + "\n")
	b.WriteString(StyleDim.Render(strings.Repeat("─", maxInt(10, m.width))) + "\n")

	switch m.tab {
	case TabDownloads:
		b.WriteString(m.viewDownloads())
	case TabQueue:
		b.WriteString(m.viewList(m.opts.QueueURLs, "queue is empty"))
	case TabCompleted:
		b.WriteString(m.viewList(m.opts.Completed, "no completed downloads yet"))
	case TabSettings:
		b.WriteString(m.opts.Settings)
	}

	// Footer: warnings, then status + key help.
	for _, w := range m.warnings {
		b.WriteString("\n" + StyleWarn.Render(w))
	}
	b.WriteString("\n" + StyleDim.Render("─") + "\n")
	if m.runFinished {
		summary := fmt.Sprintf("run finished — %d saved, %d failed", m.doneCount, m.failedCount)
		if m.failedCount > 0 {
			b.WriteString(StyleErr.Render(summary))
		} else {
			b.WriteString(StyleOK.Render(summary))
		}
	} else {
		b.WriteString(StyleDim.Render(fmt.Sprintf(
			"running %d/%d files · %d saved, %d failed",
			m.runningCount(), len(m.opts.QueueURLs), m.doneCount, m.failedCount,
		)))
	}
	b.WriteString("\n" + StyleDim.Render("q quit · tab/←→ switch · 1-4 jump · pause/resume/retry land in M2"))
	return b.String()
}

func (m Model) runningCount() int {
	n := 0
	for _, r := range m.rows {
		if r.Status == RowRunning {
			n++
		}
	}
	return n
}

func (m Model) viewDownloads() string {
	if len(m.rows) == 0 && len(m.static) == 0 {
		if m.runFinished {
			return StyleDim.Render("nothing downloaded in this run") + "\n"
		}
		return StyleDim.Render("waiting for the first transfer…") + "\n"
	}

	var b strings.Builder
	for _, r := range m.rows {
		switch r.Status {
		case RowRunning:
			b.WriteString(renderRow(r) + "\n")
		case RowSaved:
			b.WriteString(r.Detail + "\n")
		case RowFailed:
			b.WriteString(StyleErr.Render(r.Detail) + "\n")
		}
	}
	for _, line := range m.static {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m Model) viewList(items []string, empty string) string {
	if len(items) == 0 {
		return StyleDim.Render(empty) + "\n"
	}
	var b strings.Builder
	for i, item := range items {
		if i >= m.height-6 && m.height > 0 {
			b.WriteString(StyleDim.Render(fmt.Sprintf("… and %d more", len(items)-i)) + "\n")
			break
		}
		b.WriteString("  " + ui.CleanFileName(item) + "\n")
	}
	return b.String()
}

// padBetween puts left and right on one line filling width.
func padBetween(left, right string, width int) string {
	gap := width - lipglossWidth(left) - lipglossWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
