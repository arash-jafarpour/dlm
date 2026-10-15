package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arash-jafarpour/dlm/reader"
)

// readLinksFile loads queued URLs; a missing file is an empty queue, not
// an error (errors.Is unwraps through the errors.FileError wrapper).
func readLinksFile(path string) ([]string, error) {
	lf, err := reader.ReadLinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read queue: %w", err)
	}
	return lf.Links, nil
}

// queueStore serializes read-modify-write access to the queue and completed
// files. With parallel queue downloads (queue_jobs > 1) several workers
// finish around the same time; per-worker `read file, filter, rewrite`
// would stomp on each other, so every mutation goes through the store's
// mutex. The dlm process lock guarantees this is the only instance running.
type queueStore struct {
	queueFile     string
	completedFile string
	mu            sync.Mutex
}

func newQueueStore(ctx *Context) *queueStore {
	return &queueStore{
		queueFile:     ctx.Config.QueueFile,
		completedFile: ctx.Config.CompletedFile,
	}
}

// load returns the queued URLs; a missing file is an empty queue.
func (s *queueStore) load() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lf, err := readLinksFile(s.queueFile)
	if err != nil {
		return nil, err
	}
	return lf, nil
}

// append adds a URL to the queue (used by `queue add`).
func (s *queueStore) append(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.queueFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open queue file: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, url); err != nil {
		return fmt.Errorf("failed to write to queue: %w", err)
	}
	return nil
}

// completeAndRemove records a successful download: append to the completed
// history and remove the URL from the queue, in one atomic (under-store)
// step so the pair cannot observe a torn state.
func (s *queueStore) completeAndRemove(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.appendCompletedLocked(url); err != nil {
		return err
	}
	if err := s.removeLocked(url); err != nil {
		return err
	}
	return nil
}

func (s *queueStore) appendCompletedLocked(url string) error {
	f, err := os.OpenFile(s.completedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, url)
	return err
}

// remove drops one URL from the queue (board delete). Best effort: a
// missing line is not an error.
func (s *queueStore) remove(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(url)
}

// rewrite replaces the queue contents with urls, in order (board reorder,
// or reconciliation after an external editor session).
func (s *queueStore) rewrite(urls []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewriteLocked(urls)
}

func (s *queueStore) rewriteLocked(urls []string) error {
	if len(urls) == 0 {
		return os.WriteFile(s.queueFile, []byte(""), 0o644)
	}
	return os.WriteFile(s.queueFile, []byte(strings.Join(urls, "\n")+"\n"), 0o644)
}

// removeLocked rewrites the queue file without the URL, preserving the
// order of the rest.
func (s *queueStore) removeLocked(url string) error {
	data, err := os.ReadFile(s.queueFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to remove
		}
		return err
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != url {
			kept = append(kept, line)
		}
	}
	return s.rewriteLocked(kept)
}
