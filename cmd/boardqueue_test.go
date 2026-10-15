package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/arash-jafarpour/dlm/downloader"
)

// newBoardQueueRig builds a boardQueue over a running scheduler with temp
// files, ready for editing operations.
func newBoardQueueRig(t *testing.T, urls []string) (*boardQueue, *Context) {
	t.Helper()
	ctx, store := runContext(t, urls)
	ctx.Downloader = &fakeRunner{}

	sched := NewScheduler(store, urls, 2,
		func() DownloadRunner { return ctx.Downloader },
		func(int) downloader.Reporter { return nopReporter{} },
		nil,
	)
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sched.Start(runCtx)

	bq := newBoardQueue(store, sched, urls, func(string) {})
	return bq, ctx
}

func queueLines(t *testing.T, path string) []string {
	t.Helper()
	urls, err := readLinksFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return urls
}

func TestBoardQueueAddPersists(t *testing.T) {
	bq, ctx := newBoardQueueRig(t, []string{"https://x/a"})

	ids := bq.add([]string{"https://x/b", "https://x/typed-by-hand"})
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2", ids)
	}
	if ids[0] == ids[1] {
		t.Fatalf("ids must be distinct: %v", ids)
	}

	lines := queueLines(t, ctx.Config.QueueFile)
	if len(lines) != 3 || lines[1] != "https://x/b" || lines[2] != "https://x/typed-by-hand" {
		t.Fatalf("queue file = %v", lines)
	}
}

func TestBoardQueueDeleteRemovesLine(t *testing.T) {
	bq, ctx := newBoardQueueRig(t, []string{"https://x/a", "https://x/b", "https://x/c"})

	bq.delete(1) // b

	lines := queueLines(t, ctx.Config.QueueFile)
	if len(lines) != 2 || strings.Contains(strings.Join(lines, " "), "https://x/b") {
		t.Fatalf("queue file after delete = %v", lines)
	}
}

func TestBoardQueueReorderRewritesFile(t *testing.T) {
	bq, ctx := newBoardQueueRig(t, []string{"https://x/a", "https://x/b", "https://x/c"})

	bq.reorder([]int{2, 0, 1})

	lines := queueLines(t, ctx.Config.QueueFile)
	want := []string{"https://x/c", "https://x/a", "https://x/b"}
	if len(lines) != len(want) {
		t.Fatalf("queue file = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("queue file = %v, want %v", lines, want)
		}
	}
}

func TestBoardQueueReloadReconciles(t *testing.T) {
	urls := []string{"https://x/a", "https://x/b"}
	bq, ctx := newBoardQueueRig(t, urls)

	// The user edits the file externally: keeps b, drops a, adds c.
	if err := os.WriteFile(ctx.Config.QueueFile,
		[]byte("https://x/b\nhttps://x/c\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs := bq.reload()
	if len(jobs) != 2 {
		t.Fatalf("jobs = %v, want 2", jobs)
	}
	if jobs[0].URL != "https://x/b" || jobs[0].ID != 1 {
		t.Errorf("surviving URL must keep its job id, got %+v", jobs[0])
	}
	if jobs[1].URL != "https://x/c" || jobs[1].ID == 1 || jobs[1].ID < 0 {
		t.Errorf("new URL must get a fresh id, got %+v", jobs[1])
	}

	// A second reload with no changes must be a no-op (ids stable).
	again := bq.reload()
	if len(again) != 2 || again[0].ID != jobs[0].ID || again[1].ID != jobs[1].ID {
		t.Fatalf("reload must be idempotent: %v vs %v", jobs, again)
	}

	// Deleting the new job through the board also clears the file line.
	bq.delete(jobs[1].ID)
	lines := queueLines(t, ctx.Config.QueueFile)
	if len(lines) != 1 || lines[0] != "https://x/b" {
		t.Fatalf("queue file after delete = %v", lines)
	}
}
