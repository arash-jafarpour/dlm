package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	rowLabelWidth = 26
	rowBarWidth   = 24
)

// MultiBar renders a block of progress rows in place: one line per active
// file plus interleaved static lines (completions, warnings). It owns all
// cursor movement within the block and repaints it a few times per second;
// the rest of the program only appends lines through it.
//
// On a non-terminal stdout everything is inert: rows render nothing and
// Line falls back to a plain println, so cron/pipe output stays clean.
type MultiBar struct {
	mu        sync.Mutex
	out       *os.File
	term      bool
	slots     []rowCell
	printed   bool
	renderedN int
	ticker    *time.Ticker
	stop      chan struct{}
	stopOnce  sync.Once
}

type rowCell struct {
	static string // static text line when row == nil
	row    *Row
}

// Row is one live block line.
type Row struct {
	total   int64
	current atomic.Int64
	label   string
	start   time.Time
}

func NewMultiBar() *MultiBar {
	return &MultiBar{out: os.Stdout, term: isTerminal(os.Stdout)}
}

// RowHandle feeds one row; method-compatible with downloader.ProgressBar,
// so a row can be handed to the downloader as its progress sink.
type RowHandle interface {
	Add(n int)
	Done()
}

// AddRow registers a live row and returns the handle to feed it. A total
// of zero or negative (unknown size) renders without percentage. Rows are
// shown immediately; the repaint ticker starts on demand.
func (m *MultiBar) AddRow(total int64, label string, offset int64) RowHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.term {
		return rowHandle{} // silent mode
	}
	row := &Row{total: total, label: truncateRowLabel(label), start: time.Now()}
	row.current.Store(offset)
	m.slots = append(m.slots, rowCell{row: row})
	m.ensureTicker()
	m.renderLocked()
	return rowHandle{m: m, row: row}
}

// Line prints a permanent line below the block (completions, warnings),
// preserving the rows above it.
func (m *MultiBar) Line(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.term {
		fmt.Println(text)
		return
	}
	m.slots = append(m.slots, rowCell{static: text})
	m.renderLocked()
}

// FinishRow turns a live row into a permanent line. Falls back to Line
// when the row handle belongs to silent mode or has no row.
func (m *MultiBar) FinishRow(h RowHandle, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rh, ok := h.(rowHandle)
	if !ok || rh.row == nil {
		if text != "" {
			if m.term {
				m.slots = append(m.slots, rowCell{static: text})
				m.renderLocked()
			} else {
				fmt.Println(text)
			}
		}
		return
	}
	for i, cell := range m.slots {
		if cell.row == rh.row {
			m.slots[i] = rowCell{static: text}
			break
		}
	}
	if m.term {
		m.renderLocked()
	} else if text != "" {
		fmt.Println(text)
	}
}

// Finish stops the repaint ticker and parks the cursor on a fresh line so
// whatever prints next starts below the block.
func (m *MultiBar) Finish() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopTickerLocked()
	if m.term && m.printed {
		_, _ = fmt.Fprintln(m.out)
		m.printed = false
	}
}

func (m *MultiBar) stopTickerLocked() {
	m.stopOnce.Do(func() {
		if m.ticker != nil {
			m.ticker.Stop()
			close(m.stop)
		}
	})
}

func (m *MultiBar) ensureTicker() {
	if m.ticker != nil {
		return
	}
	m.ticker = time.NewTicker(120 * time.Millisecond)
	m.stop = make(chan struct{})
	go func() {
		for {
			select {
			case <-m.stop:
				return
			case <-m.ticker.C:
				m.render()
			}
		}
	}()
}

func (m *MultiBar) render() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renderLocked()
}

// renderLocked repaints the block. Cursor invariant: while the block is
// live, the cursor sits on the first free line below the last slot, and
// slots only ever GROW (new rows/completions append below); row replacement
// happens in place, so a `\x1b[<renderedN>A` rewind is always exact.
func (m *MultiBar) renderLocked() {
	n := len(m.slots)
	if !m.term || n == 0 {
		return
	}
	// Rewind by what the previous frame rendered: that many lines sit
	// between the park cursor and slot 0 (slots only grow, never shrink).
	if m.printed {
		_, _ = fmt.Fprintf(m.out, "\x1b[%dA", m.renderedN)
	}
	var b strings.Builder
	for _, cell := range m.slots {
		if cell.row != nil {
			b.WriteString(rowLine(cell.row))
		} else {
			b.WriteString(cell.static)
		}
		b.WriteString("\x1b[0K\n")
	}
	_, _ = fmt.Fprint(m.out, b.String())
	m.renderedN = n
	m.printed = true
}

// rowHandle feeds one row; safe for concurrent Add calls (chunks).
type rowHandle struct {
	m   *MultiBar
	row *Row
}

func (h rowHandle) Add(n int) {
	if h.row != nil {
		h.row.current.Add(int64(n))
	}
}

// Done freezes reporting for the row; the block slot stays until FinishRow
// replaces it with a permanent line.
func (h rowHandle) Done() {}

func rowLine(row *Row) string {
	current := row.current.Load()
	elapsed := time.Since(row.start).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(current) / elapsed
	}

	var bar string
	percent := 100.0
	if row.total > 0 {
		pct := float64(current) / float64(row.total)
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		percent = pct * 100
		filled := int(percent / 100 * float64(rowBarWidth))
		bar = Green(strings.Repeat("█", filled)) + Gray(strings.Repeat("░", rowBarWidth-filled))
	} else {
		bar = Gray(strings.Repeat("·", rowBarWidth))
	}

	var eta string
	if row.total > 0 && speed > 0 && current < row.total {
		if secs := float64(row.total-current) / speed; secs >= 1 {
			eta = "  ETA " + formatETA(secs)
		}
	}

	return fmt.Sprintf("%s %s %5.1f%%  %9s/%-9s %9s/s%s",
		Bold(row.label), bar, percent, formatBytes(current), formatBytes(row.total),
		formatBytes(int64(speed)), eta)
}

func truncateRowLabel(label string) string {
	if len(label) > rowLabelWidth {
		return label[:rowLabelWidth-1] + "…"
	}
	return label
}
