package tui

import (
	"fmt"

	"github.com/arash-jafarpour/dlm/downloader"
)

// BoardReporter adapts one job's engine callbacks into board messages.
// cmd creates a fresh instance per job (wrapped into the downloader via
// Downloader.WithReporter) and pumps messages with program.Send, which is
// safe to call from any goroutine.
type BoardReporter struct {
	send func(msg any) // tea.Msg send hook; typed as any to avoid importing tea here
	row  *Row
}

// NewBoardReporter wires a reporter to a message sink (typically prog.Send).
func NewBoardReporter(send func(msg any)) *BoardReporter {
	return &BoardReporter{send: send}
}

// BoardReporter implements downloader.Reporter.
var _ downloader.Reporter = (*BoardReporter)(nil)

func (r *BoardReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	r.row = newRow(label, total, offset)
	r.send(FileStartedMsg{Row: r.row})
	return r.row
}

func (r *BoardReporter) Saved(path string) {
	r.settle(fmt.Sprintf("✓ saved → %s", path), false)
}

func (r *BoardReporter) AlreadyComplete(fileName string) {
	r.settle(fmt.Sprintf("✓ file already complete: %s", fileName), false)
}

func (r *BoardReporter) Retry(attempt int, err error) {
	r.send(WarnMsg{Text: fmt.Sprintf("attempt %d failed: %v", attempt, err)})
}

func (r *BoardReporter) ChunkRetry(chunkIndex, attempt int, err error) {
	r.send(WarnMsg{Text: fmt.Sprintf("chunk %d attempt %d failed: %v", chunkIndex, attempt, err)})
}

// Fail is called by the engine's OnFileDone when the download itself failed
// (the reporter's own settle paths only see successful outcomes).
func (r *BoardReporter) Fail(err error) {
	r.settle(fmt.Sprintf("✗ failed: %v", err), true)
}

func (r *BoardReporter) settle(text string, failed bool) {
	r.send(FileDoneMsg{Row: r.row, Text: text, Failed: failed})
}
