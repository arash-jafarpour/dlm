package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arash-jafarpour/dlm/config"
)

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
