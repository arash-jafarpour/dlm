package tui

import (
	"fmt"

	"github.com/arash-jafarpour/dlm/downloader"
)

// BoardReporter adapts one job's engine callbacks into board messages. cmd
// creates a fresh instance per job (wrapped into the downloader via
// Downloader.WithReporter) and pumps messages with program.Send, which is
// safe to call from any goroutine.
//
// Terminal states other than "saved" (paused, cancelled, failed) are
// reported by the scheduler, which owns job state; this reporter owns
// progress and the successful outcomes.
type BoardReporter struct {
	id   int
	send func(msg any)
	row  *Row
}

// NewBoardReporter wires a per-job reporter to a message sink (typically
// prog.Send).
func NewBoardReporter(id int, send func(msg any)) *BoardReporter {
	return &BoardReporter{id: id, send: send}
}

// BoardReporter implements downloader.Reporter.
var _ downloader.Reporter = (*BoardReporter)(nil)

func (r *BoardReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	r.row = newRow(r.id, label, total, offset)
	r.send(FileStartedMsg{ID: r.id, Row: r.row})
	return r.row
}

func (r *BoardReporter) Saved(path string) {
	r.send(FileDoneMsg{Row: r.row, Text: fmt.Sprintf("✓ saved → %s", path)})
}

func (r *BoardReporter) AlreadyComplete(fileName string) {
	r.send(FileDoneMsg{Row: r.row, Text: fmt.Sprintf("✓ file already complete: %s", fileName)})
}

func (r *BoardReporter) Retry(attempt int, err error) {
	r.send(WarnMsg{Text: fmt.Sprintf("attempt %d failed: %v", attempt, err)})
}

func (r *BoardReporter) ChunkRetry(chunkIndex, attempt int, err error) {
	r.send(WarnMsg{Text: fmt.Sprintf("chunk %d attempt %d failed: %v", chunkIndex, attempt, err)})
}
