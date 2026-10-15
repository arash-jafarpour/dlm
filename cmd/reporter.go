package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/arash-jafarpour/dlm/config"

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

	// Last transfer stats, fed by the downloader's StatsSink and read by
	// the queue engine when recording the completed history.
	size    int64
	elapsed time.Duration
}

// cliReporter implements downloader.Reporter.
var _ downloader.Reporter = &cliReporter{}

// TransferDone implements downloader.StatsSink.
func (r *cliReporter) TransferDone(size int64, elapsed time.Duration) {
	r.size, r.elapsed = size, elapsed
}

// Stats returns the last transfer's size and duration (0 when unknown).
func (r *cliReporter) Stats() (int64, time.Duration) { return r.size, r.elapsed }

func (r *cliReporter) Retry(attempt int, err error) {
	if r.quiet {
		return
	}
	fmt.Printf("  attempt %d failed: %v\n", attempt, err)
	fmt.Println("retrying...")
}

func (r *cliReporter) ChunkRetry(chunkIndex, attempt int, err error) {
	if r.quiet {
		return
	}
	fmt.Printf("  chunk %d attempt %d failed: %v\n", chunkIndex, attempt, err)
	fmt.Println("retrying...")
}

func (r *cliReporter) AlreadyComplete(fileName string) {
	if r.quiet {
		return
	}
	fmt.Printf("%s %s\n", ui.Green("✓ file already complete:"), fileName)
}

func (r *cliReporter) Saved(path string) {
	if r.quiet {
		return
	}
	fmt.Printf("%s %s\n", ui.Green("✓ saved →"), path)
}

func (r *cliReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
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

	size    int64
	elapsed time.Duration

	// row is the file's live row once Progress reported its size; nil
	// until then (e.g. failures before metadata) and in quiet mode.
	row ui.RowHandle
}

// parallelReporter implements downloader.Reporter.
var _ downloader.Reporter = (*parallelReporter)(nil)

// TransferDone implements downloader.StatsSink.
func (r *parallelReporter) TransferDone(size int64, elapsed time.Duration) {
	r.size, r.elapsed = size, elapsed
}

// Stats returns the last transfer's size and duration (0 when unknown).
func (r *parallelReporter) Stats() (int64, time.Duration) { return r.size, r.elapsed }

func (r *parallelReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	if r.quiet {
		return silentBar{} // quiet stays terminal-silent as always
	}
	// Strip percent-encoding so users see the real file name.
	r.row = r.mgr.AddRow(total, ui.CleanFileName(label), offset)
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

// transferStats extracts the last transfer's size and duration from a
// reporter, when it carries them (all CLI and board reporters do).
func transferStats(rep downloader.Reporter) (int64, time.Duration) {
	if ts, ok := rep.(interface{ Stats() (int64, time.Duration) }); ok {
		return ts.Stats()
	}
	return 0, 0
}

// runnerProvider builds one downloader per job from the live config: the
// HTTP transport (connection pool) is created once and shared, while
// per-transfer settings (chunks, output dir, retries) are re-read every
// time, so a settings change applies to the next file started.
type runnerProvider struct {
	cfg  *config.Config
	base *downloader.Downloader
	mu   sync.Mutex
}

func newRunnerProvider(cfg *config.Config) *runnerProvider {
	return &runnerProvider{cfg: cfg, base: downloader.New(cfg, nil)}
}

func (p *runnerProvider) New() DownloadRunner {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *p.base
	cp.OutputDir = p.cfg.OutputDir
	cp.NumChunks = p.cfg.NumChunks
	cp.MaxRetries = p.cfg.MaxRetries
	return &cp
}
