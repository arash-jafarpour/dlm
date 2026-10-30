package tui

import (
	"bytes"
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
		Completed: []HistoryEntry{{Text: "2026-10-11 10:01:22  FAB - 21.mkv", Ref: "raw-21"}},
		Settings: []Setting{
			{Key: "num_chunks", Value: "16", Kind: SettingInt, Help: "chunks"},
			{Key: "insecure_skip_verify", Value: "false", Kind: SettingBool, Help: "tls"},
		},
		Jobs: 2,
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
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 17}) // window of 8 rows

	view := m.View()
	if !strings.Contains(view, "file-00.mkv") || !strings.Contains(view, "file-07.mkv") {
		t.Fatalf("first window must show rows 0-7:\n%s", view)
	}
	if strings.Contains(view, "file-08.mkv") {
		t.Fatalf("row 8 must be outside the window:\n%s", view)
	}
	if !strings.Contains(view, "▼ 17 more below") {
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
	if m.selected != 11 || m.scrollOffset != 4 {
		t.Fatalf("selection=%d offset=%d, want 11/4", m.selected, m.scrollOffset)
	}
	if !strings.Contains(view, "▲ 4 more above") {
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
	if !strings.Contains(view, "▲ 17 more above") {
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
	if !strings.Contains(view, "file-02.mkv") || strings.Contains(view, "file-03.mkv") {
		t.Fatalf("height 12 should show 3 rows:\n%s", view)
	}
	if !strings.Contains(view, "▼ 22 more below") {
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

// TestBoardViewFitsTerminalHeight: whatever the tab, item count or scroll
// position, the rendered frame never grows past the terminal. bubbletea drops
// lines off the top of an oversized frame, which is what hid the header.
func TestBoardViewFitsTerminalHeight(t *testing.T) {
	for _, h := range []int{12, 17, 24, 40} {
		for _, n := range []int{0, 1, 5, 20, 50} {
			urls := make([]string, n)
			completed := make([]HistoryEntry, n)
			settings := make([]Setting, n)
			for i := 0; i < n; i++ {
				urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
				completed[i] = HistoryEntry{
					Text: fmt.Sprintf("2026-10-15 10:00:00  entry-%02d.mkv", i),
					Ref:  fmt.Sprintf("raw-%d", i),
				}
				settings[i] = Setting{Key: fmt.Sprintf("key_%02d", i), Value: "x", Kind: SettingText}
			}

			base := NewModel(Options{
				QueueURLs: urls,
				Completed: completed,
				Settings:  settings,
				Jobs:      2,
				Summary:   Summary{Count: n, Dir: "/downloads", Files: n},
			})
			base = update(base, tea.WindowSizeMsg{Width: 100, Height: h})

			for _, tab := range []Tab{TabDownloads, TabCompleted, TabSettings, TabSummary} {
				m := base
				m.tab = tab

				// Exercise a scrolled window too: select the last item so the
				// markers have to share the frame.
				for i := 0; i < n; i++ {
					m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
				}

				view := m.View()
				if lines := len(strings.Split(view, "\n")); lines != h {
					t.Errorf("height %d, %d items, tab %d: view is %d lines, want %d:\n%s", h, n, tab, lines, h, view)
				}
				if !strings.HasPrefix(stripANSI(view), "dlm") {
					t.Errorf("height %d, %d items, tab %d: header trimmed:\n%s", h, n, tab, view)
				}
			}
		}
	}
}

// TestBoardFooterPinsToBottom: whatever the content height, the footer is the
// window's last line. A short list must grow the body area, not float the
// status rows up the screen.
func TestBoardFooterPinsToBottom(t *testing.T) {
	for _, h := range []int{12, 30, 40} {
		for _, n := range []int{0, 1, 5} {
			urls := make([]string, n)
			for i := range urls {
				urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
			}
			base := NewModel(Options{
				QueueURLs: urls,
				Jobs:      1,
				Version:   "1.2.3",
			})
			base = update(base, tea.WindowSizeMsg{Width: 100, Height: h})

			for _, tab := range []Tab{TabDownloads, TabCompleted, TabSettings, TabSummary} {
				m := base
				m.tab = tab

				lines := strings.Split(m.View(), "\n")
				if len(lines) != h {
					t.Errorf("height %d, %d items, tab %d: view is %d lines, want %d:\n%s",
						h, n, tab, len(lines), h, m.View())
					continue
				}
				last := stripANSI(lines[len(lines)-1])
				if !strings.Contains(last, "tab switch") || !strings.Contains(last, "dlm 1.2.3") {
					t.Errorf("height %d, %d items, tab %d: footer is not the last line: %q",
						h, n, tab, last)
				}
			}
		}
	}
}

// TestBoardModalPinsToBottom: the confirm and input layers replace the
// status area but still end on the window's last line.
func TestBoardModalPinsToBottom(t *testing.T) {
	const height = 24
	m := NewModel(Options{QueueURLs: []string{"https://x/a.mkv"}, Jobs: 1})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: height})

	m.confirm = &confirmPrompt{Prompt: "Delete a.mkv?"}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != height {
		t.Fatalf("confirm view is %d lines, want %d", len(lines), height)
	}
	if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, "y/enter confirm") {
		t.Fatalf("confirm hint must be the last line: %q", last)
	}

	m.confirm = nil
	m.inputActive = true
	lines = strings.Split(m.View(), "\n")
	if len(lines) != height {
		t.Fatalf("input view is %d lines, want %d", len(lines), height)
	}
	if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, "enter to add") {
		t.Fatalf("input hint must be the last line: %q", last)
	}
}

// TestHelpFillsTerminalHeight: the help overlay has no footer but still owns
// the whole window rather than collapsing to its content.
func TestHelpFillsTerminalHeight(t *testing.T) {
	const height = 40
	m := NewModel(Options{QueueURLs: []string{"https://x/a.mkv"}, Jobs: 1})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: height})
	m.help = true

	lines := strings.Split(m.View(), "\n")
	if len(lines) != height {
		t.Fatalf("help view is %d lines, want %d", len(lines), height)
	}
	if !strings.Contains(stripANSI(lines[0]), "dlm — keys") {
		t.Fatalf("help title must stay on the first line: %q", lines[0])
	}
}

// TestBoardUpdateCheckStartsOnInit: the background release check is wired
// into the board's first command.
func TestBoardUpdateCheckStartsOnInit(t *testing.T) {
	called := false
	m := NewModel(Options{
		QueueURLs:     []string{"https://x/a.mkv"},
		OnCheckUpdate: func() tea.Cmd { called = true; return nil },
	})
	if m.Init() == nil {
		t.Fatal("Init must keep the repaint tick")
	}
	if !called {
		t.Fatal("Init must start the background update check")
	}
}

// TestBoardUpdateNoticeAndInstall: the offered release is advertised above
// the version, u asks first, and success turns into a restart reminder.
func TestBoardUpdateNoticeAndInstall(t *testing.T) {
	var installRequested bool
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv"},
		Jobs:      1,
		Version:   "0.6.0",
		OnInstallUpdate: func() tea.Cmd {
			installRequested = true
			return func() tea.Msg { return UpdateInstalledMsg{Version: "0.7.0"} }
		},
	})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = update(m, UpdateAvailableMsg{Version: "0.7.0"})

	view := stripANSI(m.View())
	if !strings.Contains(view, "v0.7.0 is available · u update") {
		t.Fatalf("update notice missing:\n%s", view)
	}
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 24 {
		t.Fatalf("view is %d lines, want 24", len(lines))
	}
	if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, "dlm 0.6.0") {
		t.Fatalf("footer must stay on the last line with the notice: %q", last)
	}

	// u asks before touching the binary.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.confirm == nil || !strings.Contains(m.confirm.Prompt, "v0.7.0") {
		t.Fatalf("u must ask for confirmation, got %+v", m.confirm)
	}
	if installRequested {
		t.Fatal("install must wait for the confirmation")
	}

	// Accepting starts the background install and reports it in the footer.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.confirm != nil {
		t.Fatal("confirmation must close on y")
	}
	if !m.updating || !installRequested {
		t.Fatal("y must start the install")
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "updating dlm to v0.7.0") {
		t.Fatalf("install progress missing:\n%s", view)
	}

	// The hook's command reports success; the notice becomes a restart
	// reminder and the old offer disappears.
	msg := m.opts.OnInstallUpdate()()
	m = update(m, msg)
	view = stripANSI(m.View())
	if !strings.Contains(view, "dlm v0.7.0 installed · restart dlm to use it") {
		t.Fatalf("restart prompt missing:\n%s", view)
	}
	if strings.Contains(view, "u update") {
		t.Fatalf("the old offer must clear after install:\n%s", view)
	}

	// u is inert once installed (nothing left to fetch).
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.confirm != nil {
		t.Fatal("u must not offer an install after the update succeeded")
	}
}

// TestBoardUpdateFailureWarnsAndKeepsOffer: a failed install leaves the old
// binary running, reports why, and still offers a retry.
func TestBoardUpdateFailureWarnsAndKeepsOffer(t *testing.T) {
	m := NewModel(Options{
		QueueURLs:       []string{"https://x/a.mkv"},
		Jobs:            1,
		Version:         "0.6.0",
		OnInstallUpdate: func() tea.Cmd { return func() tea.Msg { return nil } },
	})
	m = update(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m = update(m, UpdateAvailableMsg{Version: "0.7.0"})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	m = update(m, UpdateFailedMsg{Err: "network unreachable"})
	view := stripANSI(m.View())
	if m.updating {
		t.Fatal("a failed install must clear the running flag")
	}
	if !strings.Contains(view, "update failed: network unreachable") {
		t.Fatalf("failure warning missing:\n%s", view)
	}
	if !strings.Contains(view, "v0.7.0 is available · u update") {
		t.Fatalf("the offer must survive so u can retry:\n%s", view)
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) != 20 {
		t.Fatalf("view is %d lines, want 20", len(lines))
	}
}

// TestBoardUpdateKeyIgnoredWithoutOffer: u does nothing unless a release was
// actually offered by the background check.
func TestBoardUpdateKeyIgnoredWithoutOffer(t *testing.T) {
	m := NewModel(Options{QueueURLs: []string{"https://x/a.mkv"}, Jobs: 1})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.confirm != nil {
		t.Fatalf("u without an offered release must be inert, got %q", m.confirm.Prompt)
	}
}

// TestBoardUpdateNoticeFitsWindow: the extra notice line comes out of the
// body window, so a full list still fits and the footer stays last.
func TestBoardUpdateNoticeFitsWindow(t *testing.T) {
	const height = 12
	urls := make([]string, 50)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}
	m := NewModel(Options{QueueURLs: urls, Jobs: 2, Version: "0.6.0"})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: height})
	m = update(m, UpdateAvailableMsg{Version: "9.9.9"})

	lines := strings.Split(m.View(), "\n")
	if len(lines) != height {
		t.Fatalf("view is %d lines, want %d:\n%s", len(lines), height, m.View())
	}
	if notice := stripANSI(lines[height-2]); !strings.Contains(notice, "v9.9.9 is available") {
		t.Fatalf("notice must sit directly above the footer: %q", notice)
	}
	if last := stripANSI(lines[height-1]); !strings.Contains(last, "dlm 0.6.0") {
		t.Fatalf("footer lost the last line: %q", last)
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

func TestBoardEmptyQueueShowsPlaceholder(t *testing.T) {
	added := 0
	m := NewModel(Options{
		Jobs: 2,
		OnAdd: func(urls []string) []int {
			added++
			return []int{0}
		},
	})

	// An empty queue is a valid board state: placeholder, not a transfer.
	view := m.View()
	for _, want := range []string{"queue is empty", "press a to add a URL", "0 files"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty board must show %q:\n%s", want, view)
		}
	}

	// 'a' + a URL fills the board from the UI; the counts follow live.
	m = press(m, "a")
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://x/added.mkv")})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if added != 1 {
		t.Fatalf("OnAdd calls = %d, want 1", added)
	}
	view = m.View()
	if strings.Contains(view, "queue is empty") {
		t.Errorf("placeholder must disappear once a URL is queued:\n%s", view)
	}
	for _, want := range []string{"1 files", "waiting for you", "added.mkv"} {
		if !strings.Contains(view, want) {
			t.Errorf("added URL must render as a held row (%q missing):\n%s", want, view)
		}
	}
}

func TestBoardDeleteSelectedAsksFirst(t *testing.T) {
	var deleted int
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      1,
		OnDelete:  func(id int) { deleted = id },
	})

	m = press(m, "j", "d") // select b, ask to delete
	if m.confirm == nil {
		t.Fatal("delete must ask for confirmation first")
	}
	if deleted != 0 {
		t.Fatalf("nothing may be deleted before confirmation, got id %d", deleted)
	}

	// Cancelling leaves the file queued.
	m = press(m, "n")
	if deleted != 0 || len(m.rows) != 3 {
		t.Fatalf("cancel must not delete: deleted=%d rows=%d", deleted, len(m.rows))
	}

	// Confirming removes the row and clears the queue-file line.
	m = press(m, "d", "y")
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
		Settings:     []Setting{{Key: "num_chunks", Value: "8", Kind: SettingInt}},
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
	m = update(m, SettingsMsg{Settings: []Setting{{Key: "queue_jobs", Value: "4", Kind: SettingInt}}})
	if !strings.Contains(m.View(), "queue_jobs") {
		t.Fatalf("settings rows not refreshed:\n%s", m.View())
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

// boardHooks records which callbacks fire, for inertness/dialog tests.
type boardHooks struct {
	control   []ControlAction
	add       int
	del       int
	delHist   []string
	clearHist int
	setKVs    []string
	resets    []string
	resetAll  int
}

func newHookedModel(h *boardHooks) Model {
	return NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv"},
		Completed: []HistoryEntry{
			{Text: "2026-10-15 10:00:00  a.mkv", Ref: "raw-a"},
			{Text: "2026-10-15 09:00:00  b.mkv", Ref: "raw-b"},
		},
		Settings: []Setting{
			{Key: "num_chunks", Value: "16", Kind: SettingInt, Help: "chunks"},
			{Key: "queue_jobs", Value: "1", Kind: SettingInt, Help: "jobs"},
			{Key: "insecure_skip_verify", Value: "false", Kind: SettingBool, Help: "tls"},
			{Key: "output_dir", Value: "/tmp/out", Kind: SettingText, Help: "dir"},
		},
		Jobs: 2,
		OnControl: func(_ int, a ControlAction) {
			h.control = append(h.control, a)
		},
		OnAdd:    func([]string) []int { h.add++; return nil },
		OnDelete: func(int) { h.del++ },
		OnSettingSet: func(key, value string) error {
			h.setKVs = append(h.setKVs, key+"="+value)
			return nil
		},
		OnSettingReset: func(key string) (string, error) {
			h.resets = append(h.resets, key)
			return "DEFAULT", nil
		},
		OnSettingsResetAll: func() ([]Setting, error) {
			h.resetAll++
			return []Setting{{Key: "num_chunks", Value: "8", Kind: SettingInt}}, nil
		},
		OnDeleteCompleted: func(ref string) error {
			h.delHist = append(h.delHist, ref)
			return nil
		},
		OnClearCompleted: func() error { h.clearHist++; return nil },
	})
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m = update(m, msg)
	}
	return m
}

func TestBoardPerPageControlsAreScoped(t *testing.T) {
	// Completed page: download-only keys must be inert.
	h := &boardHooks{}
	_ = press(press(newHookedModel(h), "2"), "s", "x", "a", "J")
	if len(h.control) != 0 || h.add != 0 {
		t.Fatalf("download-only keys acted on the completed page: %+v", h)
	}
	// Settings page: same rule (space only toggles bools).
	h2 := &boardHooks{}
	_ = press(press(newHookedModel(h2), "3"), "s", "x", "a", "d")
	if len(h2.control) != 0 || h2.add != 0 || h2.del != 0 {
		t.Fatalf("download-only keys acted on the settings page: %+v", h2)
	}
	if len(h2.setKVs) != 0 {
		t.Fatalf("nothing should have been set yet: %v", h2.setKVs)
	}

	// Downloads page: downloads keys act (space starts the selected held file).
	h3 := &boardHooks{}
	_ = press(newHookedModel(h3), " ")
	if len(h3.control) != 1 || h3.control[0] != ControlStart {
		t.Fatalf("space on downloads must start the selected file: %+v", h3.control)
	}
}

func TestBoardConfirmDialogGatesDestructiveActions(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "3") // settings
	m = press(m, "r")                  // reset num_chunks
	if m.confirm == nil {
		t.Fatal("r must ask for confirmation first")
	}
	if !strings.Contains(m.View(), "Reset num_chunks") {
		t.Fatalf("dialog prompt missing:\n%s", m.View())
	}

	// Everything is swallowed while the question is open — including q.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd != nil {
		t.Fatal("q must not quit while a confirmation is open")
	}
	m = press(m, "s") // also inert
	if len(h.resets) != 0 || h.resetAll != 0 {
		t.Fatalf("no action may run before confirmation: %+v", h)
	}

	// Cancel leaves everything untouched.
	m = press(m, "n")
	if m.confirm != nil || len(h.resets) != 0 {
		t.Fatalf("cancel must not act: confirm=%v resets=%v", m.confirm, h.resets)
	}

	// Confirm runs the action and updates the row.
	m = press(m, "r", "y")
	if len(h.resets) != 1 || h.resets[0] != "num_chunks" {
		t.Fatalf("confirm must run the reset: %+v", h.resets)
	}
	if !strings.Contains(m.View(), "DEFAULT") {
		t.Fatalf("row value must be refreshed after reset:\n%s", m.View())
	}
}

func TestBoardCompletedPageDeleteAndClear(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "2") // completed

	m = press(m, "j") // second entry
	m = press(m, "d", "y")
	if len(h.delHist) != 1 || h.delHist[0] != "raw-b" {
		t.Fatalf("delete must target the selected entry: %+v", h.delHist)
	}
	if strings.Contains(m.View(), "b.mkv") {
		t.Fatalf("deleted entry must disappear:\n%s", m.View())
	}

	m = press(m, "c", "y")
	if h.clearHist != 1 {
		t.Fatalf("clear must run after confirmation: %+v", h)
	}
	if !strings.Contains(m.View(), "no completed downloads yet") {
		t.Fatalf("history must be empty:\n%s", m.View())
	}
}

func TestBoardCompletedTabShowsSummary(t *testing.T) {
	m := NewModel(Options{
		Completed: []HistoryEntry{
			{Text: "2026-10-15 10:00:00  a.mkv", Ref: "raw-a"},
		},
		Jobs: 1,
		Summary: Summary{
			Count: 65, Bytes: 24_500_000_000,
			Dir: "/downloads", Files: 79, DirBytes: 24_500_000_000,
		},
	})

	view := press(m, "2").View()
	for _, want := range []string{"65 downloads", "saved", "/downloads", "79 files"} {
		if !strings.Contains(view, want) {
			t.Errorf("completed roll-up missing %q:\n%s", want, view)
		}
	}
}

func TestBoardCompletedTabRefreshesSummary(t *testing.T) {
	calls := 0
	m := NewModel(Options{
		Completed: []HistoryEntry{{Text: "a", Ref: "a"}},
		Jobs:      1,
		OnSummary: func() tea.Cmd {
			calls++
			return func() tea.Msg {
				return SummaryMsg{Summary: Summary{Count: 7, Dir: "/out"}}
			}
		},
	})

	// Opening the completed tab requests fresh numbers.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = next.(Model)
	if calls != 1 || cmd == nil {
		t.Fatalf("opening completed must refresh the summary: calls=%d cmd=%v", calls, cmd)
	}
	m = update(m, cmd())
	if !strings.Contains(m.View(), "7 downloads") {
		t.Fatalf("summary message must update the roll-up:\n%s", m.View())
	}

	// A finished run refreshes it again while the tab is showing.
	_, cmd = m.Update(RunFinishedMsg{})
	if calls != 2 || cmd == nil {
		t.Fatalf("a finished run must refresh the summary: calls=%d cmd=%v", calls, cmd)
	}
}

func TestBoardSummaryTabShowsDashboard(t *testing.T) {
	calls := 0
	m := NewModel(Options{
		Jobs: 2,
		Summary: Summary{
			Count: 65, Bytes: 24_500_000_000,
			Dir: "/downloads", Files: 79, DirBytes: 24_500_000_000,
			Pending: 3, Settings: "chunks=8 retries=3 queue_jobs=1",
			Platform: "linux/amd64",
		},
		OnSummary: func() tea.Cmd {
			calls++
			return func() tea.Msg { return SummaryMsg{Summary: Summary{Pending: 4}} }
		},
	})

	// '4' opens the summary tab and asks for a refresh.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = next.(Model)
	if m.tab != TabSummary {
		t.Fatalf("tab = %v, want summary", m.tab)
	}
	if calls != 1 || cmd == nil {
		t.Fatalf("opening summary must refresh: calls=%d cmd=%v", calls, cmd)
	}

	view := m.View()
	for _, want := range []string{
		"queue", "3 pending", "65 downloads", "79 files",
		"chunks=8", "linux/amd64",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("summary tab missing %q:\n%s", want, view)
		}
	}

	// The refresh command replaces the numbers.
	m = update(m, cmd())
	if !strings.Contains(m.View(), "4 pending") {
		t.Fatalf("refresh must update the summary:\n%s", m.View())
	}

	// 'r' is the manual refresh.
	if _, rcmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); rcmd == nil {
		t.Fatal("r must refresh the summary tab")
	}
}

func TestBoardSettingsEditingAndValidation(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "3")

	// Enter opens the editor seeded with the current value.
	m = press(m, "enter")
	if !m.inputActive || m.input != "16" || m.inputKey != "num_chunks" {
		t.Fatalf("editor not seeded: active=%v input=%q key=%q", m.inputActive, m.input, m.inputKey)
	}
	m = press(m, "esc")
	if m.inputActive {
		t.Fatal("esc must cancel the edit")
	}

	// Typing a new value applies it through the hook.
	m = press(m, "enter")
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace}) // drop the "6"
	m = press(m, "8", "enter")
	if len(h.setKVs) != 1 || h.setKVs[0] != "num_chunks=18" {
		t.Fatalf("typed value not applied: %v", h.setKVs)
	}
	if !strings.Contains(m.View(), "18") {
		t.Fatalf("row value must update:\n%s", m.View())
	}

	// A refused value warns and leaves the row alone.
	h.setKVs = nil
	m = NewModel(Options{
		Settings: []Setting{{Key: "num_chunks", Value: "8", Kind: SettingInt}},
		OnSettingSet: func(string, string) error {
			return fmt.Errorf("num_chunks cannot exceed 16")
		},
	})
	m = press(m, "3", "enter")
	m = press(m, "99", "enter")
	if !strings.Contains(m.View(), "cannot exceed") {
		t.Fatalf("validation warning missing:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "8") {
		t.Fatalf("row must keep the old value:\n%s", m.View())
	}
}

func TestBoardSettingsBoolToggleAndResetAll(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "3")
	m = press(m, "j", "j") // insecure_skip_verify (third row)

	m = press(m, " ") // space toggles a bool
	if len(h.setKVs) != 1 || h.setKVs[0] != "insecure_skip_verify=true" {
		t.Fatalf("bool toggle not applied: %v", h.setKVs)
	}
	m = press(m, " ")
	if h.setKVs[1] != "insecure_skip_verify=false" {
		t.Fatalf("second toggle must flip back: %v", h.setKVs)
	}

	m = press(m, "R", "y")
	if h.resetAll != 1 {
		t.Fatalf("reset-all must run after confirmation: %+v", h)
	}
	if !strings.Contains(m.View(), "num_chunks") {
		t.Fatalf("reset rows must be applied:\n%s", m.View())
	}
}

func TestBoardInputBackspaceIsRuneSafe(t *testing.T) {
	m := NewModel(Options{QueueURLs: []string{"https://x/a.mkv"}, Jobs: 1})
	m = press(m, "a")
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("☂ü")})
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.input != "☂" {
		t.Fatalf("backspace must remove one rune, got %q", m.input)
	}
}

func TestBoardSettingsWarnings(t *testing.T) {
	m := press(newTestModel(), "3")
	view := m.View()
	for _, want := range []string{
		"restart dlm after a change",
		"changing num_chunks restarts partial downloads",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("settings page missing warning %q:\n%s", want, view)
		}
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

func TestBoardFooterShowsVersion(t *testing.T) {
	m := NewModel(Options{Jobs: 1, Version: "v0.5.0"})
	if !strings.Contains(m.View(), "dlm v0.5.0") {
		t.Fatalf("footer must show the version:\n%s", m.View())
	}

	// No version configured: nothing extra is rendered.
	if strings.Contains(NewModel(Options{Jobs: 1}).View(), "dlm v") {
		t.Fatalf("an empty version must not render:\n%s", NewModel(Options{Jobs: 1}).View())
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
	if !strings.Contains(m.View(), "num_chunks") {
		t.Errorf("settings tab must list config keys:\n%s", m.View())
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != TabSummary {
		t.Fatalf("tab = %v, want summary", m.tab)
	}
	if !strings.Contains(m.View(), "platform") {
		t.Errorf("summary tab must render its fields:\n%s", m.View())
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

// ---------------------------------------------------------------------------
// '/' filter
// ---------------------------------------------------------------------------

func newFilterModel() Model {
	return NewModel(Options{
		QueueURLs: []string{
			"https://x/alpha-1.mkv",
			"https://x/beta.mkv",
			"https://x/alpha-2.mkv",
			"https://x/gamma.zip",
			"https://x/alpha-3.mkv",
		},
		Jobs: 2,
	})
}

func TestBoardFilterNarrowsDownloads(t *testing.T) {
	m := press(newFilterModel(), "/", "a", "l", "p", "h", "a", "enter")

	view := m.View()
	for _, want := range []string{"alpha-1.mkv", "alpha-2.mkv", "alpha-3.mkv", `filter "alpha" · 3 of 5`} {
		if !strings.Contains(view, want) {
			t.Fatalf("%q missing from filtered view:\n%s", want, view)
		}
	}
	for _, hidden := range []string{"beta.mkv", "gamma.zip"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("filtered-out row %q still rendered:\n%s", hidden, view)
		}
	}

	// j/k cycle only through matches: 0 -> 2 -> 4 -> wrap.
	for _, want := range []int{2, 4, 0} {
		m = press(m, "j")
		if m.selected != want {
			t.Fatalf("selection = %d, want %d (matches only)", m.selected, want)
		}
	}
	m = press(m, "k")
	if m.selected != 4 {
		t.Fatalf("k must wrap to the last match, got %d", m.selected)
	}
}

func TestBoardFilterEnterKeepsEscClearsThenQuits(t *testing.T) {
	m := press(newFilterModel(), "/", "b", "e", "t", "a")
	if f := m.filters[TabDownloads]; !f.open || f.query != "beta" {
		t.Fatalf("typing must narrow live: %+v", f)
	}

	m = press(m, "enter")
	if f := m.filters[TabDownloads]; f.open || f.query != "beta" {
		t.Fatalf("enter keeps the query and closes the prompt: %+v", f)
	}
	if !strings.Contains(m.View(), `filter "beta" · 1 of 5`) {
		t.Fatalf("applied filter missing from status:\n%s", m.View())
	}

	// esc clears the filter instead of quitting; the next esc quits.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("esc must clear the filter before it quits")
	}
	if f := m.filters[TabDownloads]; f.query != "" {
		t.Fatalf("esc must clear the query: %+v", f)
	}
	if strings.Contains(m.View(), `filter "`) {
		t.Fatalf("status must drop the cleared filter:\n%s", m.View())
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Fatal("esc with no filter must quit")
	}
}

func TestBoardFilterPromptCapturesBoardKeys(t *testing.T) {
	m := press(newFilterModel(), "/", "q")
	if err := m.filters[TabDownloads].query; err != "q" {
		t.Fatalf("q must be typed into the query, got %q", err)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd != nil {
		t.Fatal("q must not quit while the filter prompt is open")
	}

	// esc inside the prompt clears and closes it.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if f := m.filters[TabDownloads]; f.open || f.query != "" {
		t.Fatalf("esc must clear and close the prompt: %+v", f)
	}
}

func TestBoardFilterReorderSkipsHiddenRows(t *testing.T) {
	var order []int
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/keep-1.mkv",
			"https://x/x-1.mkv",
			"https://x/x-2.mkv",
			"https://x/x-3.mkv",
			"https://x/keep-2.mkv",
		},
		Jobs:      2,
		OnReorder: func(ids []int) { order = append([]int(nil), ids...) },
	})

	// Filter to rows 0 and 4; J swaps the selected row with the next
	// *visible* one, which is row 4, not row 1.
	m = press(m, "/", "k", "e", "e", "p", "enter", "J")
	want := []int{4, 1, 2, 3, 0}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("reorder ids = %v, want %v", order, want)
	}
	if m.selected != 4 {
		t.Fatalf("selection = %d, want 4", m.selected)
	}
	// K walks back to the other match.
	if m = press(m, "K"); m.selected != 0 {
		t.Fatalf("K must return to the other match, got %d", m.selected)
	}
}

func TestBoardFilterControlTargetsVisibleRow(t *testing.T) {
	var gotID int
	var gotAction ControlAction
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      1,
		OnControl: func(id int, a ControlAction) { gotID, gotAction = id, a },
	})

	m = press(m, "/", "b", "enter", " ")
	if gotID != 1 || gotAction != ControlStart {
		t.Fatalf("space hit id=%d action=%v, want start on job 1", gotID, gotAction)
	}
}

func TestBoardFilterNoMatchMessageAndInertKeys(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "/", "z", "z", "z", "enter")

	view := m.View()
	if !strings.Contains(view, `no files match "zzz"`) {
		t.Fatalf("no-match message missing:\n%s", view)
	}
	if !strings.Contains(view, "esc clears the filter") {
		t.Fatalf("no-match hint missing:\n%s", view)
	}

	// Nothing visible means no control and no delete, even with keys pressed.
	m = press(m, " ", "r", "x", "d")
	if len(h.control) != 0 || h.del != 0 {
		t.Fatalf("keys must be inert with no matches: %+v", h)
	}
	m = press(m, "J")
	if m.selected != 0 {
		t.Fatalf("reorder must be inert with no matches, selection=%d", m.selected)
	}
}

func TestBoardFilterCompletedTab(t *testing.T) {
	h := &boardHooks{}
	m := press(newHookedModel(h), "2", "/", "b", ".", "m", "k", "v", "enter")

	view := m.View()
	if !strings.Contains(view, "b.mkv") || strings.Contains(view, "a.mkv") {
		t.Fatalf("completed filter did not narrow the history:\n%s", view)
	}
	if !strings.Contains(view, `filter "b.mkv" · 1 of 2`) {
		t.Fatalf("completed filter note missing:\n%s", view)
	}

	m = press(m, "d", "y")
	if len(h.delHist) != 1 || h.delHist[0] != "raw-b" {
		t.Fatalf("delete must target the filtered entry: %+v", h.delHist)
	}
	if !strings.Contains(m.View(), `no entries match "b.mkv"`) {
		t.Fatalf("empty filtered history must say so:\n%s", m.View())
	}

	m = press(m, "esc")
	if f := m.filters[TabCompleted]; f.query != "" {
		t.Fatalf("esc must clear the completed filter: %+v", f)
	}
	if !strings.Contains(m.View(), "a.mkv") {
		t.Fatalf("clearing the filter must bring the history back:\n%s", m.View())
	}
}

func TestBoardFilterWindowCountsVisibleRows(t *testing.T) {
	urls := make([]string, 24)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}
	// Keep the 12 even-numbered files; the odd ones are filtered out.
	for i := 0; i < 24; i += 2 {
		urls[i] = strings.Replace(urls[i], "file-", "keep-", 1)
	}
	m := press(NewModel(Options{QueueURLs: urls, Jobs: 2}), "/", "k", "e", "e", "p", "enter")
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 17}) // window of 8

	view := m.View()
	if !strings.Contains(view, "▼ 4 more below") {
		t.Fatalf("bottom marker must count matches (12 of 24):\n%s", view)
	}
	if strings.Contains(view, "file-00.mkv") {
		t.Fatalf("non-matching row rendered:\n%s", view)
	}

	for i := 0; i < 11; i++ {
		m = press(m, "j")
	}
	if m.selected != 22 || m.scrollOffset != 4 {
		t.Fatalf("selection=%d offset=%d, want 22/4", m.selected, m.scrollOffset)
	}
	if !strings.Contains(m.View(), "▲ 4 more above") {
		t.Fatalf("top marker must count matches above:\n%s", m.View())
	}
}

// TestBoardFilteredViewKeepsFrameHeight: a filter shrinks the visible list but
// must not change the chrome: the frame stays exactly one terminal tall, with
// the header on top and the footer on the last line.
func TestBoardFilteredViewKeepsFrameHeight(t *testing.T) {
	for _, h := range []int{12, 17, 24} {
		urls := make([]string, 40)
		completed := make([]HistoryEntry, 40)
		for i := range urls {
			urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
			completed[i] = HistoryEntry{
				Text: fmt.Sprintf("2026-10-15 10:00:00  entry-%02d.mkv", i),
				Ref:  fmt.Sprintf("raw-%d", i),
			}
		}
		base := NewModel(Options{
			QueueURLs: urls,
			Completed: completed,
			Jobs:      2,
			Version:   "0.7.0",
		})
		base = update(base, tea.WindowSizeMsg{Width: 100, Height: h})

		for _, tab := range []Tab{TabDownloads, TabCompleted} {
			for _, q := range []string{"file-0", "zzz"} {
				m := press(base, fmt.Sprint(tab+1), "/")
				for _, r := range q {
					m = press(m, string(r))
				}
				m = press(m, "enter")

				view := m.View()
				lines := strings.Split(view, "\n")
				if len(lines) != h {
					t.Errorf("height %d, tab %d, query %q: view is %d lines, want %d:\n%s",
						h, tab, q, len(lines), h, view)
				}
				if !strings.HasPrefix(stripANSI(view), "dlm") {
					t.Errorf("height %d, tab %d, query %q: header trimmed:\n%s", h, tab, q, view)
				}
				if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, "q quit") {
					t.Errorf("height %d, tab %d, query %q: footer not last: %q", h, tab, q, last)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// R (retry all failed) and g/G (move to top/bottom)
// ---------------------------------------------------------------------------

func TestBoardRetryAllFailed(t *testing.T) {
	var ids []int
	var actions []ControlAction
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv", "https://x/d.mkv",
		},
		Jobs: 2,
		OnControl: func(id int, a ControlAction) {
			ids = append(ids, id)
			actions = append(actions, a)
		},
	})
	m = update(m, JobStateMsg{ID: 1, State: RowFailed, Detail: "timeout"})
	m = update(m, JobStateMsg{ID: 3, State: RowFailed, Detail: "reset"})
	m = update(m, JobStateMsg{ID: 2, State: RowPaused})

	// R retries both failures, not the paused file, whatever is selected.
	m = press(m, "j", "R")
	if fmt.Sprint(ids) != "[1 3]" {
		t.Fatalf("retried ids = %v, want [1 3]", ids)
	}
	for _, a := range actions {
		if a != ControlRetry {
			t.Fatalf("action = %v, want ControlRetry", a)
		}
	}
}

func TestBoardRetryAllFailedIgnoresFilter(t *testing.T) {
	var ids []int
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/alpha.mkv", "https://x/beta.mkv", "https://x/gamma.mkv",
		},
		Jobs:      1,
		OnControl: func(id int, _ ControlAction) { ids = append(ids, id) },
	})
	m = update(m, JobStateMsg{ID: 0, State: RowFailed})
	m = update(m, JobStateMsg{ID: 2, State: RowFailed})

	// Only alpha is visible under this filter; R is still "retry all".
	m = press(m, "/", "a", "l", "p", "h", "a", "enter", "R")
	if fmt.Sprint(ids) != "[0 2]" {
		t.Fatalf("retried ids = %v, want [0 2]", ids)
	}
}

func TestBoardRetryAllNoFailuresIsInert(t *testing.T) {
	h := &boardHooks{}
	_ = press(newHookedModel(h), "R")
	if len(h.control) != 0 {
		t.Fatalf("no failures must not issue controls: %+v", h.control)
	}
	// Page-scoped: other tabs ignore R entirely.
	h2 := &boardHooks{}
	_ = press(newHookedModel(h2), "2", "R")
	if len(h2.control) != 0 {
		t.Fatalf("R must be downloads-only: %+v", h2.control)
	}
}

func TestBoardMoveSelectedToEdge(t *testing.T) {
	var order []int
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv",
			"https://x/d.mkv", "https://x/e.mkv",
		},
		Jobs:      2,
		OnReorder: func(ids []int) { order = append([]int(nil), ids...) },
	})

	// Selection starts at 0; move c to the bottom, then to the top.
	m = press(m, "j", "j", "G")
	if fmt.Sprint(order) != "[0 1 3 4 2]" {
		t.Fatalf("bottom move = %v, want [0 1 3 4 2]", order)
	}
	if m.selected != 4 {
		t.Fatalf("selection = %d, want 4", m.selected)
	}

	m = press(m, "g")
	if fmt.Sprint(order) != "[2 0 1 3 4]" {
		t.Fatalf("top move = %v, want [2 0 1 3 4]", order)
	}
	if m.selected != 0 {
		t.Fatalf("selection = %d, want 0", m.selected)
	}

	// At the edge already: no redundant rewrite.
	before := len(order)
	_ = press(m, "g")
	if len(order) != before {
		t.Fatalf("g at the top must not rewrite the queue: %v", order)
	}
}

func TestBoardMoveSelectedToEdgeCtrlArrows(t *testing.T) {
	var order []int
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      1,
		OnReorder: func(ids []int) { order = append([]int(nil), ids...) },
	})

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlDown})
	if fmt.Sprint(order) != "[1 2 0]" {
		t.Fatalf("ctrl+down = %v, want [1 2 0]", order)
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlUp})
	if fmt.Sprint(order) != "[0 1 2]" {
		t.Fatalf("ctrl+up = %v, want [0 1 2]", order)
	}
}

func TestBoardMoveSelectedToEdgeSkipsHiddenRows(t *testing.T) {
	var order []int
	m := NewModel(Options{
		QueueURLs: []string{
			"https://x/keep-1.mkv",
			"https://x/x-1.mkv",
			"https://x/x-2.mkv",
			"https://x/keep-2.mkv",
			"https://x/x-3.mkv",
		},
		Jobs:      2,
		OnReorder: func(ids []int) { order = append([]int(nil), ids...) },
	})

	// Filter to keep-1 (0) and keep-2 (3); G puts keep-1 after keep-2,
	// leaving the hidden rows' order untouched.
	m = press(m, "/", "k", "e", "e", "p", "enter", "G")
	if fmt.Sprint(order) != "[1 2 3 0 4]" {
		t.Fatalf("filtered bottom move = %v, want [1 2 3 0 4]", order)
	}
	if m.selected != 3 {
		t.Fatalf("selection = %d, want 3", m.selected)
	}

	// g brings it back before the first visible row.
	m = press(m, "g")
	if fmt.Sprint(order) != "[1 2 0 3 4]" {
		t.Fatalf("filtered top move = %v, want [1 2 0 3 4]", order)
	}
	if m.selected != 2 {
		t.Fatalf("selection = %d, want 2", m.selected)
	}
}

// TestBoardHintsWrapWithoutClipping: a narrow terminal wraps the key hints
// instead of clipping keys off the right edge, and the frame keeps exactly
// one terminal's height.
func TestBoardHintsWrapWithoutClipping(t *testing.T) {
	for _, w := range []int{64, 80, 100} {
		for _, h := range []int{12, 16, 24} {
			m := NewModel(Options{
				QueueURLs: []string{"https://x/a.mkv"},
				Jobs:      1,
				Version:   "0.7.0",
			})
			m = update(m, tea.WindowSizeMsg{Width: w, Height: h})
			view := m.View()
			plain := stripANSI(view)

			for _, want := range []string{"/ filter", "d delete", "J/K g/G move"} {
				if !strings.Contains(plain, want) {
					t.Errorf("width %d height %d: hint %q missing or clipped:\n%s", w, h, want, plain)
				}
			}
			if lines := len(strings.Split(view, "\n")); lines != h {
				t.Errorf("width %d height %d: view is %d lines, want %d:\n%s", w, h, lines, h, plain)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Mouse
// ---------------------------------------------------------------------------

func mouseClick(y, x int) tea.Msg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func mouseWheel(up bool) tea.Msg {
	if up {
		return tea.MouseMsg{X: 1, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}
	}
	return tea.MouseMsg{X: 1, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
}

// lineWith finds the screen line holding needle (plain text) and its column.
func lineWith(t *testing.T, m Model, needle string) (y, x int) {
	t.Helper()
	for y, line := range strings.Split(stripANSI(m.View()), "\n") {
		if i := strings.Index(line, needle); i >= 0 {
			return y, i
		}
	}
	t.Fatalf("%q is not on screen:\n%s", needle, m.View())
	return 0, 0
}

func TestBoardMouseClickSelectsRow(t *testing.T) {
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      2,
	})

	y, x := lineWith(t, m, "c.mkv")
	m = update(m, mouseClick(y, x))
	if m.selected != 2 {
		t.Fatalf("click on c.mkv selected %d, want 2", m.selected)
	}

	// Clicking blank space under the list leaves the cursor alone.
	_, last := lineWith(t, m, "tab switch")
	m = update(m, mouseClick(last, 1))
	if m.selected != 2 {
		t.Fatalf("a click on the footer must not select, got %d", m.selected)
	}
}

func TestBoardMouseClickTabSwitches(t *testing.T) {
	m := newTestModel()
	y, x := lineWith(t, m, "completed")
	m = update(m, mouseClick(y, x))
	if m.tab != TabCompleted {
		t.Fatalf("clicking completed switched to %v", m.tab)
	}

	y, x = lineWith(t, m, "settings")
	m = update(m, mouseClick(y, x))
	if m.tab != TabSettings {
		t.Fatalf("clicking settings switched to %v", m.tab)
	}

	// A click on the strip's separators is not a tab.
	strip := strings.Split(stripANSI(m.View()), "\n")[1]
	m = update(m, mouseClick(1, strings.Index(strip, "·")))
	if m.tab != TabSettings {
		t.Fatalf("separator click switched to %v", m.tab)
	}
}

func TestBoardMouseWheelScrollsCurrentList(t *testing.T) {
	urls := make([]string, 20)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}
	m := NewModel(Options{QueueURLs: urls, Jobs: 2})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 17}) // window of 8

	m = update(m, mouseWheel(false))
	if m.selected != 3 {
		t.Fatalf("one wheel notch = %d, want 3", m.selected)
	}
	for i := 0; i < 2; i++ {
		m = update(m, mouseWheel(false))
	}
	if m.selected != 9 {
		t.Fatalf("four notches = %d, want 9", m.selected)
	}
	if want := 9 - m.windowSize() + 1; m.scrollOffset != want {
		t.Fatalf("the window must follow the wheel, offset = %d, want %d",
			m.scrollOffset, want)
	}

	// Scrolling up clamps at the top instead of wrapping to the end.
	for i := 0; i < 5; i++ {
		m = update(m, mouseWheel(true))
	}
	if m.selected != 0 || m.scrollOffset != 0 {
		t.Fatalf("wheel up must clamp at the top, got selection=%d offset=%d",
			m.selected, m.scrollOffset)
	}

	// Down at the bottom also clamps.
	for i := 0; i < 10; i++ {
		m = update(m, mouseWheel(false))
	}
	if m.selected != len(urls)-1 {
		t.Fatalf("wheel down must stop at the last row, got %d", m.selected)
	}
}

func TestBoardMouseClickIgnoresMarkersAndHeaders(t *testing.T) {
	urls := make([]string, 20)
	completed := make([]HistoryEntry, 5)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/file-%02d.mkv", i)
	}
	for i := range completed {
		completed[i] = HistoryEntry{
			Text: fmt.Sprintf("2026-10-15 10:00:00  entry-%02d.mkv", i),
			Ref:  fmt.Sprintf("raw-%d", i),
		}
	}
	m := NewModel(Options{
		QueueURLs: urls,
		Completed: completed,
		Jobs:      2,
		Summary:   Summary{Count: 5, Dir: "/downloads"},
	})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 17})

	// Scroll the queue so the "▲ more above" marker sits on the first body
	// line; clicking it must not select anything.
	m = press(m, "j", "j", "j", "j", "j", "j", "j", "j", "j", "j")
	before := m.selected
	y, x := lineWith(t, m, "▲")
	m = update(m, mouseClick(y, x))
	if m.selected != before {
		t.Fatalf("clicking the scroll marker moved the selection to %d, want %d",
			m.selected, before)
	}

	// The completed roll-up header is not a row either.
	m = press(m, "2")
	y, x = lineWith(t, m, "5 downloads")
	m = update(m, mouseClick(y, x))
	if m.selCompleted != 0 {
		t.Fatalf("clicking the roll-up header selected %d", m.selCompleted)
	}
	// The output footer under the list is not a row either.
	y, x = lineWith(t, m, "output  /downloads")
	m = update(m, mouseClick(y, x))
	if m.selCompleted != 0 {
		t.Fatalf("clicking the output footer selected %d", m.selCompleted)
	}
}

func TestBoardMouseClickCompletedRow(t *testing.T) {
	m := newTestModel()
	m = press(m, "2")
	y, x := lineWith(t, m, "FAB - 21.mkv")
	m = update(m, mouseClick(y, x))
	if m.selCompleted != 0 {
		t.Fatalf("completed row click selected %d, want 0", m.selCompleted)
	}

	// With a filter the visible row maps back to its absolute index.
	m = press(m, "2", "/", "F", "A", "B", "enter")
	if vis := m.visibleHistory(); len(vis) != 1 {
		t.Fatalf("filter should leave one entry, got %v", vis)
	}
	y, x = lineWith(t, m, "FAB - 21.mkv")
	m = update(m, mouseClick(y, x))
	if m.selCompleted != 0 {
		t.Fatalf("filtered click selected %d, want 0", m.selCompleted)
	}
}

func TestBoardMouseClickSettingsRow(t *testing.T) {
	m := newTestModel()
	m = press(m, "3")
	y, x := lineWith(t, m, "insecure_skip_verify")
	m = update(m, mouseClick(y, x))
	if m.tab != TabSettings || m.selSettings != 1 {
		t.Fatalf("settings click: tab=%v selection=%d, want settings/1", m.tab, m.selSettings)
	}
}

func TestBoardMouseIgnoredWhilePromptOrHelp(t *testing.T) {
	h := &boardHooks{}
	m := newHookedModel(h)
	// A confirmation swallows mouse clicks, like keys.
	m = press(m, "d")
	if m.confirm == nil {
		t.Fatal("d must open a confirmation")
	}
	y, x := lineWith(t, m, "b.mkv")
	m = update(m, mouseClick(y, x))
	if m.selected != 0 {
		t.Fatalf("click while confirming moved the selection to %d", m.selected)
	}
	m = press(m, "n")

	// The help overlay does too.
	m = press(m, "n", "?")
	y, x = lineWith(t, m, "completed")
	m = update(m, mouseClick(y, x))
	if m.tab != TabDownloads {
		t.Fatalf("click while help is open switched to %v", m.tab)
	}
}

// ---------------------------------------------------------------------------
// O (open the downloads folder)
// ---------------------------------------------------------------------------

func TestBoardOpenFolderKey(t *testing.T) {
	hookCalls := 0
	opts := Options{
		QueueURLs: []string{"https://x/a.mkv"},
		Completed: []HistoryEntry{{Text: "2026-10-15 10:00:00  a.mkv", Ref: "raw-a"}},
		Jobs:      1,
		OnOpenDir: func() tea.Cmd {
			hookCalls++
			return func() tea.Msg { return WarnMsg{Text: "boom"} }
		},
	}
	m := NewModel(opts)

	// Downloads: O forwards the hook's command untouched.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	m = next.(Model)
	if hookCalls != 1 || cmd == nil {
		t.Fatalf("O must call the hook once and return its command (calls=%d cmd=%v)", hookCalls, cmd)
	}
	if msg := cmd(); msg == nil || msg.(WarnMsg).Text != "boom" {
		t.Fatalf("the hook's warning must reach the board, got %#v", msg)
	}

	// Completed: the same key opens the folder that holds the finished file.
	m = press(m, "2", "O")
	if hookCalls != 2 {
		t.Fatalf("O on the completed page must call the hook, calls=%d", hookCalls)
	}

	// Other pages have no folder action.
	m = press(m, "3", "O", "4", "O")
	if hookCalls != 2 {
		t.Fatalf("settings/summary must ignore O, calls=%d", hookCalls)
	}
}

func TestBoardOpenFolderWithoutHookIsInert(t *testing.T) {
	m := newTestModel() // no OnOpenDir
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}}); cmd != nil {
		t.Fatal("O without a hook must not return a command")
	}
}

// ---------------------------------------------------------------------------
// Terminal bell
// ---------------------------------------------------------------------------

func captureBell(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := bellOut
	bellOut = buf
	t.Cleanup(func() { bellOut = old })
	return buf
}

// runCmd executes a command and flattens a tea.Batch.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}
}

func TestBoardBellsWhenRunFinishes(t *testing.T) {
	buf := captureBell(t)
	m := newTestModel()

	row := newRow(0, "a.mkv", 10, 0)
	m = update(m, FileStartedMsg{ID: 0, Row: row})
	m = update(m, FileDoneMsg{Row: row, Text: "✓ saved → /tmp/a.mkv"})

	_, cmd := m.Update(RunFinishedMsg{})
	if cmd == nil {
		t.Fatal("a settled run must return the bell command")
	}
	runCmd(cmd)
	if got := buf.String(); got != "\a" {
		t.Fatalf("bell bytes = %q, want one BEL", got)
	}
}

func TestBoardBellsWhenRunFinishesWithFailures(t *testing.T) {
	buf := captureBell(t)
	m := newTestModel()
	m = update(m, JobStateMsg{ID: 0, State: RowFailed, Detail: "timeout"})

	_, cmd := m.Update(RunFinishedMsg{})
	runCmd(cmd)
	if got := buf.String(); got != "\a" {
		t.Fatalf("a failed run must still ring once, got %q", got)
	}
}

func TestBoardNoBellWithoutASettledRun(t *testing.T) {
	buf := captureBell(t)
	m := newTestModel() // every row is still held

	_, cmd := m.Update(RunFinishedMsg{})
	if cmd != nil {
		t.Fatalf("nothing settled: no command expected, got %v", cmd)
	}
	if buf.Len() != 0 {
		t.Fatalf("nothing settled: no bell expected, got %q", buf.String())
	}
}

// TestBoardMouseClickTogglesDownload: a click on a downloads row is the
// mouse's space — start a held file, stop a running one, resume, retry — and
// a click on a saved row does nothing.
func TestBoardMouseClickTogglesDownload(t *testing.T) {
	var ids []int
	var actions []ControlAction
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv", "https://x/b.mkv", "https://x/c.mkv"},
		Jobs:      2,
		OnControl: func(id int, a ControlAction) {
			ids = append(ids, id)
			actions = append(actions, a)
		},
	})

	y, x := lineWith(t, m, "b.mkv")
	click := func() {
		m = update(m, mouseClick(y, x))
	}

	// Held → start.
	click()
	if m.selected != 1 || fmt.Sprint(ids) != "[1]" || actions[0] != ControlStart {
		t.Fatalf("held click: selected=%d ids=%v actions=%v", m.selected, ids, actions)
	}

	// Running → stop (the same click again).
	m = update(m, JobStateMsg{ID: 1, State: RowRunning})
	click()
	if len(actions) != 2 || actions[1] != ControlPause || ids[1] != 1 {
		t.Fatalf("running click must pause: ids=%v actions=%v", ids, actions)
	}

	// Paused → resume.
	m = update(m, JobStateMsg{ID: 1, State: RowPaused})
	click()
	if len(actions) != 3 || actions[2] != ControlResume {
		t.Fatalf("paused click must resume: %v", actions)
	}

	// Failed → retry.
	m = update(m, JobStateMsg{ID: 1, State: RowFailed, Detail: "timeout"})
	click()
	if len(actions) != 4 || actions[3] != ControlRetry {
		t.Fatalf("failed click must retry: %v", actions)
	}

	// Saved → nothing to start or stop.
	m = update(m, JobStateMsg{ID: 1, State: RowSaved})
	click()
	if len(actions) != 4 {
		t.Fatalf("a saved row must not issue a control: %v", actions)
	}
	if m.selected != 1 {
		t.Fatalf("a saved row click still selects it, got %d", m.selected)
	}
}

func TestBoardMouseClickOnCompletedDoesNotToggle(t *testing.T) {
	var controls int
	m := NewModel(Options{
		QueueURLs: []string{"https://x/a.mkv"},
		Completed: []HistoryEntry{{Text: "2026-10-15 10:00:00  a.mkv", Ref: "raw-a"}},
		Jobs:      1,
		OnControl: func(int, ControlAction) { controls++ },
	})
	m = press(m, "2")
	y, x := lineWith(t, m, "a.mkv")
	m = update(m, mouseClick(y, x))
	if controls != 0 {
		t.Fatalf("completed clicks must not start or stop anything: %d controls", controls)
	}
}
