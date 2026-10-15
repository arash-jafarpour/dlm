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
	// RowQueued is a held file: visible, but waiting for the user to start
	// it (space) or switch to flowing mode (s).
	RowQueued
	// RowStarting is a file the app is working on: dispatched to a worker
	// (or queued behind one) while the transfer is being set up, before
	// any bytes arrive.
	RowStarting
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
	// ControlStart starts one held job; ControlStartAll switches the run
	// into flowing mode (fill worker slots, start later jobs as earlier
	// ones finish) — the same behavior as `dlm download queue`.
	// ControlStopAll is its inverse: active transfers stop (partial .part
	// files remain) and every started-but-unfinished job returns to the
	// held state, so the board looks like it did at launch.
	ControlStart
	ControlStartAll
	ControlStopAll
	// Queue editing: ControlAdd carries a URL, ControlRemove a job id,
	// ControlReorder the new id order (see Scheduler's methods, which the
	// board reaches through Options callbacks).
	ControlAdd
	ControlRemove
	ControlReorder
)

// Options carries the static snapshots the board shows on its non-live tabs,
// the run shape, and the hooks back into the engine and the queue file.
type Options struct {
	QueueURLs []string
	Completed []string
	Settings  string
	Jobs      int

	// OnControl is called when the user issues a playback control for a job.
	// A nil hook renders the board read-only (tests).
	OnControl func(jobID int, action ControlAction)

	// Queue-editing hooks. Each returns what the model needs to keep its
	// rows in sync; nil hooks disable the corresponding key.
	// OnAdd stores the URLs and registers them with the engine, returning
	// the new job ids (same order).
	OnAdd func(urls []string) []int
	// OnDelete removes the job and its queue-file line.
	OnDelete func(jobID int)
	// OnReorder persists the new order (job ids in display order).
	OnReorder func(ids []int)
	// OnOpenEditor suspends the board and opens the queue file in $EDITOR;
	// the returned command must deliver a QueueReloadMsg afterwards.
	OnOpenEditor func() tea.Cmd
	// OnOpenConfig opens the config file in $EDITOR; the returned command
	// must deliver a SettingsMsg with the refreshed snapshot text.
	OnOpenConfig func() tea.Cmd
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
	spin         int  // advances on every tick; drives the starting spinner
	help         bool // help overlay visible

	// Queue-editing input line ('a'): active while the user types URLs.
	inputActive bool
	input       string

	// Aggregate speed sampling for the footer sparkline.
	haveSample bool
	lastTotal  int64
	hist       []float64

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

// JobRef identifies one engine job (id) and its URL; the model renders the
// file name.
type JobRef struct {
	ID  int
	URL string
}

// QueueReloadMsg replaces the board's job list after the queue file was
// edited externally ($EDITOR). cmd reconciles the engine first and sends
// the resulting job set.
type QueueReloadMsg struct{ Jobs []JobRef }

// SettingsMsg refreshes the settings tab body (after the config file was
// edited externally).
type SettingsMsg struct{ Text string }

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
		m.spin++ // animates the "starting" spinner on live rows
		m.sampleSpeed()
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

	case QueueReloadMsg:
		m.applyReload(msg.Jobs)
		return m, nil

	case SettingsMsg:
		m.opts.Settings = msg.Text
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The help overlay swallows everything except closing and quitting.
	if m.help {
		switch msg.String() {
		case "?", "esc":
			m.help = false
		case "q", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	// The add-input line captures everything until enter/esc.
	if m.inputActive {
		return m.handleInputKey(msg)
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = true
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % Tab(len(tabNames))
	case "shift+tab", "left", "h":
		m.tab = (m.tab + Tab(len(tabNames)) - 1) % Tab(len(tabNames))
	case "1", "2", "3":
		m.tab = Tab(msg.String()[0] - '1')
	case "down", "j":
		m.moveSelection(1)
	case "up", "k":
		m.moveSelection(-1)
	case "a":
		m.inputActive = true
		m.input = ""
	case "d":
		m.deleteSelected()
	case "J":
		m.moveSelected(1)
	case "K":
		m.moveSelected(-1)
	case "o":
		// Contextual: on the settings tab this edits the config file,
		// everywhere else the queue file.
		if m.tab == TabSettings && m.opts.OnOpenConfig != nil {
			return m, m.opts.OnOpenConfig()
		}
		if m.opts.OnOpenEditor != nil {
			return m, m.opts.OnOpenEditor()
		}
	case " ":
		m.controlSelected(func(row *Row) ControlAction {
			switch row.Status {
			case RowPaused:
				return ControlResume
			case RowQueued, RowStarting:
				// Held → start it; already starting → back to held.
				if row.Status == RowQueued {
					return ControlStart
				}
				return ControlPause
			case RowFailed, RowCancelled:
				return ControlRetry
			default:
				return ControlPause
			}
		})
	case "s":
		if m.opts.OnControl != nil {
			// Toggle: with anything active, s stops the whole run and
			// returns started files to the held state; otherwise it
			// starts everything.
			if m.activeCount() > 0 {
				m.opts.OnControl(-1, ControlStopAll)
			} else {
				m.opts.OnControl(-1, ControlStartAll)
			}
		}
	case "r":
		m.controlSelected(func(*Row) ControlAction { return ControlRetry })
	case "x":
		m.controlSelected(func(*Row) ControlAction { return ControlCancel })
	}
	return m, nil
}

// handleInputKey drives the 'a' add line: printable runes accumulate
// (pasted blocks may contain several whitespace-separated URLs), enter
// submits, esc cancels, backspace edits.
func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.inputActive = false
		m.input = ""
	case tea.KeyEnter:
		urls := parseURLList(m.input)
		m.inputActive = false
		m.input = ""
		if len(urls) > 0 && m.opts.OnAdd != nil {
			ids := m.opts.OnAdd(urls)
			for i, id := range ids {
				if id < 0 || i >= len(urls) {
					continue
				}
				row := newRow(id, ui.FileName(urls[i]), 0, 0)
				row.Status = RowQueued
				m.rows = append(m.rows, row)
			}
		}
	case tea.KeyBackspace:
		if n := len(m.input); n > 0 {
			m.input = m.input[:n-1]
		}
	case tea.KeySpace:
		m.input += " "
	case tea.KeyRunes:
		m.input += string(msg.Runes)
	}
	return m, nil
}

// parseURLList splits typed/pasted text into URLs: any whitespace (spaces,
// newlines, tabs) separates, and only http(s) entries count.
func parseURLList(text string) []string {
	var urls []string
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			urls = append(urls, field)
		}
	}
	return urls
}

func (m *Model) deleteSelected() {
	if m.opts.OnDelete == nil || len(m.rows) == 0 {
		return
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	row := m.rows[m.selected]
	m.opts.OnDelete(row.ID)
	m.rows = append(m.rows[:m.selected], m.rows[m.selected+1:]...)
	if m.selected >= len(m.rows) && m.selected > 0 {
		m.selected--
	}
	m.ensureSelectionVisible()
}

// moveSelected reorders the selected row within the queue.
func (m *Model) moveSelected(delta int) {
	if m.opts.OnReorder == nil || len(m.rows) < 2 {
		return
	}
	target := m.selected + delta
	if target < 0 || target >= len(m.rows) {
		return
	}
	m.rows[m.selected], m.rows[target] = m.rows[target], m.rows[m.selected]
	m.selected = target
	m.ensureSelectionVisible()

	ids := make([]int, len(m.rows))
	for i, r := range m.rows {
		ids[i] = r.ID
	}
	m.opts.OnReorder(ids)
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
	if m.help {
		return m.viewHelp()
	}

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

	if m.inputActive {
		b.WriteString(StyleStarting.Render("add url(s): ") + m.input + StyleDim.Render("▏"))
		b.WriteString(StyleDim.Render("   enter to add · esc to cancel"))
		return b.String()
	}

	saved, failed := m.counts()
	switch {
	case m.finished():
		summary := fmt.Sprintf("run finished — %d saved, %d failed", saved, failed)
		if failed > 0 {
			b.WriteString(StyleErr.Render(summary))
		} else {
			b.WriteString(StyleOK.Render(summary))
		}
	case m.runningCount() == 0 && m.waitingCount() > 0:
		b.WriteString(StyleDim.Render(fmt.Sprintf(
			"idle — %d waiting · space start one · s start all (%d at a time)",
			m.waitingCount(), m.opts.Jobs,
		)))
	default:
		status := fmt.Sprintf(
			"running %d/%d files · %d saved, %d failed",
			m.runningCount(), len(m.opts.QueueURLs), saved, failed,
		)
		if speed := m.currentSpeed(); speed > 0 {
			status += fmt.Sprintf(" · %s/s %s", humanBytes(int64(speed)), sparkline(m.hist))
		}
		b.WriteString(StyleDim.Render(status))
	}
	sKey := "s start all"
	if m.activeCount() > 0 {
		sKey = "s stop all"
	}
	b.WriteString("\n" + StyleDim.Render(fmt.Sprintf(
		"space start/pause · %s · r retry · x cancel · a add · d delete · J/K move · o edit",
		sKey,
	)))
	b.WriteString("\n" + StyleDim.Render("tab switch · j/k select · 1-3 tabs · ? help · q quit"))
	return b.String()
}

// activeCount counts rows the engine is currently working on (set-up or
// transferring) plus paused ones: anything a stop-all would take back.
func (m Model) activeCount() int {
	n := 0
	for _, r := range m.rows {
		switch r.Status {
		case RowStarting, RowRunning, RowPaused:
			n++
		}
	}
	return n
}

func (m Model) waitingCount() int {
	n := 0
	for _, r := range m.rows {
		if r.Status == RowQueued {
			n++
		}
	}
	return n
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
		case RowRunning, RowQueued, RowStarting, RowPaused:
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
			// Held: the user has not asked for this file yet.
			b.WriteString(marker + StyleDim.Render("· waiting for you  "+r.Label) + "\n")
		case RowStarting:
			// The app is on it: dispatched or waiting for a free slot
			// while the transfer is set up.
			b.WriteString(marker + StyleStarting.Render(spinnerFrame(m.spin)+" starting  "+r.Label) + "\n")
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
			b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below  (full list: dlm ui, or completed.txt)", len(items)-i)) + "\n")
			break
		}
		// Items are pre-formatted by the caller (e.g. completed lines are
		// "timestamp  file name"); only width-clip here.
		b.WriteString("  " + truncateDisplay(item, m.contentWidth()-4) + "\n")
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

// spinnerFrame returns the animated "starting" glyph for a tick count.
func spinnerFrame(tick int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[tick%len(frames)]
}

// applyReload reconciles the row list with the engine's job set after the
// queue file was edited externally: rows for surviving jobs keep their
// state, new jobs are added held, vanished jobs are dropped.
func (m *Model) applyReload(jobs []JobRef) {
	prev := make(map[int]*Row, len(m.rows))
	for _, r := range m.rows {
		prev[r.ID] = r
	}
	rows := make([]*Row, 0, len(jobs))
	for _, j := range jobs {
		if row, ok := prev[j.ID]; ok {
			rows = append(rows, row)
			continue
		}
		row := newRow(j.ID, ui.FileName(j.URL), 0, 0)
		row.Status = RowQueued
		rows = append(rows, row)
	}
	m.rows = rows
	if m.selected >= len(m.rows) {
		m.selected = maxInt(0, len(m.rows)-1)
	}
	m.ensureSelectionVisible()
}

// sampleSpeed records the aggregate transfer speed of one tick into the
// sparkline history.
func (m *Model) sampleSpeed() {
	total := int64(0)
	for _, r := range m.rows {
		switch r.Status {
		case RowRunning, RowStarting:
			total += r.Current()
		}
	}
	if !m.haveSample {
		m.haveSample = true
		m.lastTotal = total
		return
	}
	delta := total - m.lastTotal
	m.lastTotal = total
	if delta < 0 {
		delta = 0 // a finished row dropped out of the sum
	}
	m.hist = append(m.hist, float64(delta)/tickInterval.Seconds())
	if len(m.hist) > sparkWidth {
		m.hist = m.hist[len(m.hist)-sparkWidth:]
	}
}

// currentSpeed is the most recently sampled aggregate speed in bytes/s.
func (m Model) currentSpeed() float64 {
	if len(m.hist) == 0 {
		return 0
	}
	return m.hist[len(m.hist)-1]
}

// viewHelp renders the full-screen key reference ('?'). Plain text, no
// unicode diagrams: the terminal already renders it.
func (m Model) viewHelp() string {
	rows := []string{
		StyleTitle.Render("dlm ui — keys"),
		"",
		StyleDim.Render("downloads"),
		"  space      start the selected file / pause it / back to held",
		"  s          start all (flowing: " + fmt.Sprint(m.opts.Jobs) + " at a time) · press again to stop all",
		"  r          retry the selected file (failed, paused or cancelled)",
		"  x          cancel the selected file",
		"  j/k        select (up/down scrolls the queue list)",
		"",
		StyleDim.Render("queue editing"),
		"  a          add URLs (paste several, whitespace separated; enter adds)",
		"  d          delete the selected file from the queue",
		"  J/K        move the selected file down/up (changes flow order)",
		"  o          edit the queue file in $EDITOR (settings tab: the config)",
		"",
		StyleDim.Render("board"),
		"  1-3, tab   downloads / completed / settings",
		"  ?          this help · q quit (the run stops, parts resume later)",
		"",
		StyleDim.Render("row states"),
		"  · waiting for you   held — nothing happens until you start it",
		"  spinner             starting: dispatched or waiting for a slot",
		"  bar                 transferring (per-file progress)",
		"  paused              space resumes from the exact byte",
	}
	return strings.Join(rows, "\n") + "\n"
}

const (
	// tickInterval matches the repaint cadence (tickCmd) and is the sample
	// window for the footer's aggregate speed sparkline.
	tickInterval = 150 * time.Millisecond
	// sparkWidth caps the sparkline history.
	sparkWidth = 20
)

// sparkRunes maps normalized speeds to sparkline block glyphs.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders normalized values as block glyphs; empty when there is
// nothing to show.
func sparkline(vals []float64) string {
	max := 0.0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return ""
	}
	var b strings.Builder
	for _, v := range vals {
		idx := int(v / max * float64(len(sparkRunes)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkRunes) {
			idx = len(sparkRunes) - 1
		}
		b.WriteRune(sparkRunes[idx])
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
