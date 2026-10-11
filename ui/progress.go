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
	maxLabelWidth = 64
	barWidth      = 40
)

type Bar struct {
	total     int64
	current   atomic.Int64
	width     int
	label     string
	startTime time.Time
	mu        sync.Mutex
	done      chan struct{}
	// term records whether stdout is a real terminal. The bar only
	// redraws (\r-based updates) when it can; on pipes, redirects, and
	// non-interactive runs it stays silent so the output stays usable.
	// term records whether stdout is a real terminal. The bar only
	// redraws (\r-based updates) when it can; on pipes, redirects, and
	// non-interactive runs it stays silent so the output stays usable.
	term bool
	// wg tracks the listen goroutine so Done can wait for the final render.
	wg sync.WaitGroup
	// finish guarantees the Done sequence (close, wait, final render,
	// newline) runs exactly once regardless of how many times Done is
	// called.
	finish sync.Once
}

func NewBar(total int64, label string) *Bar {
	return NewBarWithOffset(total, label, 0)
}

func NewBarWithOffset(total int64, label string, offset int64) *Bar {
	term := isTerminal(os.Stdout)
	b := &Bar{
		total:     total,
		width:     barWidth,
		label:     normalizeLabel(label),
		startTime: time.Now(),
		done:      make(chan struct{}),
		term:      term,
	}
	b.current.Store(offset)
	if term {
		b.wg.Add(1)
		go b.listen()
	}
	return b
}

// isTerminal reports whether f is attached to a character device (i.e. a
// terminal that can handle carriage-return redraws).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (b *Bar) Add(n int) {
	b.current.Add(int64(n))
}

func (b *Bar) listen() {
	// Update the UI 10 times per second (100ms)
	defer b.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.render()
		case <-b.done:
			return
		}
	}
}

func (b *Bar) render() {
	b.mu.Lock()
	defer b.mu.Unlock()

	current := b.current.Load()

	var percent float64
	var filled int
	if b.total > 0 {
		percent = float64(current) / float64(b.total) * 100
		filled = int(percent / 100 * float64(b.width))
	}

	elapsed := time.Since(b.startTime).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(current) / elapsed
	}

	var eta string
	if speed > 0 && current < b.total {
		remaining := b.total - current
		etaSeconds := float64(remaining) / speed
		// Sub-second ETAs blink as "0s" on the last chunks; only show
		// an ETA when there is something worth reading.
		if etaSeconds >= 1 {
			eta = " ETA " + formatETA(etaSeconds)
		}
	}

	if filled > b.width {
		filled = b.width
	}

	filledBar := Green(strings.Repeat("█", filled))
	emptyBar := Gray(strings.Repeat("░", b.width-filled))
	bar := filledBar + emptyBar

	line := fmt.Sprintf("\r%s %s %s %s/%s  %s/s%s",
		Bold(b.label),
		bar,
		Bold(fmt.Sprintf("%.1f%%", percent)),
		formatBytes(current),
		formatBytes(b.total),
		formatBytes(int64(speed)),
		eta,
	)

	fmt.Printf("%-120s", line)
}

func (b *Bar) Done() {
	// finish runs the close/render sequence at most once, even when Done is
	// called both explicitly (normal flow, so the final frame renders before
	// any follow-up message) and again through a deferred call on error paths.
	b.finish.Do(func() {
		close(b.done)
		// Wait for the ticker goroutine so no render runs concurrently with
		// the final frame, then render one guaranteed last frame.
		b.wg.Wait()
		if b.term {
			b.render()
			fmt.Println()
		}
	})
}

func normalizeLabel(label string) string {
	if len(label) > maxLabelWidth {
		return label[:maxLabelWidth-3] + "..."
	}
	return label
}

func formatETA(seconds float64) string {
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

func formatBytes(b int64) string {
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
