package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel() Model {
	return NewModel(Options{
		QueueURLs: []string{
			"https://example.com/FAB%20-%2022.mkv",
			"https://example.com/FAB%20-%2023.mkv",
		},
		Completed: []string{"2026-10-11 10:01:22  FAB - 21.mkv"},
		Settings:  "settings body",
		Jobs:      2,
	})
}

// update is a small helper applying one message and returning the model.
func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestBoardShowsRunningRow(t *testing.T) {
	m := newTestModel()

	row := newRow(0, "FAB%20-%2022.mkv", 1000, 0)
	m = update(m, FileStartedMsg{ID: row.ID, Row: row})
	row.Add(250)

	view := m.View()
	if !strings.Contains(view, "FAB - 22.mkv") {
		t.Fatalf("row label missing from view:\n%s", view)
	}
	if !strings.Contains(view, "25.0%") {
		t.Fatalf("percentage missing from view:\n%s", view)
	}
	if !strings.Contains(view, "1/2 files") {
		t.Fatalf("progress counter missing from view:\n%s", view)
	}
}

func TestBoardSettlesRowsWithStatus(t *testing.T) {
	m := newTestModel()

	saved := newRow(0, "a.mkv", 10, 0)
	failed := newRow(1, "b.mkv", 10, 0)
	m = update(m, FileStartedMsg{ID: saved.ID, Row: saved})
	m = update(m, FileStartedMsg{ID: failed.ID, Row: failed})

	m = update(m, FileDoneMsg{Row: saved, Text: "✓ saved → /tmp/a.mkv"})
	m = update(m, JobStateMsg{ID: failed.ID, State: RowFailed, Detail: "timeout"})

	if saved.Status != RowSaved || failed.Status != RowFailed {
		t.Fatalf("statuses not applied: saved=%v failed=%v", saved.Status, failed.Status)
	}

	m = update(m, RunFinishedMsg{})
	view := m.View()
	for _, want := range []string{"✓ saved → /tmp/a.mkv", "✗ failed  b.mkv", "timeout", "1 saved, 1 failed"} {
		if !strings.Contains(view, want) {
			t.Errorf("%q missing from view:\n%s", want, view)
		}
	}
}

func TestBoardPreRowFailureCreatesFailedRow(t *testing.T) {
	m := newTestModel()
	m = update(m, JobStateMsg{
		ID: 7, Label: "ghost.mkv", State: RowFailed,
		Detail: "no route to host",
	})

	view := m.View()
	for _, want := range []string{"ghost.mkv", "no route to host", "✗ failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("%q missing:\n%s", want, view)
		}
	}
}

func TestBoardSelectionAndControls(t *testing.T) {
	var gotID int
	var gotAction ControlAction
	opts := Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv"},
		Jobs:      2,
		OnControl: func(id int, action ControlAction) { gotID, gotAction = id, action },
	}
	m := NewModel(opts)

	rowA := newRow(0, "a.mkv", 10, 0)
	rowB := newRow(1, "b.mkv", 10, 0)
	m = update(m, FileStartedMsg{ID: 0, Row: rowA})
	m = update(m, FileStartedMsg{ID: 1, Row: rowB})

	// space on a running row pauses it.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if gotID != 1 || gotAction != ControlPause {
		t.Fatalf("space on running row: got id=%d action=%v, want pause on job 1", gotID, gotAction)
	}

	// Pause reflects in the row and space then means resume.
	m = update(m, JobStateMsg{ID: 1, State: RowPaused})
	if !strings.Contains(m.View(), "paused") {
		t.Fatalf("paused row not rendered:\n%s", m.View())
	}
	m = update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if gotID != 1 || gotAction != ControlResume {
		t.Fatalf("space on paused row: got id=%d action=%v, want resume on job 1", gotID, gotAction)
	}

	// A waiting row: space means start that one file.
	waiting := NewModel(Options{
		QueueURLs: []string{"https://x/w1.mkv", "https://x/w2.mkv"},
		Jobs:      2,
		OnControl: func(id int, action ControlAction) { gotID, gotAction = id, action },
	})
	waiting = update(waiting, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	_ = update(waiting, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if gotID != 1 || gotAction != ControlStart {
		t.Fatalf("space on waiting row: got id=%d action=%v, want start on job 1", gotID, gotAction)
	}

	// x cancels, r retries the selected row.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if gotAction != ControlCancel {
		t.Fatalf("x: got %v, want cancel", gotAction)
	}
	update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if gotAction != ControlRetry {
		t.Fatalf("r: got %v, want retry", gotAction)
	}
}

func TestBoardIdleFooterAndStartAllKey(t *testing.T) {
	var gotID int
	var gotAction ControlAction
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv"},
		Jobs:      2,
		OnControl: func(id int, action ControlAction) { gotID, gotAction = id, action },
	})

	view := m.View()
	if !strings.Contains(view, "idle") || !strings.Contains(view, "s start all") {
		t.Fatalf("board must explain it is idle before anything starts:\n%s", view)
	}

	// s switches to the flowing, download-all mode.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if gotAction != ControlStartAll || gotID != -1 {
		t.Fatalf("s: got id=%d action=%v, want start-all", gotID, gotAction)
	}

	// Once a file runs, the footer reports progress again.
	m = update(m, FileStartedMsg{ID: 0, Row: newRow(0, "a.mkv", 100, 0)})
	view = m.View()
	if !strings.Contains(view, "running 1/2 files") {
		t.Fatalf("running footer missing:\n%s", view)
	}

	// With work active, s becomes the stop-all toggle.
	if !strings.Contains(view, "s stop all") {
		t.Fatalf("footer must offer stop-all while running:\n%s", view)
	}
	_ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if gotAction != ControlStopAll {
		t.Fatalf("second s: got %v, want stop-all", gotAction)
	}

	// After the engine confirms everything is held again, s starts all.
	m = update(m, JobStateMsg{ID: 0, State: RowQueued})
	_ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if gotAction != ControlStartAll {
		t.Fatalf("s after stop: got %v, want start-all", gotAction)
	}
}

func TestBoardPausedRowKeepsRunOpen(t *testing.T) {
	m := newTestModel()
	done := newRow(0, "done.mkv", 10, 0)
	paused := newRow(1, "paused.mkv", 10, 0)
	m = update(m, FileStartedMsg{ID: 0, Row: done})
	m = update(m, FileStartedMsg{ID: 1, Row: paused})
	m = update(m, FileDoneMsg{Row: done, Text: "✓ saved → /tmp/done.mkv"})
	m = update(m, JobStateMsg{ID: 1, State: RowPaused})

	if m.finished() {
		t.Fatal("a paused job must keep the run open (it is resumable)")
	}
	if !strings.Contains(m.View(), "running 0/2 files") {
		t.Errorf("footer should not claim completion:\n%s", m.View())
	}
}

func TestBoardScrollingWindow(t *testing.T) {
	urls := make([]string, 25)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}
	m := NewModel(Options{QueueURLs: urls, Jobs: 2})
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 17}) // window of 10 rows

	view := m.View()
	if !strings.Contains(view, "file-00.mkv") || !strings.Contains(view, "file-09.mkv") {
		t.Fatalf("first window must show rows 0-9:\n%s", view)
	}
	if strings.Contains(view, "file-10.mkv") {
		t.Fatalf("row 10 must be outside the window:\n%s", view)
	}
	if !strings.Contains(view, "▼ 15 more below") {
		t.Fatalf("bottom indicator missing:\n%s", view)
	}
	if strings.Contains(view, "▲") {
		t.Fatalf("nothing is above the first window:\n%s", view)
	}

	// Scrolling down moves the window and shows the above-indicator.
	for i := 0; i < 11; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	view = m.View()
	if m.selected != 11 || m.scrollOffset != 2 {
		t.Fatalf("selection=%d offset=%d, want 11/2", m.selected, m.scrollOffset)
	}
	if !strings.Contains(view, "▲ 2 more above") {
		t.Fatalf("top indicator missing:\n%s", view)
	}
	if strings.Contains(view, "file-00.mkv") {
		t.Fatalf("scrolled-out rows must not render:\n%s", view)
	}

	// Scrolling to the bottom clamps the window and hides the bottom
	// indicator.
	for i := 0; i < 14; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.selected != 0 { // wrapped around to the top
		t.Fatalf("selection wrap: got %d, want 0", m.selected)
	}
	if m.scrollOffset != 0 {
		t.Fatalf("window must snap back to the top on wrap, got offset %d", m.scrollOffset)
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}) // wrap up
	if m.selected != len(urls)-1 {
		t.Fatalf("selection = %d, want %d", m.selected, len(urls)-1)
	}
	view = m.View()
	if !strings.Contains(view, "file-24.mkv") {
		t.Fatalf("last row must be visible after wrap:\n%s", view)
	}
	if strings.Contains(view, "▼") {
		t.Fatalf("nothing is below the last window:\n%s", view)
	}
	if !strings.Contains(view, "▲ 15 more above") {
		t.Fatalf("top indicator for the last window:\n%s", view)
	}
}

// TestBoardWindowAdaptsToTerminalHeight: the visible row count follows the
// terminal height instead of a fixed 10.
func TestBoardWindowAdaptsToTerminalHeight(t *testing.T) {
	urls := make([]string, 25)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}

	// Short terminal: fewer rows, bottom marker counts the rest.
	short := NewModel(Options{QueueURLs: urls, Jobs: 2})
	short = update(short, tea.WindowSizeMsg{Width: 120, Height: 12})
	view := short.View()
	if !strings.Contains(view, "file-04.mkv") || strings.Contains(view, "file-05.mkv") {
		t.Fatalf("height 12 should show 5 rows:\n%s", view)
	}
	if !strings.Contains(view, "▼ 20 more below") {
		t.Fatalf("bottom marker wrong for height 12:\n%s", view)
	}

	// Tall terminal: everything fits, no markers.
	tall := NewModel(Options{QueueURLs: urls, Jobs: 2})
	tall = update(tall, tea.WindowSizeMsg{Width: 120, Height: 40})
	view = tall.View()
	if !strings.Contains(view, "file-24.mkv") {
		t.Fatalf("tall terminal must show every row:\n%s", view)
	}
	if strings.Contains(view, "▲") || strings.Contains(view, "▼") {
		t.Fatalf("no scroll markers expected when all rows fit:\n%s", view)
	}
}

// TestBoardRowsFitTerminalWidth: label and bar widths derive from the
// terminal width so a full row (marker included) never overflows.
func TestBoardRowsFitTerminalWidth(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		m := NewModel(Options{QueueURLs: []string{"https://x/very-long-file-name-that-should-truncate.mkv"}, Jobs: 1})
		m = update(m, tea.WindowSizeMsg{Width: width, Height: 30})

		labelW, barW := m.rowLayout()
		row := newRow(0, "very-long-file-name-that-should-truncate.mkv", 200, 0)
		row.Start = time.Now().Add(-2 * time.Second)
		row.Add(100)

		line := renderRow(row, labelW, barW, width)
		if got := lipglossWidth(line) + 2; got > width { // +2 for "▸ "
			t.Errorf("width %d: row is %d cells wide, overflows", width, got)
		}
	}
}

// TestBoardHeldVersusStartingStates: the user must be able to tell "needs
// my input" (held, waiting for space/s) from "the app is on it" (starting:
// dispatched or queued behind a worker while the transfer is set up).
func TestBoardHeldVersusStartingStates(t *testing.T) {
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv"},
		Jobs:      1,
	})

	// Everything held: "waiting for you" and no spinner anywhere.
	view := m.View()
	if !strings.Contains(view, "waiting for you") {
		t.Fatalf("held rows must say they wait for the user:\n%s", view)
	}
	if strings.Contains(view, "starting") {
		t.Fatalf("nothing may look like it is starting before the user acts:\n%s", view)
	}

	// Start the first file: its row switches to the animated "starting"
	// state, the second stays held.
	m = update(m, JobStateMsg{ID: 0, State: RowStarting})
	view = m.View()
	if !strings.Contains(view, "starting") {
		t.Fatalf("started row must show the starting state:\n%s", view)
	}
	if !strings.Contains(view, "waiting for you") {
		t.Fatalf("the untouched file must still ask for the user:\n%s", view)
	}

	// The spinner animates across ticks.
	first := m.View()
	m = update(m, tickMsg{})
	if m.View() == first {
		// The bytes did not change either, so only the spinner can differ;
		// identical frames would mean the spinner is static.
		t.Fatalf("starting spinner must animate between ticks")
	}

	// Bytes flowing switches the row to the progress bar.
	live := newRow(0, "a.mkv", 100, 0)
	live.Add(50)
	m = update(m, FileStartedMsg{ID: 0, Row: live})
	view = m.View()
	if !strings.Contains(view, "50.0%") {
		t.Fatalf("live row must show progress:\n%s", view)
	}
	if strings.Contains(view, "starting  a.mkv") {
		t.Fatalf("starting line must be replaced by the bar:\n%s", view)
	}
}

// TestSpaceOnStartingRowPutsItBackToHeld: space on a queued-behind-slots
// row returns it to the held state instead of doing nothing.
func TestSpaceOnStartingRowPutsItBackToHeld(t *testing.T) {
	var action ControlAction
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv"},
		Jobs:      1,
		OnControl: func(_ int, a ControlAction) { action = a },
	})
	m = update(m, JobStateMsg{ID: 0, State: RowStarting})
	_ = update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	if action != ControlPause {
		t.Fatalf("space on a starting row must ask to stop it, got %v", action)
	}
}

// ---------------------------------------------------------------------------
// Queue editing (M3)
// ---------------------------------------------------------------------------

func TestParseURLList(t *testing.T) {
	got := parseURLList("  https://a/1\nhttps://b/2   notaurl\n\thttp://c/3 ")
	want := []string{"https://a/1", "https://b/2", "http://c/3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBoardAddFlow(t *testing.T) {
	var added []string
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv"},
		Jobs:      1,
		OnAdd: func(urls []string) []int {
			added = urls
			return []int{10, 11}
		},
	})

	// 'a' opens the input line; esc cancels without adding.
	_ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.inputActive {
		t.Fatal("a must open the add line")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.inputActive || len(added) != 0 {
		t.Fatal("esc must cancel the add line")
	}

	// Typing and enter adds; pasted whitespace-separated blocks work.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://x/new1.mkv")})
	m = update(m, tea.KeyMsg{Type: tea.KeySpace})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://x/new2.mkv")})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(added) != 2 || added[0] != "https://x/new1.mkv" || added[1] != "https://x/new2.mkv" {
		t.Fatalf("added = %v", added)
	}
	if len(m.rows) != 3 {
		t.Fatalf("rows = %d, want 3 (1 initial + 2 new)", len(m.rows))
	}
	if m.rows[2].Status != RowQueued {
		t.Errorf("new rows must be held, got status %v", m.rows[2].Status)
	}
	if !strings.Contains(m.View(), "new2.mkv") {
		t.Errorf("new row must render:\n%s", m.View())
	}
}

func TestBoardDeleteSelected(t *testing.T) {
	var deleted int
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      1,
		OnDelete:  func(id int) { deleted = id },
	})

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // select b
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if deleted != 1 {
		t.Fatalf("deleted id = %d, want 1", deleted)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.rows))
	}
	view := m.View()
	if strings.Contains(view, "b.mkv") {
		t.Errorf("deleted row still rendered:\n%s", view)
	}
	if !strings.Contains(view, "c.mkv") {
		t.Errorf("later rows must survive:\n%s", view)
	}
}

func TestBoardReorderSelected(t *testing.T) {
	var order []int
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      1,
		OnReorder: func(ids []int) { order = ids },
	})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // select b (id 1)
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}}) // move up

	if len(order) != 3 || order[0] != 1 || order[1] != 0 || order[2] != 2 {
		t.Fatalf("order = %v, want [1 0 2]", order)
	}
	if m.selected != 0 {
		t.Fatalf("selection must follow the moved row, got %d", m.selected)
	}
	view := m.View()
	if strings.Index(view, "b.mkv") > strings.Index(view, "a.mkv") {
		t.Errorf("rows not reordered in view:\n%s", view)
	}

	// At the top edge, K is a no-op.
	update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	if order[0] != 1 {
		t.Errorf("K at the top must not reorder: %v", order)
	}
}

func TestBoardEditorReload(t *testing.T) {
	opened := false
	reloaded := []JobRef{
		{ID: 0, URL: "https://x/a.mkv"},
		{ID: 7, URL: "https://x/late.mkv"},
	}
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv"},
		Jobs:      1,
		OnOpenEditor: func() tea.Cmd {
			opened = true
			return nil
		},
	})

	// Start a transfer so its state must survive the reload.
	live := newRow(0, "a.mkv", 100, 0)
	live.Add(40)
	m = update(m, FileStartedMsg{ID: 0, Row: live})

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if !opened {
		t.Fatal("o must trigger the editor hook")
	}

	m = update(m, QueueReloadMsg{Jobs: reloaded})
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.rows))
	}
	if m.rows[0].Status != RowRunning || m.rows[0].ID != 0 {
		t.Errorf("surviving row must keep its state, got id=%d status=%v", m.rows[0].ID, m.rows[0].Status)
	}
	if m.rows[1].ID != 7 || m.rows[1].Status != RowQueued {
		t.Errorf("new row must be held, got id=%d status=%v", m.rows[1].ID, m.rows[1].Status)
	}
	if strings.Contains(m.View(), "b.mkv") {
		t.Errorf("removed row must disappear:\n%s", m.View())
	}
}

func TestBoardHelpOverlay(t *testing.T) {
	m := newTestModel()
	m = update(m, JobStateMsg{ID: 0, State: RowStarting})

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	view := m.View()
	for _, want := range []string{"keys", "start all", "move the selected file", "waiting for you"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, view)
		}
	}

	// Other keys are inert while help is open.
	before := m.selected
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.selected != before {
		t.Fatal("j must not act while the help overlay is open")
	}

	// esc closes it and the board returns.
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.help {
		t.Fatal("esc must close help")
	}
	if !strings.Contains(m.View(), "downloads · completed · settings") {
		t.Fatalf("board must return after help closes:\n%s", m.View())
	}
}

func TestBoardSettingsEditorIsContextual(t *testing.T) {
	openedQueue, openedConfig := false, false
	m := NewModel(Options{
		QueueURLs:    []string{"https://x/a.mkv"},
		Jobs:         1,
		OnOpenEditor: func() tea.Cmd { openedQueue = true; return nil },
		OnOpenConfig: func() tea.Cmd { openedConfig = true; return nil },
	})

	// Downloads tab: o edits the queue file.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if !openedQueue || openedConfig {
		t.Fatalf("o on downloads: queue=%v config=%v", openedQueue, openedConfig)
	}

	// Settings tab: o edits the config instead.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if !openedConfig {
		t.Fatal("o on settings must open the config")
	}

	// The refreshed snapshot text replaces the settings body.
	m = update(m, SettingsMsg{Text: "refreshed settings body"})
	if !strings.Contains(m.View(), "refreshed settings body") {
		t.Fatalf("settings body not refreshed:\n%s", m.View())
	}
}

func TestBoardReporterStatsSink(t *testing.T) {
	rep := NewBoardReporter(0, func(any) {})
	rep.TransferDone(2048, 3*time.Second)

	size, elapsed := rep.Stats()
	if size != 2048 || elapsed != 3*time.Second {
		t.Fatalf("Stats() = %d, %v", size, elapsed)
	}
}

func TestBoardSpeedSparkline(t *testing.T) {
	m := NewModel(Options{QueueURLs: []string{"https://x/a.mkv"}, Jobs: 1})
	live := newRow(0, "a.mkv", 10_000_000, 0)
	m = update(m, FileStartedMsg{ID: 0, Row: live})

	// First tick only baselines the counter.
	m = update(m, tickMsg{})
	if m.currentSpeed() != 0 {
		t.Fatalf("baseline tick must not report a speed, got %v", m.currentSpeed())
	}

	// Bytes moved between ticks produce a speed and a sparkline.
	live.Add(300_000) // 300 KB in one tick ≈ 2 MB/s
	m = update(m, tickMsg{})
	if m.currentSpeed() <= 0 {
		t.Fatalf("speed not sampled: %v", m.currentSpeed())
	}
	view := m.View()
	if !strings.Contains(view, "/s") {
		t.Fatalf("footer must show the aggregate speed:\n%s", view)
	}
	if !strings.ContainsAny(view, string(sparkRunes)) {
		t.Fatalf("footer must show a sparkline:\n%s", view)
	}

	// A row finishing (dropping out of the sum) must not produce a
	// negative speed.
	live.Status = RowSaved
	m = update(m, tickMsg{})
	if m.currentSpeed() < 0 {
		t.Fatalf("speed went negative: %v", m.currentSpeed())
	}
}

func TestBoardWarningsAreCappedAndShown(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 5; i++ {
		m = update(m, WarnMsg{Text: fmt.Sprintf("warning %d", i)})
	}
	view := m.View()
	if strings.Contains(view, "warning 0") || strings.Contains(view, "warning 1") {
		t.Errorf("old warnings should be dropped:\n%s", view)
	}
	if !strings.Contains(view, "warning 4") {
		t.Errorf("latest warning missing:\n%s", view)
	}
}

func TestBoardTabSwitching(t *testing.T) {
	m := newTestModel()

	m = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != TabCompleted {
		t.Fatalf("tab = %v, want completed", m.tab)
	}
	completedView := m.View()
	if !strings.Contains(completedView, "2026-10-11 10:01:22  FAB - 21.mkv") {
		t.Errorf("completed tab must show date/time and file name:\n%s", completedView)
	}
	if strings.Contains(completedView, "http") {
		t.Errorf("completed tab must never show URLs:\n%s", completedView)
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.tab != TabSettings {
		t.Fatalf("tab = %v, want settings", m.tab)
	}
	if !strings.Contains(m.View(), "settings body") {
		t.Errorf("settings tab must show the snapshot body:\n%s", m.View())
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != TabDownloads {
		t.Fatalf("tab should wrap around, got %v", m.tab)
	}
}

// TestBoardShowsWholeQueueUpFront is the M2.5 requirement: one page lists
// every queued file (names only, queue order); queue_jobs decides which of
// them are live.
func TestBoardShowsWholeQueueUpFront(t *testing.T) {
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/FAB%20-%2022.mkv",
			"https://x/FAB%20-%2023.mkv",
			"https://x/FAB%20-%2024.mkv",
		},
		Jobs: 2,
	})

	view := m.View()
	for _, want := range []string{"FAB - 22.mkv", "FAB - 23.mkv", "FAB - 24.mkv"} {
		if !strings.Contains(view, want) {
			t.Fatalf("%q must be visible before any transfer starts:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "waiting") {
		t.Fatalf("queued files must show as waiting:\n%s", view)
	}
	if strings.Contains(view, "http") {
		t.Fatalf("URLs must never be shown, only names:\n%s", view)
	}

	// The second queued file goes live; it keeps its position, the others
	// stay waiting and visible.
	live := newRow(1, "FAB%20-%2023.mkv", 1000, 0)
	live.Add(250)
	m = update(m, FileStartedMsg{ID: 1, Row: live})
	view = m.View()
	if !strings.Contains(view, "25.0%") {
		t.Fatalf("live row must show progress:\n%s", view)
	}
	if strings.Index(view, "FAB - 22.mkv") > strings.Index(view, "FAB - 23.mkv") {
		t.Errorf("queue order must be preserved:\n%s", view)
	}
	if !strings.Contains(view, "waiting") {
		t.Errorf("untouched files must remain waiting:\n%s", view)
	}
}

func TestBoardHeaderCounts(t *testing.T) {
	m := newTestModel()
	if m.opts.Jobs != 2 {
		t.Fatalf("fixture jobs = %d", m.opts.Jobs)
	}
	m = update(m, FileStartedMsg{ID: 0, Row: newRow(0, "x.mkv", 10, 0)})

	view := m.View()
	if !strings.Contains(view, "2 workers · 2 files") {
		t.Errorf("header should count queue files once, got:\n%s", view)
	}

	single := NewModel(Options{QueueURLs: []string{"a"}, Jobs: 1})
	if !strings.Contains(single.View(), "1 worker · 1 files") {
		t.Errorf("singular worker word missing:\n%s", single.View())
	}
}

func TestBoardQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		m := newTestModel()
		next, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("key %q must quit", key.String())
		}
		if _, ok := next.(Model); !ok {
			t.Fatalf("update must return a Model")
		}
	}
}

func TestBoardRowSpeedAndETA(t *testing.T) {
	row := newRow(0, "f.mkv", 200, 0)
	row.Start = time.Now().Add(-2 * time.Second) // 2s elapsed
	row.Add(100)

	line := renderRow(row, 26, 24, 0)
	if !strings.Contains(line, "/s") {
		t.Errorf("speed missing: %s", line)
	}
	if !strings.Contains(line, "ETA") {
		t.Errorf("ETA missing: %s", line)
	}

	// Unknown size renders without percentage but stays a valid line.
	unknown := newRow(0, "u.mkv", 0, 0)
	unknown.Add(10)
	if line := renderRow(unknown, 26, 24, 0); !strings.Contains(line, "u.mkv") {
		t.Errorf("unknown-size row broken: %s", line)
	}
}
