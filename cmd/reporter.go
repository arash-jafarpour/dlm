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
// All its methods may be called while chunk goroutines run, which is fine:
// ui.Bar is concurrency-safe and the plain fmt.Printf calls only ever happen
// from the download's main goroutine (outside bar lifetime).
type cliReporter struct{}

// cliReporter implements downloader.Reporter.
var _ downloader.Reporter = cliReporter{}

func (cliReporter) Retry(attempt int, err error) {
	fmt.Printf("  attempt %d failed: %v\n", attempt, err)
	fmt.Println("retrying...")
}

func (cliReporter) AlreadyComplete(fileName string) {
	fmt.Printf("%s %s\n", ui.Green("✓ file already complete:"), fileName)
}

func (cliReporter) Saved(path string) {
	fmt.Printf("%s %s\n", ui.Green("✓ saved →"), path)
}

func (cliReporter) Progress(total int64, label string, offset int64) downloader.ProgressBar {
	return ui.NewBarWithOffset(total, label, offset)
}
