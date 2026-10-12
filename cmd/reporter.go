package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/ui"
)

// cliReporter implements downloader.Reporter by printing download lifecycle
// notifications to stdout and rendering progress with the ui package. It is
// the only place besides logError where download output reaches the terminal.
//
// quiet suppresses every notification, noProgress suppresses only the
// progress bar. All methods may be called while chunk goroutines run, which
// is fine: ui.Bar is concurrency-safe and the plain fmt.Printf calls only
// ever happen from the download's main goroutine (outside bar lifetime).
type cliReporter struct {
	quiet      bool
	noProgress bool
}

// cliReporter implements downloader.Reporter.
var _ downloader.Reporter = cliReporter{}

func (r cliReporter) Retry(attempt int, err error) {
	if r.quiet {
		return
	}
	fmt.Printf("  attempt %d failed: %v\n", attempt, err)
	fmt.Println("retrying...")
}

func (r cliReporter) ChunkRetry(chunkIndex, attempt int, err error) {
	if r.quiet {
		return
	}
	fmt.Printf("  chunk %d attempt %d failed: %v\n", chunkIndex, attempt, err)
	fmt.Println("retrying...")
}

func (r cliReporter) AlreadyComplete(fileName string) {
	if r.quiet {
		return
	}
	fmt.Printf("%s %s\n", ui.Green("✓ file already complete:"), fileName)
}

func (r cliReporter) Saved(path string) {
	if r.quiet {
		return
	}
	fmt.Printf("%s %s\n", ui.Green("✓ saved →"), path)
}

func (r cliReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	// quiet implies no bar; noProgress suppresses only the bar but keeps
	// the retry/complete/saved messages.
	if r.quiet || r.noProgress {
		return silentBar{}
	}
	return ui.NewBarWithOffset(total, label, offset)
}

// silentBar is a ProgressBar that discards everything.
type silentBar struct{}

func (silentBar) Add(int) {}
func (silentBar) Done()   {}

// parallelReporter feeds one queue-download job into a ui.MultiBar row.
// Each worker creates its own per job, so row lifecycle (finish on saved /
// fail) stays bound to exactly one file.
type parallelReporter struct {
	mgr   *ui.MultiBar
	quiet bool

	// row is the file's live row once Progress reported its size; nil
	// until then (e.g. failures before metadata) and in quiet mode.
	row ui.RowHandle
}

// parallelReporter implements downloader.Reporter.
var _ downloader.Reporter = (*parallelReporter)(nil)

func (r *parallelReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	if r.quiet {
		return silentBar{} // quiet stays terminal-silent as always
	}
	// Strip percent-encoding so users see the real file name.
	r.row = r.mgr.AddRow(total, cleanFileName(label), offset)
	return r.row
}

func (r *parallelReporter) Saved(path string) {
	if !r.quiet {
		r.mgr.FinishRow(r.row, ui.Green("✓ saved → ")+path)
	}
}

func (r *parallelReporter) AlreadyComplete(fileName string) {
	if !r.quiet {
		r.mgr.FinishRow(r.row, ui.Green("✓ file already complete: ")+fileName)
	}
}

func (r *parallelReporter) Retry(attempt int, err error) {
	if !r.quiet {
		r.mgr.Line(ui.Yellow(fmt.Sprintf("  attempt %d failed: %v", attempt, err)))
	}
}

func (r *parallelReporter) ChunkRetry(chunkIndex, attempt int, err error) {
	if !r.quiet {
		r.mgr.Line(ui.Yellow(fmt.Sprintf("  chunk %d attempt %d failed: %v", chunkIndex, attempt, err)))
	}
}

// finishError marks the job's row failed with a short, name-first reason.
// When the failure happened before any row existed (e.g. metadata), the
// error still surfaces as a plain line.
func (r *parallelReporter) finishError(err error) {
	if r.quiet {
		return // quiet mode: errors surface via exit code only
	}
	line := fmt.Sprintf("%s %v", ui.Red("✗ failed:"), err)
	if r.row != nil {
		r.mgr.FinishRow(r.row, line)
	} else {
		r.mgr.Line(line)
	}
}

// cleanFileName renders percent-encoded URL names as human names, dropping
// any query string ("FAB%20-%2012" → "FAB - 12.mkv").
func cleanFileName(name string) string {
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	if decoded, err := url.PathUnescape(name); err == nil && decoded != "" {
		name = decoded
	}
	return strings.TrimSpace(name)
}
