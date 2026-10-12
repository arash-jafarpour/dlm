// Package tui implements the interactive `dlm ui` board: a live view over a
// queue run with per-file progress rows and playback controls, built on
// bubbletea. It only ever runs on a real terminal; the plain snapshot path
// lives in cmd.
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
	TabCompleted
	TabSettings
)

var tabNames = []string{"downloads", "completed", "settings"}

// RowStatus is a row's lifecycle state.
type RowStatus int

const (
	RowRunning RowStatus = iota
	RowSaved
	RowFailed
	RowPaused
	RowCancelled
	// RowQueued is a file waiting for a worker slot. Every queued file is
	// shown from the start; queue_jobs decides how many turn RowRunning.
	RowQueued
)

// Row is one file's slot in the downloads panel. Byte counts are updated
// from worker goroutines through atomics; every other field is written only
// from the model's Update (single goroutine).
type Row struct {
	ID     int
	Label  string
	Total  int64
	Start  time.Time
	Status RowStatus
	Detail string

	current atomic.Int64
}

func newRow(id int, label string, total, offset int64) *Row {
	r := &Row{
		ID:    id,
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

// ControlAction is a playback control the board can issue for a job.
type ControlAction int

const (
	ControlPause ControlAction = iota
	ControlResume
	ControlRetry
	ControlCancel
)

// Options carries the static snapshots the board shows on its non-live tabs,
// the run shape, and the control hook back into the engine.
type Options struct {
	QueueURLs []string
	Completed []string
	Settings  string
	Jobs      int

	// OnControl is called when the user issues a playback control for a job.
	// A nil hook renders the board read-only (tests).
	OnControl func(jobID int, action ControlAction)
}

// defaultVisibleRows is the fallback window when the terminal size is not
// known yet (before the first WindowSizeMsg).
const defaultVisibleRows = 10

// Model is the board's bubbletea model.
type Model struct {
	opts Options
	tab  Tab

	rows     []*Row
	static   []string
	warnings []string

	selected     int
	scrollOffset int
	runFinished  bool

	width  int
	height int
}

// windowSize is how many list rows fit on screen: terminal height minus the
// chrome (header, tabs, separator, warnings, footer). Falls back to a fixed
// default until the terminal reports its size.
func (m Model) windowSize() int {
	if m.height <= 0 {
		return defaultVisibleRows
	}
	n := m.height - 7 - 2*len(m.warnings)
	if n < 3 {
		n = 3
	}
	return n
}

// rowLayout derives label and bar widths from the terminal width so rows
// fit the screen: the row format after the bar needs ~41 cells (percent,
// byte counters, speed) plus up to 9 for an ETA string, and the selection
// marker adds 2 more.
func (m Model) rowLayout() (labelW, barW int) {
	w := m.width
	if w <= 0 {
		w = 100
	}
	labelW = clampInt(w/3, 16, 34)
	barW = clampInt(w-labelW-52, 8, 40)
	return labelW, barW
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func NewModel(opts Options) Model {
	m := Model{opts: opts, width: 100, height: 30}
	// Every queued file is visible from the start, in queue order: the
	// board is the queue. queue_jobs decides which rows go live.
	for i, u := range opts.QueueURLs {
		m.rows = append(m.rows, newRow(i, ui.FileName(u), 0, 0))
		m.rows[i].Status = RowQueued
	}
	return m
}

// ---------------------------------------------------------------------------
// Messages (exported so cmd can send them from the engine side)
// ---------------------------------------------------------------------------

type tickMsg struct{}

// FileStartedMsg announces a row for a file whose transfer began.
type FileStartedMsg struct {
	ID  int
	Row *Row
}

// FileDoneMsg settles a file that produced a row (saved / already-complete).
type FileDoneMsg struct {
	Row  *Row
	Text string
}

// JobStateMsg reports an engine-side state change for a job that did not
// finish with a saved file: paused, cancelled, or failed (the latter also
// covers failures before any row existed, hence Label).
type JobStateMsg struct {
	ID     int
	Label  string
	State  RowStatus
	Detail string
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
		// A live transfer (re)opens the run: retries after a finished
		// summary must flip the footer back to running.
		m.runFinished = false
		if row := m.rowByID(msg.ID); row != nil {
			// The queued slot adopts the live row so byte updates land in
			// the right place, position preserved.
			m.replaceRow(row, msg.Row)
		} else {
			m.rows = append(m.rows, msg.Row)
		}
		return m, nil

	case FileDoneMsg:
		if msg.Row != nil {
			msg.Row.Status = RowSaved
			msg.Row.Detail = msg.Text
		} else if msg.Text != "" {
			m.static = append(m.static, msg.Text)
		}
		return m, nil

	case JobStateMsg:
		row := m.rowByID(msg.ID)
		if row == nil {
			row = newRow(msg.ID, msg.Label, 0, 0) // no transfer ever began
			m.rows = append(m.rows, row)
		}
		row.Status = msg.State
		row.Detail = msg.Detail
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
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % Tab(len(tabNames))
	case "shift+tab", "left", "h":
		m.tab = (m.tab + Tab(len(tabNames)) - 1) % Tab(len(tabNames))
	case "1", "2", "3", "4":
		m.tab = Tab(msg.String()[0] - '1')
	case "down", "j":
		m.moveSelection(1)
	case "up", "k":
		m.moveSelection(-1)
	case " ":
		m.controlSelected(func(row *Row) ControlAction {
			if row.Status == RowPaused {
				return ControlResume
			}
			return ControlPause
		})
	case "r":
		m.controlSelected(func(*Row) ControlAction { return ControlRetry })
	case "x":
		m.controlSelected(func(*Row) ControlAction { return ControlCancel })
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.rows) == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta + len(m.rows)) % len(m.rows)
	m.ensureSelectionVisible()
}

// ensureSelectionVisible scrolls the window so the selected row is inside
// the visible slice.
func (m *Model) ensureSelectionVisible() {
	window := m.windowSize()
	if m.selected < m.scrollOffset {
		m.scrollOffset = m.selected
	}
	if m.selected >= m.scrollOffset+window {
		m.scrollOffset = m.selected - window + 1
	}

	maxOffset := len(m.rows) - window
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m *Model) controlSelected(action func(row *Row) ControlAction) {
	if m.opts.OnControl == nil || len(m.rows) == 0 {
		return
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	row := m.rows[m.selected]
	m.opts.OnControl(row.ID, action(row))
}

func (m Model) rowByID(id int) *Row {
	for _, r := range m.rows {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (m *Model) replaceRow(old, fresh *Row) {
	for i, r := range m.rows {
		if r == old {
			fresh.Status = RowRunning
			m.rows[i] = fresh
			return
		}
	}
	m.rows = append(m.rows, fresh)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m Model) View() string {
	var b strings.Builder

	header := StyleTitle.Render("dlm") + StyleDim.Render("  interactive ui")
	workerWord := "workers"
	if m.opts.Jobs == 1 {
		workerWord = "worker"
	}
	right := StyleDim.Render(fmt.Sprintf("%d %s · %d files", m.opts.Jobs, workerWord, len(m.opts.QueueURLs)))
	b.WriteString(padBetween(header, right, m.width) + "\n")

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
	case TabCompleted:
		b.WriteString(m.viewList(m.opts.Completed, "no completed downloads yet"))
	case TabSettings:
		b.WriteString(m.clipBlock(m.opts.Settings))
	}

	for _, w := range m.warnings {
		b.WriteString("\n" + StyleWarn.Render(w))
	}
	b.WriteString("\n" + StyleDim.Render("─") + "\n")

	saved, failed := m.counts()
	if m.finished() {
		summary := fmt.Sprintf("run finished — %d saved, %d failed", saved, failed)
		if failed > 0 {
			b.WriteString(StyleErr.Render(summary))
		} else {
			b.WriteString(StyleOK.Render(summary))
		}
	} else {
		b.WriteString(StyleDim.Render(fmt.Sprintf(
			"running %d/%d files · %d saved, %d failed",
			m.runningCount(), len(m.opts.QueueURLs), saved, failed,
		)))
	}
	b.WriteString("\n" + StyleDim.Render("space pause/resume · r retry · x cancel · tab switch · q quit"))
	return b.String()
}

// finished reports whether every known job reached a terminal state. Paused
// rows keep the run open: they are resumable.
func (m Model) finished() bool {
	if m.runFinished {
		return true
	}
	if len(m.rows) == 0 {
		return false
	}
	for _, r := range m.rows {
		switch r.Status {
		case RowRunning, RowQueued, RowPaused:
			return false
		}
	}
	return true
}

func (m Model) counts() (saved, failed int) {
	for _, r := range m.rows {
		switch r.Status {
		case RowSaved:
			saved++
		case RowFailed:
			failed++
		}
	}
	return saved, failed
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
		return StyleDim.Render("waiting for the first transfer…") + "\n"
	}

	var b strings.Builder

	window := m.windowSize()
	labelW, barW := m.rowLayout()

	end := m.scrollOffset + window
	if end > len(m.rows) {
		end = len(m.rows)
	}
	if m.scrollOffset > 0 {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▲ %d more above", m.scrollOffset)) + "\n")
	}
	for i := m.scrollOffset; i < end; i++ {
		r := m.rows[i]
		marker := "  "
		if i == m.selected {
			marker = StyleTabActive.Render("▸ ")
		}
		switch r.Status {
		case RowRunning:
			b.WriteString(marker + renderRow(r, labelW, barW, m.width) + "\n")
		case RowQueued:
			b.WriteString(marker + StyleDim.Render("· waiting  "+r.Label) + "\n")
		case RowSaved:
			b.WriteString(marker + r.Detail + "\n")
		case RowFailed:
			b.WriteString(marker + StyleErr.Render("✗ failed  "+r.Label) +
				StyleDim.Render("  "+r.Detail) + "\n")
		case RowPaused:
			b.WriteString(marker + StyleWarn.Render("⏸ paused (space resumes)  ") + StyleDim.Render(r.Label) + "\n")
		case RowCancelled:
			b.WriteString(marker + StyleDim.Render("⊘ cancelled  "+r.Label) + "\n")
		}
	}
	if end < len(m.rows) {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below", len(m.rows)-end)) + "\n")
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
	window := m.windowSize()
	var b strings.Builder
	for i, item := range items {
		if i >= window {
			b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below  (full list: dlm completed)", len(items)-i)) + "\n")
			break
		}
		b.WriteString("  " + truncateDisplay(ui.CleanFileName(item), m.contentWidth()-4) + "\n")
	}
	return b.String()
}

// contentWidth is the usable line width for list/body content.
func (m Model) contentWidth() int {
	if m.width <= 0 {
		return 100
	}
	return m.width
}

// clipBlock limits a multi-line body (settings) to the visible window.
func (m Model) clipBlock(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	window := m.windowSize()
	if len(lines) <= window {
		return strings.Join(lines, "\n") + "\n"
	}
	shown := lines[:window-1]
	shown = append(shown, StyleDim.Render(fmt.Sprintf("… %d more lines", len(lines)-window+1)))
	return strings.Join(shown, "\n") + "\n"
}

// truncateDisplay shortens a plain (unstyled) string to max visible cells.
func truncateDisplay(s string, max int) string {
	if max < 4 || len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
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
