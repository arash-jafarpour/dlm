package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arash-jafarpour/dlm/config"
	apperrors "github.com/arash-jafarpour/dlm/errors"
)

func TestQueueAdd(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	tmpFile.Close()

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{Config: cfg}

	if err := queueAdd(ctx, "https://example.com"); err != nil {
		t.Fatalf("queueAdd failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "https://example.com") {
		t.Errorf("expected URL to be in queue file, got: %s", content)
	}
}

func TestQueueList(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	testData := "https://example.com\nhttps://test.com\n"
	if _, err := tmpFile.WriteString(testData); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{Config: cfg}
	if err := queueList(ctx); err != nil {
		t.Fatalf("queueList failed: %v", err)
	}
}

func TestQueueListMissingFileIsEmptyQueue(t *testing.T) {
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "does-not-exist.txt")
	ctx := &Context{Config: cfg}

	if err := queueList(ctx); err != nil {
		t.Fatalf("queueList with missing file: %v", err)
	}
}

func TestQueueClear(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	testData := "https://example.com\nhttps://test.com\n"
	if err := os.WriteFile(tmpFile.Name(), []byte(testData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{Config: cfg}

	if err := queueClearWithConfirm(ctx, false); err != nil {
		t.Fatalf("queueClearWithConfirm failed: %v", err)
	}

	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 0 {
		t.Errorf("expected empty file, got: %s", content)
	}
}

// runContext wires a queue-based Context over temp files with the given
// queued URLs.
func runContext(t *testing.T, urls []string) (*Context, *queueStore) {
	t.Helper()
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	cfg.OutputDir = t.TempDir()
	if err := os.WriteFile(cfg.QueueFile, []byte(strings.Join(urls, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Config: cfg}
	return ctx, newQueueStore(ctx)
}

// fakeRunner implements DownloadRunner without the network, recording
// concurrency and completion.
type fakeRunner struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	failures    map[string]bool
	delay       time.Duration
}

func (f *fakeRunner) Download(_ context.Context, url string) (bool, error) {
	if f.failures[url] {
		// Marked permanent so tests do not sit through retry backoffs.
		return false, apperrors.MarkPermanent(fmt.Errorf("fake download error"))
	}

	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.mu.Unlock()

	time.Sleep(f.delay)

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return true, nil
}

func (f *fakeRunner) stats() (maxInFlight int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

func TestQueueParallelRunsConcurrently(t *testing.T) {
	urls := make([]string, 6)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/file%d.bin", i)
	}
	ctx, _ := runContext(t, urls)
	ctx.Config.QueueJobs = 3
	runner := &fakeRunner{delay: 20 * time.Millisecond}
	ctx.Downloader = runner

	if err := downloadQueue(ctx); err != nil {
		t.Fatalf("downloadQueue: %v", err)
	}

	if got := runner.stats(); got < 2 {
		t.Errorf("max in-flight = %d, want >= 2 (files ran concurrently)", got)
	}

	remaining, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("queue not drained: %v", remaining)
	}
	data, err := os.ReadFile(ctx.Config.CompletedFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := nonEmptyLines(string(data)); len(lines) != len(urls) {
		t.Errorf("completed lines = %d, want %d:\n%s", len(lines), len(urls), data)
	}
}

func TestQueueSequentialIsExclusive(t *testing.T) {
	urls := []string{"https://a/1", "https://b/2"}
	ctx, _ := runContext(t, urls)
	ctx.Config.QueueJobs = 1
	runner := &fakeRunner{delay: 10 * time.Millisecond}
	ctx.Downloader = runner

	if err := downloadQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runner.stats(); got != 1 {
		t.Errorf("sequential run had %d files in flight, want exactly 1", got)
	}
}

func TestQueueParallelFailureKeepsURL(t *testing.T) {
	urls := []string{"https://a/ok", "https://b/bad"}
	ctx, _ := runContext(t, urls)
	ctx.Config.QueueJobs = 2
	runner := &fakeRunner{
		failures: map[string]bool{"https://b/bad": true},
	}
	ctx.Downloader = runner

	if err := downloadQueue(ctx); err != nil {
		t.Fatalf("queue failures are per-file, not fatal: %v", err)
	}

	remaining, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0] != "https://b/bad" {
		t.Errorf("failed URL must stay queued, got %v", remaining)
	}
	data, _ := os.ReadFile(ctx.Config.CompletedFile)
	if strings.Contains(string(data), "https://b/bad") {
		t.Error("failed URL must not be marked completed")
	}
}

func TestQueueStoreConcurrentCompletionIntegrity(t *testing.T) {
	// Hammer completeAndRemove from many goroutines directly: the queue
	// rewrite path is where per-worker read-modify-write would corrupt.
	urls := make([]string, 12)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://x/%02d", i)
	}
	ctx, store := runContext(t, urls)

	var wg sync.WaitGroup
	for _, u := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := store.completeAndRemove(url); err != nil {
				t.Errorf("completeAndRemove(%s): %v", url, err)
			}
		}(u)
	}
	wg.Wait()

	remaining, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("queue must be empty, got %v", remaining)
	}
	data, _ := os.ReadFile(ctx.Config.CompletedFile)
	if lines := nonEmptyLines(string(data)); len(lines) != len(urls) {
		t.Errorf("completed lines = %d, want %d", len(lines), len(urls))
	}
}
