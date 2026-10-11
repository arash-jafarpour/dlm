package errors

// PermanentError marks an error as non-retryable even when the wrapped error
// is transient. The per-chunk retry loop uses it when all chunk attempts are
// exhausted, so the outer whole-download retry loop does not restart a
// doomed download (with the same failure) from scratch.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return "permanent failure: " + e.Err.Error()
}

// Unwrap exposes the cause so errors.As / errors.Is keep matching inner
// error types (HTTPError, ChunkError, ...).
func (e *PermanentError) Unwrap() error { return e.Err }

// MarkPermanent wraps err so IsRetryable reports false regardless of the
// wrapped error's own retryability.
func MarkPermanent(err error) error {
	return &PermanentError{Err: err}
}
