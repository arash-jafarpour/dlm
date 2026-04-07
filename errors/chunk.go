package errors

import "fmt"

// ChunkError represents a failure in chunked downloading
type ChunkError struct {
	URL   string
	Index int // Which chunk failed; -1 for merge failures
	Err   error
}

func (e *ChunkError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("chunk %d failed for %s: %v", e.Index, e.URL, e.Err)
	}
	return fmt.Sprintf("chunk merge failed for %s: %v", e.URL, e.Err)
}

func (e *ChunkError) Unwrap() error { return e.Err }

func WrapChunkError(url string, index int, err error) error {
	return &ChunkError{URL: url, Index: index, Err: err}
}
