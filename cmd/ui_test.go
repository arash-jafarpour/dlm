package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arash-jafarpour/dlm/config"
	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/internal/tui"
)

func TestSettingDefsRoundTrip(t *testing.T) {
	cfg := config.Default()
	before := *cfg

	// Per-kind invalid input: text/path values reject empty strings,
	// numerics reject non-numbers, booleans reject anything else.
	invalidByKey := map[string]string{
		"num_chunks":           "abc",
		"queue_jobs":           "abc",
		"insecure_skip_verify": "yes",
		"output_dir":           "",
		"queue_file":           "",
		"completed_file":       "",
	}

	for _, d := range settingDefs {
		if d.Default(cfg) == "" {
			t.Errorf("%s: empty default", d.Key)
		}
		bad, ok := invalidByKey[d.Key]
		if !ok {
			t.Errorf("%s: no invalid sample in the table", d.Key)
			continue
		}
		if err := d.Set(cfg, bad); err == nil {
			t.Errorf("%s: invalid input %q must be refused", d.Key, bad)
		}
	}
	if *cfg != before {
		t.Errorf("invalid sets must not mutate the config: %+v", cfg)
	}

	// Valid input through the shared applySetting path.
	if err := applySetting(cfg, "num_chunks", "16"); err != nil {
		t.Fatal(err)
	}
	if cfg.NumChunks != 16 {
		t.Errorf("num_chunks = %d", cfg.NumChunks)
	}
	if err := applySetting(cfg, "queue_jobs", "99"); err == nil {
		t.Error("queue_jobs above the cap must be refused")
	}
	if err := applySetting(cfg, "insecure_skip_verify", "yes"); err == nil {
		t.Error("non-boolean value must be refused")
	}
	if err := applySetting(cfg, "nope", "x"); err == nil {
		t.Error("unknown key must be refused")
	}

	// Rows render the live values.
	rows := settingsRows(cfg)
	if len(rows) != len(settingDefs) {
		t.Fatalf("rows = %d, want %d", len(rows), len(settingDefs))
	}
	for _, r := range rows {
		if r.Key == "num_chunks" && r.Value != "16" {
			t.Errorf("row value = %q, want 16", r.Value)
		}
	}
}

func TestStoreRemoveCompletedLineAndClear(t *testing.T) {
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	lines := []string{
		"2026-10-15 10:00:00 | https://x/a | 100 | 1s",
		"2026-10-15 11:00:00 | https://x/b | 200 | 2s",
	}
	if err := os.WriteFile(cfg.CompletedFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Config: cfg}
	store := newQueueStore(ctx)

	if err := store.removeCompletedLine(lines[0]); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.CompletedFile)
	if strings.Contains(string(data), "https://x/a") || !strings.Contains(string(data), "https://x/b") {
		t.Fatalf("remove dropped the wrong line: %s", data)
	}

	if err := store.clearCompleted(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfg.CompletedFile)
	if len(data) != 0 {
		t.Fatalf("clear left %q", data)
	}

	// Removing from a missing file is not an error.
	if err := store.removeCompletedLine("whatever"); err != nil {
		t.Fatalf("remove on empty history: %v", err)
	}
}

func TestCompletedDisplayEntriesKeepRefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "completed.txt")
	raw := []string{
		"2026-10-15 10:00:00 | https://x/a.mkv | 1048576 | 3s",
		"2026-10-15 11:00:00 | https://x/b.mkv",
	}
	if err := os.WriteFile(path, []byte(strings.Join(raw, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := completedDisplayEntries(path)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// Newest first, refs are the raw lines, display shows size/duration.
	if !strings.Contains(entries[0].Text, "b.mkv") || entries[0].Ref != raw[1] {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if !strings.Contains(entries[1].Text, "1.0 MB") || !strings.Contains(entries[1].Text, "3.0s") {
		t.Errorf("entry 1 must show size and duration: %+v", entries[1])
	}
	if entries[1].Ref != raw[0] {
		t.Errorf("entry 1 ref = %q", entries[1].Ref)
	}
}

func TestRunnerProviderPicksUpConfigChanges(t *testing.T) {
	cfg := config.Default()
	p := newRunnerProvider(cfg)

	first, ok := p.New().(*downloader.Downloader)
	if !ok {
		t.Fatal("provider must build *downloader.Downloader")
	}
	if first.NumChunks != cfg.NumChunks {
		t.Fatalf("first runner chunks = %d", first.NumChunks)
	}

	// Settings change between jobs: the next runner picks it up, while the
	// transport (connection pool) is shared.
	cfg.NumChunks = 16
	cfg.OutputDir = "/tmp/somewhere-else"

	second, _ := p.New().(*downloader.Downloader)
	if second.NumChunks != 16 || second.OutputDir != "/tmp/somewhere-else" {
		t.Fatalf("second runner must see live config: chunks=%d dir=%s",
			second.NumChunks, second.OutputDir)
	}
	if first.Client != second.Client {
		t.Error("runners must share the HTTP transport")
	}
}

func TestSchedulerBuildsOneRunnerPerJob(t *testing.T) {
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	urls := []string{"https://x/1", "https://x/2"}
	if err := os.WriteFile(cfg.QueueFile, []byte(strings.Join(urls, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Config: cfg}

	builds := 0
	sched := NewScheduler(newQueueStore(ctx), urls, 1,
		func() DownloadRunner {
			builds++
			return okRunner{}
		},
		func(int) downloader.Reporter { return nopReporter{} },
		nil,
	)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := sched.Start(runCtx)

	sched.Control(-1, tui.ControlStartAll)
	waitFor(t, "run finished", func() bool { return closed(done) })

	if builds != len(urls) {
		t.Errorf("runner builds = %d, want %d (one per job)", builds, len(urls))
	}
}

func TestCompletedLineDisplayWithStats(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"2026-10-15 12:03:44 | https://x/FAB%20-%2029.mkv | 316757335 | 1m12.345s",
			"2026-10-15 12:03:44  FAB - 29.mkv  302.1 MB  1m12s",
		},
		// Legacy two-field lines still render (no size/duration columns).
		{
			"2026-10-11 10:01:22 | https://x/FAB-21.mkv",
			"2026-10-11 10:01:22  FAB-21.mkv",
		},
		// Bad numeric columns are simply omitted.
		{
			"2026-10-15 12:00:00 | https://x/a.bin | notanumber | nope",
			"2026-10-15 12:00:00  a.bin",
		},
	}
	for _, tc := range cases {
		if got := completedLineForDisplay(tc.in); got != tc.want {
			t.Errorf("completedLineForDisplay(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{12340 * time.Millisecond, "12.3s"},
		{72 * time.Second, "1m12s"},
		{64 * time.Minute, "1h04m"},
	}
	for _, tc := range cases {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStoreCompletedLineCarriesStats(t *testing.T) {
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	if err := os.WriteFile(cfg.QueueFile, []byte("https://x/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Config: cfg}
	store := newQueueStore(ctx)

	if err := store.completeAndRemove("https://x/a", 1234, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(cfg.CompletedFile)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	parts := strings.Split(line, "|")
	if len(parts) != 4 {
		t.Fatalf("completed line = %q, want 4 fields", line)
	}
	if strings.TrimSpace(parts[2]) != "1234" {
		t.Errorf("size field = %q", parts[2])
	}
	if got, err := time.ParseDuration(strings.TrimSpace(parts[3])); err != nil || got != 2*time.Second {
		t.Errorf("duration field = %q (%v)", parts[3], err)
	}
}

// TestBareDlmRunsTheUI: `dlm` with no args and no terminal (the test
// environment) prints the snapshot instead of help; `ui` is not a command
// any more.
func TestBareDlmRunsTheUI(t *testing.T) {
	queueFile := filepath.Join(t.TempDir(), "queue.txt")
	if err := os.WriteFile(queueFile, []byte("https://example.com/FAB-01.mkv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.QueueFile = queueFile
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	cfg.OutputDir = t.TempDir()

	ctx := &Context{Config: cfg}
	if err := runUI(ctx, true); err != nil {
		t.Fatalf("runUI: %v", err)
	}
}

func TestUICmdIsGone(t *testing.T) {
	state := &appState{ctx: &Context{Config: config.Default()}}
	root := newRootCmd(state)

	for _, c := range root.Commands() {
		if c.Name() == "ui" {
			t.Fatal("the ui subcommand must be removed: bare dlm is the board")
		}
	}
	if root.RunE == nil {
		t.Fatal("bare dlm must run the UI (root RunE)")
	}
	if root.Flags().Lookup("inline") == nil {
		t.Fatal("--inline must be available on bare dlm")
	}
}

func TestUISnapshotTilesAndCounts(t *testing.T) {
	queueFile := filepath.Join(t.TempDir(), "queue.txt")
	if err := os.WriteFile(queueFile, []byte(strings.Join([]string{
		"https://example.com/FAB-08.mkv",
		"https://example.com/FAB-09.mkv",
		"",
		"# a comment line should count as a URL in the pending status.",
		"https://example.com/FAB-10.mkv",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	completedFile := filepath.Join(t.TempDir(), "completed.txt")
	if err := os.WriteFile(completedFile, []byte(strings.Join([]string{
		"2026-10-11 10:01:22 | https://example.com/FAB-04.mkv",
		"2026-10-12 11:02:01 | https://example.com/FAB-05.mkv",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.QueueFile = queueFile
	cfg.CompletedFile = completedFile
	cfg.OutputDir = t.TempDir() // empty downloads dir

	out, err := uiSnapshot(&Context{Config: cfg})
	if err != nil {
		t.Fatalf("uiSnapshot: %v", err)
	}

	checks := []struct{ name, want string }{
		{"queue count", "3 pending"},
		{"queue first item", "FAB-08.mkv"},
		{"queue truncation", ""},
		{"completed count", "2 downloads"},
		{"latest completed first", "FAB-05.mkv"},
		{"empty downloads", "0 files"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: %q not found in snapshot:\n%s", c.name, c.want, out)
		}
	}
}

func TestUISnapshotEmptyAndMissingStates(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(dir, "does-not-exist.txt")
	cfg.CompletedFile = filepath.Join(dir, "no-completed.txt")
	cfg.OutputDir = filepath.Join(dir, "missing-downloads")

	out, err := uiSnapshot(&Context{Config: cfg})
	if err != nil {
		t.Fatalf("missing paths should not error: %v", err)
	}
	for _, want := range []string{"queue:      empty", "completed:  none", "(missing)"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q not in snapshot:\n%s", want, out)
		}
	}
}

func TestBoardSummaryTotals(t *testing.T) {
	outDir := t.TempDir()
	histDir := t.TempDir()
	cfg := config.Default()
	cfg.CompletedFile = filepath.Join(histDir, "completed.txt")
	cfg.OutputDir = outDir
	cfg.QueueFile = filepath.Join(histDir, "queue.txt")

	lines := []string{
		"2026-10-15 10:00:00 | https://x/a.mkv | 1048576 | 3s",
		"2026-10-15 11:00:00 | https://x/b.mkv | 2097152 | 2s",
		"2026-10-11 10:01:22 | https://x/legacy.mkv", // no size column
	}
	if err := os.WriteFile(cfg.CompletedFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.QueueFile, []byte("https://x/q1.mkv\nhttps://x/q2.mkv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "out.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := boardSummary(&Context{Config: cfg})
	if s.Count != 3 {
		t.Errorf("Count = %d, want 3", s.Count)
	}
	if want := int64(1048576 + 2097152); s.Bytes != want {
		t.Errorf("Bytes = %d, want %d", s.Bytes, want)
	}
	if s.Dir != outDir {
		t.Errorf("Dir = %q, want %q", s.Dir, outDir)
	}
	if s.Files != 1 || s.DirBytes != 5 {
		t.Errorf("dir stats = %d files, %d bytes, want 1 and 5", s.Files, s.DirBytes)
	}
	if s.Pending != 2 {
		t.Errorf("Pending = %d, want 2", s.Pending)
	}
	if !strings.Contains(s.Settings, "chunks=") || !strings.Contains(s.Settings, "queue_jobs=") {
		t.Errorf("Settings = %q, want a chunks/retries/queue_jobs recap", s.Settings)
	}
	if s.Platform == "" {
		t.Error("Platform must be set")
	}

	// A missing history/queue/output dir must not error: counts stay zero.
	cfg.CompletedFile = filepath.Join(histDir, "absent.txt")
	cfg.QueueFile = filepath.Join(histDir, "absent-queue.txt")
	cfg.OutputDir = filepath.Join(histDir, "absent-dir")
	s = boardSummary(&Context{Config: cfg})
	if s.Count != 0 || s.Bytes != 0 || s.Files != 0 || s.Pending != 0 {
		t.Errorf("missing paths must read as empty: %+v", s)
	}
}

func TestUISnapshotQueueTruncatesAtFive(t *testing.T) {
	cfg := config.Default()
	cfg.QueueFile = filepath.Join(t.TempDir(), "queue.txt")
	cfg.CompletedFile = filepath.Join(t.TempDir(), "completed.txt")
	cfg.OutputDir = t.TempDir()

	lines := []string{}
	for i := 0; i < 7; i++ {
		lines = append(lines, "https://example.com/file"+strings.Repeat("x", 1)+string(rune('0'+i))+".mkv")
	}
	if err := os.WriteFile(cfg.QueueFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := uiSnapshot(&Context{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	want := "… and 2 more" // 3 more, rendered as 7-5=2? unicode digits: it should use decimal.
	if !strings.Contains(out, want) {
		t.Errorf("want %q in snapshot:\n%s", want, out)
	}
}
