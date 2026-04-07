package errors

import "fmt"

// DownloadError is a general download failure wrapper
type DownloadError struct {
	URL     string
	Message string
	Err     error
}

func (e *DownloadError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("download failed for %s: %s: %v", e.URL, e.Message, e.Err)
	}
	return fmt.Sprintf("download failed for %s: %s", e.URL, e.Message)
}

func (e *DownloadError) Unwrap() error { return e.Err }

func WrapDownloadError(url, msg string, err error) error {
	return &DownloadError{URL: url, Message: msg, Err: err}
}
