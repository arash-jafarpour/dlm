package errors

import (
	"fmt"
	"net/http"
)

// HTTPError represents an HTTP-related failure (network errors, bad status codes)
type HTTPError struct {
	URL        string
	StatusCode int    // -1 if no response received (network error)
	Message    string // What operation failed
	Err        error
}

func (e *HTTPError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf(
			"HTTP error for %s: %s (status %d): %v",
			e.URL,
			e.Message,
			e.StatusCode,
			e.Err,
		)
	}
	return fmt.Sprintf("HTTP error for %s: %s: %v", e.URL, e.Message, e.Err)
}

func (e *HTTPError) Unwrap() error { return e.Err }

func WrapHTTPError(url, msg string, statusCode int, err error) error {
	return &HTTPError{URL: url, Message: msg, StatusCode: statusCode, Err: err}
}

// NewStatusError creates an HTTPError for unexpected HTTP status codes
func NewStatusError(url string, resp *http.Response) error {
	return &HTTPError{
		URL:        url,
		StatusCode: resp.StatusCode,
		Message:    fmt.Sprintf("unexpected status: %s", resp.Status),
	}
}
