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
		Completed: []string{"2026-10-11 10:01:22 | https://example.com/FAB-21.mkv"},
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

	row := newRow("FAB%20-%2022.mkv", 1000, 0)
	m = update(m, FileStartedMsg{Row: row})
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

	saved := newRow("a.mkv", 10, 0)
	failed := newRow("b.mkv", 10, 0)
	m = update(m, FileStartedMsg{Row: saved})
	m = update(m, FileStartedMsg{Row: failed})

	m = update(m, FileDoneMsg{Row: saved, Text: "✓ saved → /tmp/a.mkv"})
	m = update(m, FileDoneMsg{Row: failed, Text: "✗ failed: timeout", Failed: true})

	if saved.Status != RowSaved || failed.Status != RowFailed {
		t.Fatalf("statuses not applied: saved=%v failed=%v", saved.Status, failed.Status)
	}

	m = update(m, RunFinishedMsg{})
	view := m.View()
	for _, want := range []string{"✓ saved → /tmp/a.mkv", "✗ failed: timeout", "1 saved, 1 failed"} {
		if !strings.Contains(view, want) {
			t.Errorf("%q missing from view:\n%s", want, view)
		}
	}
}

func TestBoardPreRowFailureBecomesStaticLine(t *testing.T) {
	m := newTestModel()
	m = update(m, FileDoneMsg{Text: "✗ failed: no route to host", Failed: true})

	view := m.View()
	if !strings.Contains(view, "✗ failed: no route to host") {
		t.Fatalf("static failure line missing:\n%s", view)
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
	if m.tab != TabQueue {
		t.Fatalf("tab = %v, want queue", m.tab)
	}
	if !strings.Contains(m.View(), "FAB - 22.mkv") {
		t.Errorf("queue tab must list pending files:\n%s", m.View())
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
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

func TestBoardHeaderCounts(t *testing.T) {
	m := newTestModel()
	if m.opts.Jobs != 2 {
		t.Fatalf("fixture jobs = %d", m.opts.Jobs)
	}
	m = update(m, FileStartedMsg{Row: newRow("x.mkv", 10, 0)})

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
	row := newRow("f.mkv", 200, 0)
	row.Start = time.Now().Add(-2 * time.Second) // 2s elapsed
	row.Add(100)

	line := renderRow(row)
	if !strings.Contains(line, "/s") {
		t.Errorf("speed missing: %s", line)
	}
	if !strings.Contains(line, "ETA") {
		t.Errorf("ETA missing: %s", line)
	}

	// Unknown size renders without percentage but stays a valid line.
	unknown := newRow("u.mkv", 0, 0)
	unknown.Add(10)
	if line := renderRow(unknown); !strings.Contains(line, "u.mkv") {
		t.Errorf("unknown-size row broken: %s", line)
	}
}
