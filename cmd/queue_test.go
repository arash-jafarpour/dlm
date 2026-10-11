package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/arash-jafarpour/dlm/config"
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
	ctx := &Context{
		Config: cfg,
	}

	// Add a URL
	if err := queueAdd(ctx, "https://example.com"); err != nil {
		t.Fatalf("queueAdd failed: %v", err)
	}

	// Verify it was written
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

	// Write test data
	testData := "https://example.com\nhttps://test.com\n"
	if _, err := tmpFile.WriteString(testData); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{
		Config: cfg,
	}
	if err := queueList(ctx); err != nil {
		t.Fatalf("queueList failed: %v", err)
	}
}

func TestQueueClear(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write test data
	testData := "https://example.com\nhttps://test.com\n"
	if err := os.WriteFile(tmpFile.Name(), []byte(testData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{
		Config: cfg,
	}

	if err := queueClearWithConfirm(ctx, false); err != nil { // Skip confirmation
		t.Fatalf("queueClearWithConfirm failed: %v", err)
	}

	// Verify file is empty
	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if len(content) != 0 {
		t.Errorf("expected empty file, got: %s", content)
	}
}
