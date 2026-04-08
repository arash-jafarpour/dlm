package ui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Bar struct {
	total     int64
	current   atomic.Int64
	width     int
	label     string
	startTime time.Time
	mu        sync.Mutex
}

func NewBar(total int64, label string) *Bar {
	return &Bar{
		total:     total,
		width:     40,
		label:     label,
		startTime: time.Now(),
	}
}

func NewBarWithOffset(total int64, label string, offset int64) *Bar {
	b := &Bar{
		total:     total,
		width:     40,
		label:     label,
		startTime: time.Now(),
	}
	b.current.Store(offset)
	return b
}

// Add adds n bytes and redraws the bar
func (b *Bar) Add(n int) {
	b.current.Add(int64(n))
	b.render()
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
		etaDuration := time.Duration(etaSeconds * float64(time.Second))
		eta = fmt.Sprintf(" ETA %s", etaDuration.Round(time.Second))
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", b.width-filled)
	suffix := fmt.Sprintf(" %s %.1f%% %s/%s  %s/s%s",
		bar,
		percent,
		formatBytes(current),
		formatBytes(b.total),
		formatBytes(int64(speed)),
		eta,
	)
	label := normalizeLabel(b.label)

	fmt.Printf("\r%s%s", label, suffix)

	if current >= b.total && b.total > 0 {
		fmt.Println()
	}
}

func normalizeLabel(label string) string {
	maxWidth := 64

	if len(label) > maxWidth {
		return label[:maxWidth-1] + "…"
	}
	return label
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
