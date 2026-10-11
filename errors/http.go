package errors

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPError represents an HTTP-related failure (network errors, bad status codes)
type HTTPError struct {
	URL        string
	StatusCode int    // -1 if no response received (network error)
	Message    string // What operation failed
	// RetryAfter is the server-declared retry delay from a Retry-After
	// header, when one was present and parseable in seconds. The retry
	// machinery must wait at least this long on the next attempt.
	RetryAfter time.Duration
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
		RetryAfter: RetryAfterOf(resp),
	}
}

// RetryAfterOf parses a Retry-After header into a duration. Only the
// "delay-seconds" form is handled; HTTP-date responses (rare on rate
// limiter status codes) yield 0 and fall back to exponential backoff.
func RetryAfterOf(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// IsRetryable returns true if the error might succeed on retry
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Explicitly permanent wrappers win over everything else, including
	// the retryable error wrapped inside them.
	var permanent *PermanentError
	if errors.As(err, &permanent) {
		return false
	}

	// Check for HTTP errors
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		code := httpErr.StatusCode

		// No response received (network error) - retryable
		if code == -1 {
			return true
		}

		// 4xx client errors are permanent (bad request, not found,
		// forbidden, etc.) — except the rate/timeout family, which is
		// exactly what Retry-After exists for.
		if code >= 400 && code < 500 {
			switch code {
			case http.StatusRequestTimeout, http.StatusTooManyRequests:
				return true
			default:
				return false
			}
		}

		// 5xx server errors are retryable
		if code >= 500 && code < 600 {
			return true
		}

		// Other status codes (e.g., 3xx) - don't retry
		return false
	}

	// Network/timeout errors are retryable
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// DNS errors are retryable
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Connection refused, reset, etc. are retryable
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// TLS errors are generally permanent (cert issues)
	if strings.Contains(err.Error(), "tls:") || strings.Contains(err.Error(), "certificate") {
		return false
	}

	// File system errors are permanent
	var fileErr *FileError
	if errors.As(err, &fileErr) {
		return false
	}

	// Default: retry on unknown errors
	return true
}
