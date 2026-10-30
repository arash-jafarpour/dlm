// Package tui implements the interactive `dlm` board: a live view over a
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
	TabSummary
)

var tabNames = []string{"downloads", "completed", "settings", "summary"}

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

// SettingKind picks the settings-page editing affordance for a value.
type SettingKind int

const (
	SettingText SettingKind = iota
	SettingInt
	SettingBool
)

// Setting is one editable config-file value on the settings page.
type Setting struct {
	Key   string
	Value string
	Kind  SettingKind
	Help  string
}

// HistoryEntry is one completed-download history line: Text is what the
// board renders, Ref is the opaque raw line used to delete exactly that
// entry.
type HistoryEntry struct {
	Text string
	Ref  string
}

// Summary is the board's dashboard snapshot: the completed-history totals,
// the output directory's state, the queue depth, and the environment. Cmd
// computes it (filesystem reads live there) and refreshes it on demand
// through Options.OnSummary. The completed tab reads the history/output
// subset; the summary tab reads all of it.
type Summary struct {
	Count    int    // completed downloads (history lines)
	Bytes    int64  // total bytes across the history
	Dir      string // output directory, empty when unknown
	Files    int    // files currently in the output directory
	DirBytes int64  // total bytes in the output directory

	Pending  int    // URLs waiting in the queue file
	Settings string // one-line config recap (chunks, retries, workers)
	Platform string // GOOS/GOARCH
}

// Options carries the static snapshots the board shows on its non-live tabs,
// the run shape, and the hooks back into the engine and the queue file.
type Options struct {
	QueueURLs []string
	Completed []HistoryEntry
	Settings  []Setting
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
	// must deliver a SettingsMsg with the refreshed rows.
	OnOpenConfig func() tea.Cmd

	// Settings-page hooks (nil disables the corresponding key row action).
	// OnSettingSet validates and applies one key=value (persisting it).
	OnSettingSet func(key, value string) error
	// OnSettingReset restores one key to its default and returns the new
	// display value.
	OnSettingReset func(key string) (string, error)
	// OnSettingsResetAll restores every default and returns the refreshed
	// rows.
	OnSettingsResetAll func() ([]Setting, error)

	// Completed-page hooks.
	OnDeleteCompleted func(ref string) error
	OnClearCompleted  func() error

	// Summary is the board's dashboard roll-up; OnSummary returns a command
	// that delivers a fresh SummaryMsg (see cmd for how it is computed).
	// The board refreshes it when the completed or summary tab is opened,
	// when a run finishes, and on demand (r on the summary tab).
	Summary   Summary
	OnSummary func() tea.Cmd

	// Version is the running dlm version, shown dim on the footer.
	Version string

	// OnCheckUpdate runs the background release check when the board starts;
	// its command delivers UpdateAvailableMsg when a newer version exists.
	// Nil disables the check (tests, non-release builds).
	OnCheckUpdate func() tea.Cmd
	// OnInstallUpdate downloads and installs the offered release; its command
	// delivers UpdateInstalledMsg or UpdateFailedMsg.
	OnInstallUpdate func() tea.Cmd
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

	// Per-page cursors: each tab owns its selection so keys never act on
	// another page's rows.
	selCompleted    int
	scrollCompleted int
	selSettings     int
	scrollSettings  int

	// confirm is the modal for destructive actions; while it is set the
	// board ignores every other key (including q).
	confirm *confirmPrompt

	// Update flow: the release the background check offered, whether its
	// install is running, and the version installed this session (which only
	// takes effect after a restart).
	updateAvailable string
	updating        bool
	updatedTo       string

	// Queue-editing input line ('a' on downloads, enter on a setting):
	// active while the user types.
	inputActive bool
	input       string
	inputKey    string // setting key being edited; "" means "add URLs"

	// Per-page list filters: '/' narrows the downloads rows or the completed
	// history. Each page owns its query, so switching tabs never applies one
	// page's filter to another.
	filters [TabSummary + 1]filterState

	// Aggregate speed sampling for the footer sparkline.
	haveSample bool
	lastTotal  int64
	hist       []float64

	width  int
	height int
}

// confirmPrompt is a pending yes/no question. OnYes receives the current
// model so the confirmed action mutates the same value Update returns
// (closures over the model would edit a stale copy). Cmd, when set, starts
// the work the answer authorizes (e.g. downloading an update).
type confirmPrompt struct {
	Prompt string
	OnYes  func(m *Model)
	Cmd    func() tea.Cmd
}

// filterState is one page's list filter: the applied, case-insensitive
// substring query, and whether the '/' prompt that edits it is open. The
// query narrows the list live while the prompt is open; enter keeps it, esc
// clears it.
type filterState struct {
	query string
	open  bool
}

// windowSize is how many list rows fit on screen: terminal height minus the
// chrome (header, tabs, separators, warnings, status/hints/footer), the
// tab's fixed body lines, and room for the ▲/▼ scroll markers. It must match
// what View actually emits: bubbletea drops lines off the top when a frame
// is taller than the terminal, which would hide the header. View pads any
// leftover rows, so a short list fills the window instead of floating the
// footer up.
func (m Model) windowSize() int {
	if m.height <= 0 {
		return defaultVisibleRows
	}
	// Chrome lines: header, tab strip, top separator, footer separator,
	// status, hints, footer — plus one per transient warning.
	n := m.height - 7 - len(m.warnings)
	// The update notice owns a line of its own above the footer.
	if m.updateLineShown() {
		n--
	}
	// Key hints wrap to as many lines as the width needs; the chrome
	// constant above counts one.
	if !m.modalOpen() {
		if extra := m.hintLines() - 1; extra > 0 {
			n -= extra
		}
	}
	// Body lines a tab spends on fixed content rather than list rows.
	n -= m.tabExtraLines()
	// Room for the "more above" / "more below" markers so a scrolled list
	// still fits the frame. The summary tab is fixed-height (no window).
	if m.tab != TabSummary {
		n -= 2
	}
	if n < 1 {
		n = 1
	}
	return n
}

// modalOpen reports whether a prompt (confirm, input, filter) currently
// replaces the status/keys area at the bottom of the board.
func (m Model) modalOpen() bool {
	return m.confirm != nil || m.inputActive || m.filterState(m.tab).open
}

// hintLines is how many lines the page's key hints occupy at the current
// width. Hints wrap at " · " boundaries instead of being clipped, so no key
// disappears on a narrow terminal.
func (m Model) hintLines() int {
	return len(wrapHint(m.pageHints(), m.contentWidth()))
}

// wrapHint packs hint segments ("a · b · c") into lines no wider than width,
// breaking only between segments so a key label never splits across lines.
// When the result is two very uneven lines, it rebalances them at the segment
// boundary nearest the middle.
func wrapHint(hint string, width int) []string {
	if width <= 0 {
		return []string{hint}
	}
	segs := strings.Split(hint, " · ")
	lines := packHintSegments(segs, width)
	if len(lines) == 2 && lipglossWidth(lines[0]) > width*3/4 {
		if balanced := balanceHintSegments(segs, width); balanced != nil {
			return balanced
		}
	}
	return lines
}

// packHintSegments greedily fills lines with whole segments.
func packHintSegments(segs []string, width int) []string {
	var lines []string
	cur := ""
	for _, seg := range segs {
		cand := seg
		if cur != "" {
			cand = cur + " · " + seg
		}
		if cur == "" || lipglossWidth(cand) <= width {
			cur = cand
			continue
		}
		lines = append(lines, cur)
		cur = seg
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{strings.Join(segs, " · ")}
	}
	return lines
}

// balanceHintSegments splits the segments in two as evenly as the width
// allows, avoiding the "long line plus one stray key" look of a greedy wrap.
func balanceHintSegments(segs []string, width int) []string {
	best, bestWidest := -1, 0
	for k := 1; k < len(segs); k++ {
		left := strings.Join(segs[:k], " · ")
		right := strings.Join(segs[k:], " · ")
		wl, wr := lipglossWidth(left), lipglossWidth(right)
		if wl > width || wr > width {
			continue
		}
		if widest := maxInt(wl, wr); best < 0 || widest < bestWidest {
			best, bestWidest = k, widest
		}
	}
	if best < 0 {
		return nil
	}
	return []string{
		strings.Join(segs[:best], " · "),
		strings.Join(segs[best:], " · "),
	}
}

// tabExtraLines counts the body lines a tab spends on fixed content rather
// than list rows: the completed roll-up and the settings notes.
func (m Model) tabExtraLines() int {
	switch m.tab {
	case TabCompleted:
		return m.completedSummaryLines()
	case TabSettings:
		return 2 // the two "note:" lines under the list
	}
	return 0
}

// completedSummaryLines counts the body lines the completed tab spends on
// its roll-up (header and output footer) rather than on list rows.
func (m Model) completedSummaryLines() int {
	n := 0
	if m.opts.Summary.Count > 0 {
		n++
	}
	if m.opts.Summary.Dir != "" {
		n++
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

// SettingsMsg refreshes the settings rows (after the config file was
// edited externally or reset to defaults).
type SettingsMsg struct{ Settings []Setting }

// SummaryMsg refreshes the board's dashboard totals (completed history,
// output dir, queue depth).
type SummaryMsg struct{ Summary Summary }

// UpdateAvailableMsg offers a newer release after the background check. An
// empty Version means the running build is current.
type UpdateAvailableMsg struct{ Version string }

// UpdateInstalledMsg reports that the newer release replaced the binary; it
// takes effect on the next start.
type UpdateInstalledMsg struct{ Version string }

// UpdateFailedMsg reports an install failure; the running binary is intact.
type UpdateFailedMsg struct{ Err string }

// ---------------------------------------------------------------------------
// Model plumbing
// ---------------------------------------------------------------------------

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), m.checkUpdate())
}

func tickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// checkUpdate starts the background release check, if cmd wired one.
func (m Model) checkUpdate() tea.Cmd {
	if m.opts.OnCheckUpdate == nil {
		return nil
	}
	return m.opts.OnCheckUpdate()
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
		// A finished run changes the totals the completed tab shows.
		return m, m.refreshSummary()

	case QueueReloadMsg:
		m.applyReload(msg.Jobs)
		return m, nil

	case SettingsMsg:
		m.opts.Settings = msg.Settings
		if m.selSettings >= len(m.opts.Settings) {
			m.selSettings = maxInt(0, len(m.opts.Settings)-1)
		}
		return m, nil

	case SummaryMsg:
		m.opts.Summary = msg.Summary
		return m, nil

	case UpdateAvailableMsg:
		// A late answer must not clobber an install already under way or one
		// that already succeeded this session.
		if msg.Version != "" && !m.updating && m.updatedTo == "" {
			m.updateAvailable = msg.Version
		}
		return m, nil

	case UpdateInstalledMsg:
		m.updating = false
		m.updateAvailable = ""
		m.updatedTo = msg.Version
		return m, nil

	case UpdateFailedMsg:
		m.updating = false
		m.warn("update failed: " + msg.Err)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A pending confirmation swallows everything: y/enter accepts, n/esc
	// cancels, and even q is ignored so answered questions cannot be
	// escaped by habit.
	if m.confirm != nil {
		var cmd tea.Cmd
		switch msg.String() {
		case "y", "enter":
			onYes := m.confirm.OnYes
			after := m.confirm.Cmd
			m.confirm = nil
			if onYes != nil {
				onYes(&m)
			}
			if after != nil {
				cmd = after()
			}
		case "n", "esc":
			m.confirm = nil
		}
		// A confirmed delete/clear changes the completed totals.
		return m, tea.Batch(cmd, m.refreshSummary())
	}

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

	// The filter prompt ('/' on downloads or completed) captures everything
	// until enter/esc, so typing a query never triggers a board key.
	if f := m.filterState(m.tab); f.open {
		return m.handleFilterKey(msg)
	}

	// An open input line captures everything until enter/esc.
	if m.inputActive {
		return m.handleInputKey(msg)
	}

	// Global keys work on every page.
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		// With a filter applied, esc clears it first; the next esc quits.
		if m.clearFilter() {
			return m, nil
		}
		return m, tea.Quit
	case "/":
		m.openFilter()
		return m, nil
	case "?":
		m.help = true
		return m, nil
	case "u":
		return m, m.confirmUpdate()
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % Tab(len(tabNames))
		return m, m.refreshSummary()
	case "shift+tab", "left", "h":
		m.tab = (m.tab + Tab(len(tabNames)) - 1) % Tab(len(tabNames))
		return m, m.refreshSummary()
	case "1", "2", "3", "4":
		m.tab = Tab(msg.String()[0] - '1')
		return m, m.refreshSummary()
	}

	// Page-scoped keys: a control that belongs to the downloads page must
	// never act on settings or history rows.
	switch m.tab {
	case TabDownloads:
		return m, m.handleDownloadsKey(msg)
	case TabCompleted:
		return m, m.handleCompletedKey(msg)
	case TabSettings:
		return m, m.handleSettingsKey(msg)
	case TabSummary:
		return m, m.handleSummaryKey(msg)
	}
	return m, nil
}

// confirmUpdate asks before replacing the running binary; accepting starts
// the install in the background and the footer reports its progress. It is a
// no-op when no update was offered, one is running, or it already installed.
func (m *Model) confirmUpdate() tea.Cmd {
	if m.updateAvailable == "" || m.updating || m.updatedTo != "" || m.opts.OnInstallUpdate == nil {
		return nil
	}
	version := m.updateAvailable
	m.confirm = &confirmPrompt{
		Prompt: fmt.Sprintf("Update dlm to v%s?  The running binary will be replaced.", version),
		OnYes:  func(cur *Model) { cur.updating = true },
		Cmd:    m.opts.OnInstallUpdate,
	}
	return nil
}

// handleDownloadsKey is the downloads page's own key map.
func (m *Model) handleDownloadsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "down", "j":
		m.moveSelection(1)
	case "up", "k":
		m.moveSelection(-1)
	case "a":
		m.inputActive = true
		m.input = ""
		m.inputKey = ""
	case "d":
		m.confirmDeleteSelected()
	case "J":
		m.moveSelected(1)
	case "K":
		m.moveSelected(-1)
	case "g", "ctrl+up":
		m.moveSelectedToEdge(true)
	case "G", "ctrl+down":
		m.moveSelectedToEdge(false)
	case "R":
		m.retryAllFailed()
	case "o":
		if m.opts.OnOpenEditor != nil {
			return m.opts.OnOpenEditor()
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
	return nil
}

// handleCompletedKey is the completed page's own key map: scrolling plus
// the two destructive actions, both behind a confirmation.
func (m *Model) handleCompletedKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "down", "j":
		m.moveHistorySelection(1)
	case "up", "k":
		m.moveHistorySelection(-1)
	case "d":
		if m.opts.OnDeleteCompleted == nil ||
			visiblePos(m.visibleHistory(), m.selCompleted) < 0 {
			return nil
		}
		idx := m.selCompleted
		entry := m.opts.Completed[idx]
		m.confirm = &confirmPrompt{
			Prompt: fmt.Sprintf("Delete this history entry?  %s", truncateDisplay(entry.Text, 48)),
			OnYes: func(cur *Model) {
				if err := cur.opts.OnDeleteCompleted(entry.Ref); err != nil {
					cur.warn(err.Error())
					return
				}
				cur.opts.Completed = append(cur.opts.Completed[:idx], cur.opts.Completed[idx+1:]...)
				if cur.selCompleted >= len(cur.opts.Completed) && cur.selCompleted > 0 {
					cur.selCompleted--
				}
				cur.clampHistorySelection()
			},
		}
	case "c":
		if m.opts.OnClearCompleted == nil || len(m.opts.Completed) == 0 {
			return nil
		}
		m.confirm = &confirmPrompt{
			Prompt: fmt.Sprintf("Clear the entire completed list (%d entries)?", len(m.opts.Completed)),
			OnYes: func(cur *Model) {
				if err := cur.opts.OnClearCompleted(); err != nil {
					cur.warn(err.Error())
					return
				}
				cur.opts.Completed = nil
				cur.selCompleted = 0
				cur.scrollCompleted = 0
			},
		}
	}
	return nil
}

// handleSettingsKey is the settings page's own key map: navigate, edit,
// toggle, reset (confirmed).
func (m *Model) handleSettingsKey(msg tea.KeyMsg) tea.Cmd {
	if len(m.opts.Settings) == 0 {
		return nil
	}
	sel := &m.opts.Settings[m.selSettings]
	switch msg.String() {
	case "down", "j":
		m.moveSettingsSelection(1)
	case "up", "k":
		m.moveSettingsSelection(-1)
	case "enter", "e":
		if sel.Kind == SettingBool {
			m.toggleSelectedBool()
			return nil
		}
		m.inputActive = true
		m.input = sel.Value
		m.inputKey = sel.Key
	case " ":
		if sel.Kind == SettingBool {
			m.toggleSelectedBool()
		}
	case "o":
		if m.opts.OnOpenConfig != nil {
			return m.opts.OnOpenConfig()
		}
	case "r":
		key := sel.Key
		d := m.defaultFor(key)
		if m.opts.OnSettingReset == nil || d == "" || d == sel.Value {
			return nil
		}
		m.confirm = &confirmPrompt{
			Prompt: fmt.Sprintf("Reset %s to its default (%s)?", key, d),
			OnYes: func(cur *Model) {
				value, err := cur.opts.OnSettingReset(key)
				if err != nil {
					cur.warn(err.Error())
					return
				}
				cur.setSettingValue(key, value)
			},
		}
	case "R":
		m.confirm = &confirmPrompt{
			Prompt: "Reset all settings to their defaults?",
			OnYes: func(cur *Model) {
				if cur.opts.OnSettingsResetAll == nil {
					return
				}
				rows, err := cur.opts.OnSettingsResetAll()
				if err != nil {
					cur.warn(err.Error())
					return
				}
				cur.opts.Settings = rows
				cur.clampSettingsSelection()
			},
		}
	}
	return nil
}

func (m *Model) toggleSelectedBool() {
	sel := m.opts.Settings[m.selSettings]
	next := "true"
	if sel.Value == "true" {
		next = "false"
	}
	key := sel.Key
	if err := m.opts.OnSettingSet(key, next); err != nil {
		m.warn(err.Error())
		return
	}
	m.setSettingValue(key, next)
}

// defaultFor is the settings snapshot's default hint for a key, empty when
// unknown (used only to phrase the reset question).
func (m *Model) defaultFor(key string) string {
	for _, s := range m.opts.Settings {
		if s.Key == key && s.Kind == SettingBool {
			return "false"
		}
	}
	if def := defaultHints[key]; def != "" {
		return def
	}
	return ""
}

// defaultHints mirrors cmd's setting defaults for dialog phrasing only.
var defaultHints = map[string]string{
	"num_chunks": "8",
	"queue_jobs": "1",
}

func (m *Model) setSettingValue(key, value string) {
	for i := range m.opts.Settings {
		if m.opts.Settings[i].Key == key {
			m.opts.Settings[i].Value = value
			return
		}
	}
}

func (m *Model) warn(text string) {
	m.warnings = append(m.warnings, text)
	if len(m.warnings) > 3 {
		m.warnings = m.warnings[len(m.warnings)-3:]
	}
}

// refreshSummary asks cmd for fresh dashboard totals, but only while a tab
// that shows them (completed or summary) is open.
func (m Model) refreshSummary() tea.Cmd {
	if (m.tab == TabCompleted || m.tab == TabSummary) && m.opts.OnSummary != nil {
		return m.opts.OnSummary()
	}
	return nil
}

// handleSummaryKey is the summary panel's key map: it is read-only, so the
// only action is a manual refresh.
func (m *Model) handleSummaryKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "r" {
		return m.refreshSummary()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

const (
	// boardBodyStart is the first screen line of the page body: the frame
	// renders the header, then the tab strip, then a rule.
	boardBodyStart = 3
	// mouseWheelRows is how far one wheel notch moves the cursor: enough to
	// feel like scrolling, small enough not to overshoot a row.
	mouseWheelRows = 3
)

// handleMouse maps clicks and wheel ticks onto the board: the tab strip
// switches pages, rows take the cursor, and the wheel moves the current
// page's cursor. Prompts and the help overlay swallow mouse input, like they
// do keys.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.help || m.modalOpen() {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := mouseWheelRows
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -delta
		}
		m.scrollCurrentTab(delta)
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// The tab strip is the frame's second line.
	if msg.Y == 1 {
		tab, ok := tabAt(msg.X)
		if !ok || tab == m.tab {
			return m, nil
		}
		m.tab = tab
		return m, m.refreshSummary()
	}

	line := msg.Y - boardBodyStart
	if line < 0 {
		return m, nil
	}
	idx, ok := m.rowAtBodyLine(line)
	if !ok {
		return m, nil
	}
	switch m.tab {
	case TabDownloads:
		m.selected = idx
		m.ensureSelectionVisible()
	case TabCompleted:
		m.selCompleted = idx
		m.ensureHistoryVisible()
	case TabSettings:
		m.selSettings = idx
		m.ensureSettingsVisible()
	}
	return m, nil
}

// tabAt maps a column of the tab strip to the tab whose name covers it.
// Names are ASCII and the separators are " · ".
func tabAt(x int) (Tab, bool) {
	off := 0
	for i, name := range tabNames {
		if x >= off && x < off+len(name) {
			return Tab(i), true
		}
		off += len(name) + len(" · ")
	}
	return 0, false
}

// rowAtBodyLine maps a line of the page body (0 = first line under the rule)
// to the item rendered there. It mirrors the marker, roll-up and note lines
// the views emit, so a click on an item's line selects that item. ok is
// false for markers, headers, notes, blank padding and non-list tabs.
func (m Model) rowAtBodyLine(line int) (int, bool) {
	switch m.tab {
	case TabDownloads:
		vis := m.visibleRows()
		if len(vis) == 0 {
			return 0, false // the no-match panel
		}
		if m.scrollOffset > 0 {
			line-- // "▲ N more above"
			if line < 0 {
				return 0, false
			}
		}
		pos := m.scrollOffset + line
		if line >= m.windowSize() || pos < 0 || pos >= len(vis) {
			return 0, false // static lines or blank padding
		}
		return vis[pos], true

	case TabCompleted:
		vis := m.visibleHistory()
		if len(vis) == 0 {
			return 0, false
		}
		if m.opts.Summary.Count > 0 {
			line-- // "<n> downloads · <bytes> saved"
		}
		if m.scrollCompleted > 0 {
			line-- // "▲ N more above"
		}
		pos := m.scrollCompleted + line
		if line < 0 || line >= m.windowSize() || pos >= len(vis) {
			return 0, false
		}
		return vis[pos], true

	case TabSettings:
		if len(m.opts.Settings) == 0 {
			return 0, false
		}
		if m.scrollSettings > 0 {
			line--
		}
		pos := m.scrollSettings + line
		if line < 0 || line >= m.windowSize() || pos >= len(m.opts.Settings) {
			return 0, false
		}
		return pos, true
	}
	return 0, false
}

// scrollCurrentTab moves the current page's cursor by delta rows, stopping
// at the ends instead of wrapping (a wheel at the bottom should stay put).
func (m *Model) scrollCurrentTab(delta int) {
	switch m.tab {
	case TabDownloads:
		vis := m.visibleRows()
		if pos := visiblePos(vis, m.selected); pos >= 0 {
			m.selected = vis[clampInt(pos+delta, 0, len(vis)-1)]
			m.ensureSelectionVisible()
		}
	case TabCompleted:
		vis := m.visibleHistory()
		if pos := visiblePos(vis, m.selCompleted); pos >= 0 {
			m.selCompleted = vis[clampInt(pos+delta, 0, len(vis)-1)]
			m.ensureHistoryVisible()
		}
	case TabSettings:
		if len(m.opts.Settings) == 0 {
			return
		}
		m.selSettings = clampInt(m.selSettings+delta, 0, len(m.opts.Settings)-1)
		m.ensureSettingsVisible()
	}
}

func (m *Model) clampSettingsSelection() {
	if m.selSettings >= len(m.opts.Settings) {
		m.selSettings = maxInt(0, len(m.opts.Settings)-1)
	}
	m.ensureSettingsVisible()
}

func (m *Model) moveSettingsSelection(delta int) {
	if len(m.opts.Settings) == 0 {
		return
	}
	m.selSettings = (m.selSettings + delta + len(m.opts.Settings)) % len(m.opts.Settings)
	m.ensureSettingsVisible()
}

func (m *Model) ensureSettingsVisible() {
	window := m.windowSize()
	if m.selSettings < m.scrollSettings {
		m.scrollSettings = m.selSettings
	}
	if m.selSettings >= m.scrollSettings+window {
		m.scrollSettings = m.selSettings - window + 1
	}
	m.scrollSettings = clampInt(m.scrollSettings, 0, maxInt(0, len(m.opts.Settings)-window))
}

// clampHistorySelection keeps the history cursor on a visible entry after
// the query or the history changed.
func (m *Model) clampHistorySelection() {
	vis := m.visibleHistory()
	if len(vis) == 0 {
		m.selCompleted, m.scrollCompleted = 0, 0
		return
	}
	if visiblePos(vis, m.selCompleted) >= 0 {
		m.ensureHistoryVisible()
		return
	}
	for _, i := range vis {
		if i > m.selCompleted {
			m.selCompleted = i
			m.ensureHistoryVisible()
			return
		}
	}
	m.selCompleted = vis[len(vis)-1]
	m.ensureHistoryVisible()
}

// moveHistorySelection moves the history cursor one visible entry, wrapping.
func (m *Model) moveHistorySelection(delta int) {
	vis := m.visibleHistory()
	if len(vis) == 0 {
		return
	}
	pos := visiblePos(vis, m.selCompleted)
	if pos < 0 {
		if delta < 0 {
			pos = len(vis) - 1
		} else {
			pos = 0
		}
	} else {
		pos = (pos + delta + len(vis)) % len(vis)
	}
	m.selCompleted = vis[pos]
	m.ensureHistoryVisible()
}

// ensureHistoryVisible scrolls the history window so the selected entry is
// inside it; scrollCompleted is a position in the visible list.
func (m *Model) ensureHistoryVisible() {
	window := m.windowSize()
	vis := m.visibleHistory()
	if len(vis) == 0 {
		m.selCompleted, m.scrollCompleted = 0, 0
		return
	}
	pos := visiblePos(vis, m.selCompleted)
	if pos < 0 {
		m.selCompleted = vis[0]
		pos = 0
	}
	if pos < m.scrollCompleted {
		m.scrollCompleted = pos
	}
	if pos >= m.scrollCompleted+window {
		m.scrollCompleted = pos - window + 1
	}
	m.scrollCompleted = clampInt(m.scrollCompleted, 0, maxInt(0, len(vis)-window))
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
		value := m.input
		key := m.inputKey
		m.inputActive = false
		m.input = ""
		m.inputKey = ""

		if key != "" {
			// Applying a settings value: validate, warn on refusal.
			if m.opts.OnSettingSet != nil {
				if err := m.opts.OnSettingSet(key, value); err != nil {
					m.warn(err.Error())
				} else {
					m.setSettingValue(key, value)
				}
			}
			return m, nil
		}

		urls := parseURLList(value)
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
			// Added rows may not match the applied filter; keep the cursor
			// on something the user can see.
			m.clampSelectionToVisible()
		}
	case tea.KeyBackspace:
		// Rune-safe: delete one visible character, not one byte.
		if runes := []rune(m.input); len(runes) > 0 {
			m.input = string(runes[:len(runes)-1])
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

// ---------------------------------------------------------------------------
// List filter ('/')
// ---------------------------------------------------------------------------

// filterState returns a page's filter state; pages without a list yield the
// zero value (never open, empty query).
func (m Model) filterState(tab Tab) filterState {
	if int(tab) < 0 || int(tab) > int(TabSummary) {
		return filterState{}
	}
	return m.filters[tab]
}

// filterAt is the mutating counterpart of filterState.
func (m *Model) filterAt(tab Tab) *filterState {
	if int(tab) < 0 || int(tab) > int(TabSummary) {
		return nil
	}
	return &m.filters[tab]
}

// clampCurrentSelection keeps the current page's cursor on a visible row
// after the query changed.
func (m *Model) clampCurrentSelection() {
	if m.tab == TabCompleted {
		m.clampHistorySelection()
		return
	}
	m.clampSelectionToVisible()
}

// openFilter starts the '/' prompt on a page that has a list. The prompt
// opens empty (the query is typed fresh) and narrows the list live.
func (m *Model) openFilter() {
	if m.tab != TabDownloads && m.tab != TabCompleted {
		return
	}
	f := m.filterAt(m.tab)
	if f == nil {
		return
	}
	f.open = true
	f.query = ""
	m.clampCurrentSelection()
}

// clearFilter drops the current page's applied query and reports whether
// there was one: esc clears the filter, then a second esc quits.
func (m *Model) clearFilter() bool {
	f := m.filterAt(m.tab)
	if f == nil || f.query == "" {
		return false
	}
	f.query, f.open = "", false
	m.clampCurrentSelection()
	return true
}

// handleFilterKey drives the '/' line: runes accumulate, backspace edits,
// enter keeps the query, esc clears it.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.filterAt(m.tab)
	if f == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		f.open, f.query = false, ""
		m.clampCurrentSelection()
	case tea.KeyEnter:
		f.open = false
	case tea.KeyBackspace:
		if runes := []rune(f.query); len(runes) > 0 {
			f.query = string(runes[:len(runes)-1])
		}
		m.clampCurrentSelection()
	case tea.KeySpace:
		f.query += " "
		m.clampCurrentSelection()
	case tea.KeyRunes:
		f.query += string(msg.Runes)
		m.clampCurrentSelection()
	}
	return m, nil
}

// matchesFilter reports whether text contains the applied query, ignoring
// case. An empty query matches everything.
func matchesFilter(text, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

// visibleRows returns the absolute indices of the queue rows that pass the
// downloads filter, in display order.
func (m Model) visibleRows() []int {
	vis := make([]int, 0, len(m.rows))
	for i, r := range m.rows {
		if matchesFilter(r.Label, m.filters[TabDownloads].query) {
			vis = append(vis, i)
		}
	}
	return vis
}

// visibleHistory is visibleRows for the completed tab.
func (m Model) visibleHistory() []int {
	vis := make([]int, 0, len(m.opts.Completed))
	for i, e := range m.opts.Completed {
		if matchesFilter(e.Text, m.filters[TabCompleted].query) {
			vis = append(vis, i)
		}
	}
	return vis
}

// visiblePos is idx's position in a visible index list, -1 when filtered out.
func visiblePos(vis []int, idx int) int {
	for pos, i := range vis {
		if i == idx {
			return pos
		}
	}
	return -1
}

// clampSelectionToVisible keeps the downloads selection on a visible row
// after the query, the row list, or the queue changed.
func (m *Model) clampSelectionToVisible() {
	vis := m.visibleRows()
	if len(vis) == 0 {
		m.selected, m.scrollOffset = 0, 0
		return
	}
	if visiblePos(vis, m.selected) >= 0 {
		m.ensureSelectionVisible()
		return
	}
	for _, i := range vis {
		if i > m.selected {
			m.selected = i
			m.ensureSelectionVisible()
			return
		}
	}
	m.selected = vis[len(vis)-1]
	m.ensureSelectionVisible()
}

// confirmDeleteSelected asks before removing the selected file from the
// queue (confirming also clears its queue-file line via OnDelete).
func (m *Model) confirmDeleteSelected() {
	if m.opts.OnDelete == nil || len(m.rows) == 0 {
		return
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	// With no visible match there is nothing to confirm deleting.
	if visiblePos(m.visibleRows(), m.selected) < 0 {
		return
	}
	idx := m.selected
	row := m.rows[idx]
	m.confirm = &confirmPrompt{
		Prompt: fmt.Sprintf("Remove from the queue?  %s", truncateDisplay(row.Label, 48)),
		OnYes: func(cur *Model) {
			cur.opts.OnDelete(row.ID)
			cur.rows = append(cur.rows[:idx], cur.rows[idx+1:]...)
			if cur.selected >= len(cur.rows) && cur.selected > 0 {
				cur.selected--
			}
			cur.clampSelectionToVisible()
		},
	}
}

// moveSelected reorders the selected row within the queue. With a filter
// applied it swaps with the next/previous visible row: the selection always
// moves one visible step, and hidden rows in between shift along with it.
func (m *Model) moveSelected(delta int) {
	if m.opts.OnReorder == nil || len(m.rows) < 2 {
		return
	}
	vis := m.visibleRows()
	pos := visiblePos(vis, m.selected)
	target := -1
	if pos >= 0 {
		if next := pos + delta; next >= 0 && next < len(vis) {
			target = vis[next]
		}
	}
	if target < 0 {
		return
	}
	m.rows[m.selected], m.rows[target] = m.rows[target], m.rows[m.selected]
	m.selected = target
	m.ensureSelectionVisible()
	m.persistOrder()
}

// moveSelectedToEdge moves the selected row to the top or bottom of the
// queue, keeping every other row's relative order. With a filter applied,
// "top"/"bottom" is the first/last visible row: the moved row lands where
// the eye expects, and hidden rows keep their slots.
func (m *Model) moveSelectedToEdge(toTop bool) {
	if m.opts.OnReorder == nil || len(m.rows) < 2 {
		return
	}
	vis := m.visibleRows()
	if visiblePos(vis, m.selected) < 0 {
		return
	}
	edge := vis[0]
	if !toTop {
		edge = vis[len(vis)-1]
	}
	// Already at that edge: nothing to persist.
	if edge == m.selected {
		return
	}

	row := m.rows[m.selected]
	rest := make([]*Row, 0, len(m.rows)-1)
	rest = append(rest, m.rows[:m.selected]...)
	rest = append(rest, m.rows[m.selected+1:]...)

	// The edge's position in the list that no longer holds the moved row,
	// then before it (top) or right after it (bottom).
	target := edge
	if target > m.selected {
		target--
	}
	if !toTop {
		target++
	}

	rows := make([]*Row, 0, len(m.rows))
	rows = append(rows, rest[:target]...)
	rows = append(rows, row)
	rows = append(rows, rest[target:]...)
	m.rows = rows
	m.selected = target
	m.ensureSelectionVisible()
	m.persistOrder()
}

// retryAllFailed re-dispatches every failed row, so a run that lost a
// network blip does not take one keypress per file. A filter does not
// narrow it (start-all ignores the filter too): R means "fix everything
// that broke".
func (m *Model) retryAllFailed() {
	if m.opts.OnControl == nil {
		return
	}
	for _, r := range m.rows {
		if r.Status == RowFailed {
			m.opts.OnControl(r.ID, ControlRetry)
		}
	}
}

// persistOrder sends the current display order to the queue file and the
// engine's flow priority.
func (m *Model) persistOrder() {
	if m.opts.OnReorder == nil {
		return
	}
	ids := make([]int, len(m.rows))
	for i, r := range m.rows {
		ids[i] = r.ID
	}
	m.opts.OnReorder(ids)
}

// moveSelection moves the cursor by delta rows, skipping rows the filter
// hides. An empty list leaves the cursor at the top.
func (m *Model) moveSelection(delta int) {
	vis := m.visibleRows()
	if len(vis) == 0 {
		m.selected = 0
		return
	}
	pos := visiblePos(vis, m.selected)
	if pos < 0 {
		// The selected row was filtered out: continue from the nearest
		// edge in the direction of travel.
		if delta < 0 {
			pos = len(vis) - 1
		} else {
			pos = 0
		}
	} else {
		pos = (pos + delta + len(vis)) % len(vis)
	}
	m.selected = vis[pos]
	m.ensureSelectionVisible()
}

// ensureSelectionVisible scrolls the window so the selected row is inside
// the visible slice. scrollOffset is a position in the visible list, which
// equals the row's absolute index when no filter is applied.
func (m *Model) ensureSelectionVisible() {
	window := m.windowSize()
	vis := m.visibleRows()
	if len(vis) == 0 {
		m.selected, m.scrollOffset = 0, 0
		return
	}
	pos := visiblePos(vis, m.selected)
	if pos < 0 {
		m.selected = vis[0]
		pos = 0
	}
	if pos < m.scrollOffset {
		m.scrollOffset = pos
	}
	if pos >= m.scrollOffset+window {
		m.scrollOffset = pos - window + 1
	}
	m.scrollOffset = clampInt(m.scrollOffset, 0, maxInt(0, len(vis)-window))
}

func (m *Model) controlSelected(action func(row *Row) ControlAction) {
	if m.opts.OnControl == nil || len(m.rows) == 0 {
		return
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
	// A filter with no matches leaves no row to act on: never touch a row
	// the user cannot see.
	if visiblePos(m.visibleRows(), m.selected) < 0 {
		return
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
		return padLines(strings.TrimRight(m.viewHelp(), "\n"), m.height)
	}

	var b strings.Builder

	header := StyleTitle.Render("dlm") + StyleDim.Render("  interactive ui")
	workerWord := "workers"
	if m.opts.Jobs == 1 {
		workerWord = "worker"
	}
	right := StyleDim.Render(fmt.Sprintf("%d %s · %d files", m.opts.Jobs, workerWord, len(m.rows)))
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

	var body string
	switch m.tab {
	case TabDownloads:
		body = m.viewDownloads()
	case TabCompleted:
		body = m.viewCompleted()
	case TabSettings:
		body = m.viewSettings()
	case TabSummary:
		body = m.viewSummary()
	}
	// Exactly one newline after the body: any more would add a phantom line
	// that pushes the header off a full-height frame.
	b.WriteString(strings.TrimRight(body, "\n"))

	// Insert the slack between the body and the bottom chrome so the footer
	// stays on the window's last line whatever the body height: a short list
	// leaves the body area tall rather than floating the status rows up.
	return pinBottom(b.String(), m.viewBottom(), m.height)
}

// viewBottom renders the bottom chrome: transient warnings, the rule, then
// either the modal/input lines or the status, key hints, and footer.
func (m Model) viewBottom() string {
	var b strings.Builder

	for _, w := range m.warnings {
		// Keep a long diagnostic (an update failure, say) on one line so the
		// reserved chrome rows stay honest.
		b.WriteString(StyleWarn.Render(truncateStyled(w, m.contentWidth())) + "\n")
	}
	b.WriteString(StyleDim.Render("─") + "\n")

	// Modal layers replace the status/keys area.
	if m.confirm != nil {
		b.WriteString(StyleWarn.Render("confirm: ") + m.confirm.Prompt + "\n")
		b.WriteString(StyleDim.Render("y/enter confirm · n/esc cancel"))
		return b.String()
	}
	if f := m.filterState(m.tab); f.open {
		_, shown, total := m.filterScope()
		b.WriteString(StyleStarting.Render("filter: ") + f.query + StyleDim.Render("▏"))
		b.WriteString(StyleDim.Render(fmt.Sprintf("   %d of %d · enter keep · esc clear", shown, total)))
		return b.String()
	}
	if m.inputActive {
		label := "add url(s): "
		hint := "enter to add · esc to cancel"
		if m.inputKey != "" {
			label = "set " + m.inputKey + " = "
			hint = "enter to apply · esc to cancel"
		}
		b.WriteString(StyleStarting.Render(label) + m.input + StyleDim.Render("▏"))
		b.WriteString(StyleDim.Render("   " + hint))
		return b.String()
	}

	saved, failed := m.counts()
	// The summary panel is a snapshot, not a run view: it shows no status.
	if m.tab != TabSummary {
		if note := m.filterNote(); note != "" {
			b.WriteString(StyleDim.Render(note) + " · ")
		}
		switch {
		case m.finished():
			summary := fmt.Sprintf("run finished — %d saved, %d failed", saved, failed)
			if failed > 0 {
				b.WriteString(StyleErr.Render(summary))
			} else {
				b.WriteString(StyleOK.Render(summary))
			}
		case len(m.rows) == 0:
			// The board opened an empty queue: invite the user to fill it.
			b.WriteString(StyleDim.Render("queue is empty · press a to add a URL"))
		case m.runningCount() == 0 && m.waitingCount() > 0:
			b.WriteString(StyleDim.Render(fmt.Sprintf(
				"idle — %d waiting · space start one · s start all (%d at a time)",
				m.waitingCount(), m.opts.Jobs,
			)))
		default:
			status := fmt.Sprintf(
				"running %d/%d files · %d saved, %d failed",
				m.runningCount(), len(m.rows), saved, failed,
			)
			if speed := m.currentSpeed(); speed > 0 {
				status += fmt.Sprintf(" · %s/s %s", humanBytes(int64(speed)), sparkline(m.hist))
			}
			b.WriteString(StyleDim.Render(status))
		}
	}
	for _, line := range wrapHint(m.pageHints(), m.contentWidth()) {
		b.WriteString("\n" + StyleDim.Render(line))
	}
	if line := m.updateLine(); line != "" {
		b.WriteString("\n" + line)
	}
	footer := StyleDim.Render("tab switch · 1-4 tabs · ? help · q quit")
	if m.opts.Version != "" {
		footer = padBetween(footer, StyleDim.Render("dlm "+m.opts.Version), m.width)
	}
	b.WriteString("\n" + footer)
	return b.String()
}

// updateLineShown reports whether the update notice adds a line above the
// footer. Modal layers (confirm, input) replace that whole area, so the
// window must not reserve a row for it then.
func (m Model) updateLineShown() bool {
	return m.confirm == nil && !m.inputActive && !m.filterState(m.tab).open &&
		(m.updating || m.updatedTo != "" || m.updateAvailable != "")
}

// updateLine is the standalone notice between the key hints and the footer:
// the offered release with its shortcut, the running install, or the restart
// reminder after success.
func (m Model) updateLine() string {
	switch {
	case m.updating:
		return StyleWarn.Render(spinnerFrame(m.spin) + " updating dlm to v" + m.updateAvailable + "…")
	case m.updatedTo != "":
		return StyleOK.Render("dlm v" + m.updatedTo + " installed · restart dlm to use it · q quit")
	case m.updateAvailable != "":
		return StyleWarn.Render("v" + m.updateAvailable + " is available · u update")
	}
	return ""
}

// pageHints is the per-page key reminder shown above the global line.
func (m Model) pageHints() string {
	switch m.tab {
	case TabCompleted:
		return "j/k scroll · d delete entry · c clear history · / filter"
	case TabSettings:
		return "j/k select · enter edit · space toggle · r reset · R reset all · o editor"
	case TabSummary:
		return "r refresh"
	default:
		sKey := "s start all"
		if m.activeCount() > 0 {
			sKey = "s stop all"
		}
		return fmt.Sprintf(
			"space start/pause · %s · r retry · R retry all · x cancel · a add · / filter · d delete · J/K g/G move",
			sKey,
		)
	}
}

// filterScope is the current page's applied query and how many rows it
// matches out of the page's total. Pages without a filterable list report an
// empty query and zero counts.
func (m Model) filterScope() (query string, shown, total int) {
	switch m.tab {
	case TabDownloads:
		return m.filters[TabDownloads].query, len(m.visibleRows()), len(m.rows)
	case TabCompleted:
		return m.filters[TabCompleted].query, len(m.visibleHistory()), len(m.opts.Completed)
	}
	return "", 0, 0
}

// filterNote summarizes the applied filter above the run status:
// `filter "mkv" · 3 of 40`. Empty when the current page has no query.
func (m Model) filterNote() string {
	query, shown, total := m.filterScope()
	if query == "" {
		return ""
	}
	return fmt.Sprintf("filter %q · %d of %d", query, shown, total)
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
		return StyleDim.Render("queue is empty") + "\n" +
			StyleDim.Render("press a to add a URL, or o to edit the queue file in $EDITOR") + "\n"
	}

	vis := m.visibleRows()
	if len(vis) == 0 {
		return fmt.Sprintf("%s %q\n", StyleDim.Render("no files match"), m.filters[TabDownloads].query) +
			StyleDim.Render("esc clears the filter · / edits it") + "\n"
	}

	var b strings.Builder

	window := m.windowSize()
	labelW, barW := m.rowLayout()

	end := m.scrollOffset + window
	if end > len(vis) {
		end = len(vis)
	}
	if m.scrollOffset > 0 {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▲ %d more above", m.scrollOffset)) + "\n")
	}
	for pos := m.scrollOffset; pos < end; pos++ {
		i := vis[pos]
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
	if end < len(vis) {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below", len(vis)-end)) + "\n")
	}
	for _, line := range m.static {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// viewCompleted renders the history with its own cursor and scroll window,
// framed by the roll-up: total downloads and bytes on top, the output
// directory's current state below.
func (m Model) viewCompleted() string {
	var b strings.Builder

	s := m.opts.Summary
	if s.Count > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("%d downloads", s.Count)))
		if s.Bytes > 0 {
			b.WriteString(StyleDim.Render(" · " + humanBytes(s.Bytes) + " saved"))
		}
		b.WriteString("\n")
	}

	vis := m.visibleHistory()
	if len(vis) == 0 {
		if q := m.filters[TabCompleted].query; q != "" {
			b.WriteString(fmt.Sprintf("%s %q\n", StyleDim.Render("no entries match"), q))
			b.WriteString(StyleDim.Render("esc clears the filter · / edits it") + "\n")
			if s.Dir != "" {
				b.WriteString(m.outputLine())
			}
			return b.String()
		}
		b.WriteString(StyleDim.Render("no completed downloads yet") + "\n")
		if s.Dir != "" {
			b.WriteString(m.outputLine())
		}
		return b.String()
	}

	window := m.windowSize()
	end := m.scrollCompleted + window
	if end > len(vis) {
		end = len(vis)
	}

	if m.scrollCompleted > 0 {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▲ %d more above", m.scrollCompleted)) + "\n")
	}
	for pos := m.scrollCompleted; pos < end; pos++ {
		i := vis[pos]
		marker := "  "
		if i == m.selCompleted {
			marker = StyleTabActive.Render("▸ ")
		}
		b.WriteString(marker + truncateDisplay(m.opts.Completed[i].Text, m.contentWidth()-2) + "\n")
	}
	if end < len(vis) {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below", len(vis)-end)) + "\n")
	}

	if s.Dir != "" {
		b.WriteString(m.outputLine())
	}
	return b.String()
}

// outputLine describes where downloads land: directory, file count, size.
func (m Model) outputLine() string {
	s := m.opts.Summary
	return StyleDim.Render(fmt.Sprintf("output  %s — %d files, %s",
		s.Dir, s.Files, humanBytes(s.DirBytes))) + "\n"
}

// viewSettings renders the config rows with their own cursor and window.
func (m Model) viewSettings() string {
	if len(m.opts.Settings) == 0 {
		return StyleDim.Render("no settings available") + "\n"
	}
	window := m.windowSize()
	end := m.scrollSettings + window
	if end > len(m.opts.Settings) {
		end = len(m.opts.Settings)
	}

	var b strings.Builder
	if m.scrollSettings > 0 {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▲ %d more above", m.scrollSettings)) + "\n")
	}
	keyWidth := 0
	for _, st := range m.opts.Settings {
		if len(st.Key) > keyWidth {
			keyWidth = len(st.Key)
		}
	}
	for i := m.scrollSettings; i < end; i++ {
		st := m.opts.Settings[i]
		marker := "  "
		if i == m.selSettings {
			marker = StyleTabActive.Render("▸ ")
		}
		value := st.Value
		switch st.Kind {
		case SettingBool:
			if value == "true" {
				value = StyleOK.Render(value)
			} else {
				value = StyleDim.Render(value)
			}
		case SettingInt:
			value = StyleStarting.Render(value)
		}
		line := fmt.Sprintf("%s%-*s  %s", marker, keyWidth, st.Key, value)
		if st.Help != "" {
			line += "  " + StyleDim.Render(st.Help)
		}
		b.WriteString(truncateStyled(line, m.contentWidth()) + "\n")
	}
	if end < len(m.opts.Settings) {
		b.WriteString(StyleDim.Render(fmt.Sprintf("  ▼ %d more below", len(m.opts.Settings)-end)) + "\n")
	}

	b.WriteString("\n" + StyleWarn.Render("note: restart dlm after a change so it takes full effect"))
	b.WriteString("\n" + StyleWarn.Render("note: changing num_chunks restarts partial downloads from the beginning"))
	return b.String()
}

// viewSummary is the dashboard panel: the same roll-up the plain snapshot
// prints — queue, completed, downloads dir, settings, platform — assembled
// from the refreshed Summary.
func (m Model) viewSummary() string {
	s := m.opts.Summary

	queue := "empty"
	if s.Pending > 0 {
		queue = fmt.Sprintf("%d pending", s.Pending)
	}

	completed := "none yet"
	if s.Count > 0 {
		completed = fmt.Sprintf("%d downloads · %s saved", s.Count, humanBytes(s.Bytes))
	}

	downloads := "unknown"
	if s.Dir != "" {
		downloads = fmt.Sprintf("%s — %d files, %s", s.Dir, s.Files, humanBytes(s.DirBytes))
	}

	settings := s.Settings
	if settings == "" {
		settings = "—"
	}
	platform := s.Platform
	if platform == "" {
		platform = "—"
	}

	rows := []struct{ label, value string }{
		{"queue", queue},
		{"completed", completed},
		{"downloads", downloads},
		{"settings", settings},
		{"platform", platform},
	}

	labelW := 0
	for _, r := range rows {
		if len(r.label) > labelW {
			labelW = len(r.label)
		}
	}

	var b strings.Builder
	for _, r := range rows {
		line := StyleDim.Render(fmt.Sprintf("%-*s", labelW, r.label)) + "  " + r.value
		b.WriteString(truncateStyled(line, m.contentWidth()) + "\n")
	}
	return b.String()
}

// truncateStyled clips a styled line to max visible cells, appending nothing
// when it already fits.
func truncateStyled(line string, max int) string {
	if max <= 0 || lipglossWidth(line) <= max {
		return line
	}
	// Cutting inside ANSI sequences would corrupt styling; keep whole
	// glyphs by trimming visible runes from the end.
	runes := []rune(stripANSI(line))
	if len(runes) <= max-1 {
		return line
	}
	return string(runes[:max-1]) + "…"
}

// stripANSI removes escape sequences, leaving the visible text.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		default:
			b.WriteRune(r)
		}
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
	m.clampSelectionToVisible()
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
		StyleTitle.Render("dlm — keys"),
		"",
		StyleDim.Render("downloads"),
		"  space      start the selected file / pause it / back to held",
		"  s          start all (flowing: " + fmt.Sprint(m.opts.Jobs) + " at a time) · press again to stop all",
		"  r          retry the selected file (failed, paused or cancelled)",
		"  R          retry every failed file (ignores the filter)",
		"  x          cancel the selected file",
		"  j/k        select (up/down scrolls the queue list)",
		"",
		StyleDim.Render("queue editing"),
		"  a          add URLs (paste several, whitespace separated; enter adds)",
		"  d          delete the selected file from the queue",
		"  J/K        move the selected file down/up (changes flow order)",
		"  g/G        move the selected file to the top/bottom (also ctrl+↑/↓)",
		"  o          edit the queue file in $EDITOR (settings tab: the config)",
		"",
		StyleDim.Render("board"),
		"  1-4, tab   downloads / completed / settings / summary",
		"  mouse      click a tab or row · wheel scrolls the current list",
		"  ?          this help · q quit (the run stops, parts resume later)",
		"",
		StyleDim.Render("filtering (downloads, completed)"),
		"  /          narrow the list to matching names as you type",
		"  enter      keep the filter · esc clears it (esc again quits)",
		"",
		StyleDim.Render("summary tab"),
		"  r          refresh the queue/completed/downloads totals",
		"",
		StyleDim.Render("updates"),
		"  u          install the newer release the footer offers, then restart dlm",
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

// padLines appends blank lines so frame fills height rows. For frames without
// a pinned last line (the help overlay); frame must not end with a newline,
// or the count gains a phantom line.
func padLines(frame string, height int) string {
	if height <= 0 {
		return frame
	}
	lines := strings.Count(frame, "\n") + 1
	if lines >= height {
		return frame
	}
	return frame + strings.Repeat("\n", height-lines)
}

// pinBottom joins top and bottom into a frame that fills height rows by
// growing the gap between them, so bottom's last line lands on the window's
// bottom edge. Neither fragment may end with a newline; a frame that already
// meets or exceeds the window is joined without extra blank lines.
func pinBottom(top, bottom string, height int) string {
	if height <= 0 {
		return top + "\n" + bottom
	}
	lines := strings.Count(top, "\n") + 1 + strings.Count(bottom, "\n") + 1
	blank := height - lines
	if blank <= 0 {
		return top + "\n" + bottom
	}
	return top + strings.Repeat("\n", blank+1) + bottom
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
