package cmd

import (
	"os"
	"testing"

	"dlm/config"
)

func TestCompletedClear(t *testing.T) {
	// Create a temporary completed file with some content
	tmpFile, err := os.CreateTemp("", "completed-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write some initial content
	initialContent := "https://example.com/file1.zip\nhttps://example.com/file2.zip\n"
	if err := os.WriteFile(tmpFile.Name(), []byte(initialContent), 0o644); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}

	// Create context with the temp file
	ctx := &Context{
		Config: &config.Config{
			CompletedFile: tmpFile.Name(),
		},
	}

	// Call completedClear
	completedClear(ctx)

	// Verify the file is now empty
	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to read completed file: %v", err)
	}

	if len(content) != 0 {
		t.Errorf("expected empty file, got %d bytes: %q", len(content), string(content))
	}
}
