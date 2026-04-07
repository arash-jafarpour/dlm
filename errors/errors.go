package errors

import "errors"

// Predefined error values for exact matching with errors.Is()
var (
	ErrUnsupportedProtocol = errors.New("unsupported protocol")
	ErrMetadataFetchFailed = errors.New("failed to fetch metadata")
	ErrNilResponse         = errors.New("received nil HTTP response")
	ErrMergeFailed         = errors.New("failed to merge download chunks")
)
