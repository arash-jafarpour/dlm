package cmd

import (
	"os"
	"strings"
	"testing"

	"dlm/config"
)

func TestQueueAdd(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	cfg := config.Default()
	cfg.QueueFile = tmpFile.Name()
	ctx := &Context{
		Config: cfg,
	}

	// Add a URL
	queueAdd(ctx, "https://example.com")

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
	defer os.Remove(tmpFile.Name())

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
	queueList(ctx)
}

func TestQueueClear(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

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

	queueClearWithConfirm(ctx, false) // Skip confirmation

	// Verify file is empty
	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if len(content) != 0 {
		t.Errorf("expected empty file, got: %s", content)
	}
}
