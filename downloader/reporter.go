package downloader

// ProgressBar reports bytes consumed by an in-flight transfer. Add may be
// called concurrently from multiple chunk goroutines; Done marks the transfer
// as finished (e.g. clears the last carriage-return line).
type ProgressBar interface {
	Add(n int)
	Done()
}

// Reporter receives download lifecycle notifications so the downloader package
// stays free of direct printing and terminal UI code.
//
// Implementations must be safe for concurrent use: Add/Progress are called
// from multiple chunk goroutines simultaneously, and every other method may
// also be invoked while transfers run.
type Reporter interface {
	// Retry is called after a whole-download attempt failed, immediately
	// before the next attempt starts.
	Retry(attempt int, err error)
	// ChunkRetry is called after a single chunk attempt failed, immediately
	// before that chunk's next attempt starts. Chunks retry independently,
	// so several chunk attempts may interleave.
	ChunkRetry(chunkIndex, attempt int, err error)
	// AlreadyComplete is called when the target file already exists with
	// the expected size, so nothing was downloaded.
	AlreadyComplete(fileName string)
	// Saved is called after the file was fully assembled at path.
	Saved(path string)
	// Progress returns a progress adder for a transfer. offset is the number
	// of already-downloaded bytes, so a resume shows cumulative progress.
	Progress(total int64, label string, offset int64) ProgressBar
}

// noopReporter discards all notifications; it is installed by New when no
// Reporter is supplied so callers never need nil checks.
type noopReporter struct{}

func (noopReporter) Retry(int, error)                          {}
func (noopReporter) ChunkRetry(int, int, error)                {}
func (noopReporter) AlreadyComplete(string)                    {}
func (noopReporter) Saved(string)                              {}
func (noopReporter) Progress(int64, string, int64) ProgressBar { return noopBar{} }

// noopBar is the ProgressBar used by noopReporter.
type noopBar struct{}

func (noopBar) Add(int) {}
func (noopBar) Done()   {}
