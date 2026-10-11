package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

type LockFile struct {
	path string
	file *os.File
}

func acquireLock() (*LockFile, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	lockPath := filepath.Join(homeDir, ".config", "dlm", "dlm.lock")

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	if err := flockExclusive(file); err != nil {
		_ = file.Close() // best effort; the error path already decided
		return nil, fmt.Errorf("another instance of dlm is already running")
	}

	// The pid is best-effort diagnostics for the process table; failures to
	// write it must not make a working lock fail, but an unwriteable lock
	// file is suspicious enough to abort.
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to truncate lock file: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to seek lock file: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to write pid to lock file: %w", err)
	}

	return &LockFile{
		path: lockPath,
		file: file,
	}, nil
}

func (lf *LockFile) Release() error {
	if lf.file == nil {
		return nil
	}

	if err := funlock(lf.file); err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	if err := lf.file.Close(); err != nil {
		return err
	}

	return os.Remove(lf.path)
}
