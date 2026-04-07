package errors

import "fmt"

// FileError represents a file I/O failure
type FileError struct {
	Path string
	Op   string // "open", "read", "write", "mkdir", "stat", "remove"
	Err  error
}

func (e *FileError) Error() string {
	return fmt.Sprintf("file %s failed for %q: %v", e.Op, e.Path, e.Err)
}

func (e *FileError) Unwrap() error { return e.Err }

func WrapFileError(path, op string, err error) error {
	return &FileError{Path: path, Op: op, Err: err}
}
