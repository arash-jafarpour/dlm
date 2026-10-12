package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arash-jafarpour/dlm/config"
	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/internal/tui"
)

// blockRunner blocks each download until its context is canceled, recording
// attempts per URL. Tests drive pause/cancel deterministically through it.
type blockRunner struct {
	mu       sync.Mutex
	attempts map[string]int
	started  chan string
}

func newBlockRunner() *blockRunner {
	return &blockRunner{attempts: map[string]int{}, started: make(chan string, 16)}
}

func (b *blockRunner) Download(ctx context.Context, url string) (bool, error) {
	b.mu.Lock()
	b.attempts[url]++
	b.mu.Unlock()
	select {
	case b.started <- url:
	default:
	}
	<-ctx.Done()
	return false, ctx.Err()
}

func (b *blockRunner) count(url string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempts[url]
}

// failOnceRunner fails the first attempt for each URL immediately, then
// blocks like blockRunner. Simulates a transient failure for retry tests.
type failOnceRunner struct {
	*blockRunner
	mu     sync.Mutex
	failed map[string]bool
}

func newFailOnceRunner() *failOnceRunner {
	return &failOnceRunner{blockRunner: newBlockRunner(), failed: map[string]bool{}}
}

func (f *failOnceRunner) Download(ctx context.Context, url string) (bool, error) {
	f.mu.Lock()
	first := !f.failed[url]
	f.failed[url] = true
	f.mu.Unlock()

	if first {
		f.mu.Lock()
		f.attempts[url]++
		f.mu.Unlock()
		return false, errors.New("transient boom")
	}
	return f.blockRunner.Download(ctx, url)
}

// okRunner completes immediately with success.
type okRunner struct{}

func (okRunner) Download(context.Context, string) (bool, error) { return true, nil }

// nopReporter satisfies downloader.Reporter for scheduler tests; none of its
// hooks are exercised because the test runners are not the concrete
// downloader.
type nopReporter struct{}

func (nopReporter) Retry(int, error)                                     {}
func (nopReporter) ChunkRetry(int, int, error)                           {}
func (nopReporter) AlreadyComplete(string)                               {}
func (nopReporter) Saved(string)                                         {}
func (nopReporter) Progress(int64, string, int64) downloader.ProgressBar { return nopBar{} }

type nopBar struct{}

func (nopBar) Add(int) {}
func (nopBar) Done()   {}

// schedRig is the test rig around a Scheduler.
type schedRig struct {
	sched    *Scheduler
	store    *queueStore
	notifyMu sync.Mutex
	states   []tui.JobStateMsg
	done     <-chan struct{}
}

func newSchedRig(t *testing.T, urls []string, workers int, runner DownloadRunner) (*schedRig, *Context) {
	t.Helper()
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	cfg.OutputDir = t.TempDir()
	if len(urls) > 0 {
		if err := os.WriteFile(cfg.QueueFile, []byte(strings.Join(urls, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &Context{Config: cfg}
	ctx.Downloader = runner

	rig := &schedRig{store: newQueueStore(ctx)}
	rig.sched = NewScheduler(ctx, rig.store, urls, workers,
		func(int) downloader.Reporter { return nopReporter{} },
		func(msg tui.JobStateMsg) {
			rig.notifyMu.Lock()
			rig.states = append(rig.states, msg)
			rig.notifyMu.Unlock()
		},
	)
	return rig, ctx
}

func (r *schedRig) has(state tui.RowStatus, id int) bool {
	r.notifyMu.Lock()
	defer r.notifyMu.Unlock()
	for _, m := range r.states {
		if m.State == state && (id < 0 || m.ID == id) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestSchedulerPauseResumeRerunsJob(t *testing.T) {
	url := "https://example.com/FAB.mkv"
	runner := newBlockRunner()
	rig, _ := newSchedRig(t, []string{url}, 1, runner)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	rig.done = rig.sched.Start(runCtx)

	waitFor(t, "first attempt", func() bool { return runner.count(url) == 1 })
	rig.sched.Control(0, tui.ControlPause)
	waitFor(t, "paused report", func() bool { return rig.has(tui.RowPaused, 0) })
	if closed(rig.done) {
		t.Fatal("paused job must keep the run open")
	}

	rig.sched.Control(0, tui.ControlResume)
	waitFor(t, "second attempt (resume)", func() bool { return runner.count(url) == 2 })

	rig.sched.Control(0, tui.ControlCancel)
	waitFor(t, "cancelled report", func() bool { return rig.has(tui.RowCancelled, 0) })
	waitFor(t, "run finished", func() bool { return closed(rig.done) })

	if got := runner.count(url); got != 2 {
		t.Errorf("attempts = %d, want 2 (pause stops, resume re-runs)", got)
	}
}

func TestSchedulerRetryAfterFailure(t *testing.T) {
	url := "https://example.com/flaky.bin"
	runner := newFailOnceRunner()
	rig, _ := newSchedRig(t, []string{url}, 1, runner)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	rig.done = rig.sched.Start(runCtx)

	waitFor(t, "failed report", func() bool { return rig.has(tui.RowFailed, 0) })

	// The run may already consider itself finished with only failed jobs,
	// but retry must still work: the control loop stays live.
	rig.sched.Control(0, tui.ControlRetry)
	waitFor(t, "retry attempt", func() bool { return runner.count(url) == 2 })

	rig.sched.Control(0, tui.ControlCancel)
	waitFor(t, "run finished", func() bool { return closed(rig.done) })
}

func TestSchedulerCancelQueuedJobSkipsIt(t *testing.T) {
	first := "https://example.com/first.bin"
	second := "https://example.com/second.bin"
	runner := newBlockRunner()
	rig, _ := newSchedRig(t, []string{first, second}, 1, runner)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	rig.done = rig.sched.Start(runCtx)

	waitFor(t, "first job running", func() bool { return runner.count(first) == 1 })

	// The second job is still queued: cancel it before it ever starts.
	rig.sched.Control(1, tui.ControlCancel)
	waitFor(t, "cancelled report", func() bool { return rig.has(tui.RowCancelled, 1) })

	rig.sched.Control(0, tui.ControlCancel)
	waitFor(t, "run finished", func() bool { return closed(rig.done) })

	if got := runner.count(second); got != 0 {
		t.Errorf("cancelled queued job started %d times, want 0", got)
	}
}

func TestSchedulerSuccessRecordsAndFinishes(t *testing.T) {
	url := "https://example.com/good.bin"
	rig, ctx := newSchedRig(t, []string{url}, 1, okRunner{})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	rig.done = rig.sched.Start(runCtx)

	waitFor(t, "run finished", func() bool { return closed(rig.done) })

	remaining, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("completed job must leave the queue, got %v", remaining)
	}
	data, err := os.ReadFile(ctx.Config.CompletedFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), url) {
		t.Errorf("completed history missing the URL: %s", data)
	}
}

func TestSchedulerAllCancelledFinishes(t *testing.T) {
	url := "https://example.com/never.bin"
	runner := newBlockRunner()
	rig, _ := newSchedRig(t, []string{url}, 1, runner)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	rig.done = rig.sched.Start(runCtx)

	waitFor(t, "job started", func() bool { return runner.count(url) == 1 })
	rig.sched.Control(0, tui.ControlCancel)
	waitFor(t, "run finished", func() bool { return closed(rig.done) })
}
