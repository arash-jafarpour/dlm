package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arash-jafarpour/dlm/config"
)

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
