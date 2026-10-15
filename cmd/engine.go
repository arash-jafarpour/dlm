package cmd

import (
	"context"
	"sync"

	"github.com/arash-jafarpour/dlm/downloader"
)

// QueueRun drives a parallel queue run with caller-supplied per-job
// reporters. The plain CLI (progress block) and the interactive board both
// use it, so the download semantics live in exactly one place.
//
// Bookkeeping (completed-history append and queue removal, atomically via
// the store) happens inside the run; callbacks only observe.
type QueueRun struct {
	Ctx   *Context
	Store *queueStore
	URLs  []string
	Jobs  int

	// NewReporter builds the reporter for one job. Each job gets its own
	// reporter instance so row/line lifecycle binds to exactly one file.
	NewReporter func() downloader.Reporter

	// OnFileDone fires after every settled file. err is nil on success; it
	// is the download error on failure, or the store error when a
	// successful download could not be recorded. rep is the same reporter
	// instance created for that job (callers type-assert their own kind).
	OnFileDone func(url string, err error, rep downloader.Reporter)
}

// Start launches the worker pool and returns a channel closed once every
// job settled. Canceling ctx stops scheduling new files and aborts active
// transfers at their next context check (partial .part files remain, so a
// later run resumes).
func (r *QueueRun) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)

		jobs := r.Jobs
		if jobs < 1 {
			jobs = 1
		}
		if jobs > len(r.URLs) {
			jobs = len(r.URLs)
		}
		if jobs == 0 {
			return
		}

		var wg sync.WaitGroup
		urlCh := make(chan string)

		for w := 0; w < jobs; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for url := range urlCh {
					rep := r.NewReporter()
					runner := DownloadRunner(r.Ctx.Downloader)
					if concrete, ok := runner.(*downloader.Downloader); ok {
						runner = concrete.WithReporter(rep)
					}

					completed, err := runner.Download(ctx, url)
					if err == nil && completed {
						size, elapsed := transferStats(rep)
						if storeErr := r.Store.completeAndRemove(url, size, elapsed); storeErr != nil {
							err = storeErr
						}
					}
					if r.OnFileDone != nil {
						r.OnFileDone(url, err, rep)
					}
				}
			}()
		}

		for _, url := range r.URLs {
			select {
			case urlCh <- url:
			case <-ctx.Done():
			}
		}
		close(urlCh)
		wg.Wait()
	}()
	return done
}
