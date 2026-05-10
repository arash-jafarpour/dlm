package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("another instance of dlm is already running")
	}

	file.Truncate(0)
	file.Seek(0, 0)
	fmt.Fprintf(file, "%d\n", os.Getpid())

	return &LockFile{
		path: lockPath,
		file: file,
	}, nil
}

func (lf *LockFile) Release() error {
	if lf.file == nil {
		return nil
	}

	syscall.Flock(int(lf.file.Fd()), syscall.LOCK_UN)

	if err := lf.file.Close(); err != nil {
		return err
	}

	return os.Remove(lf.path)
}
