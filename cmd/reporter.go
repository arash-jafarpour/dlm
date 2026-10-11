package cmd

import (
	"fmt"

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
	// the retry/complete/saved messages. Without either flag the ui.Bar
	// itself decides whether stdout can carry \r redraws.
	if r.quiet || r.noProgress {
		return silentBar{}
	}
	return ui.NewBarWithOffset(total, label, offset)
}

// silentBar is a ProgressBar that discards everything.
type silentBar struct{}

func (silentBar) Add(int) {}
func (silentBar) Done()   {}
