package cmd

import (
	"fmt"
	"testing"
)

// Mock downloader for testing
type mockDownloader struct {
	downloadedURLs []string
	shouldFail     bool
	shouldComplete bool
}

func (m *mockDownloader) Download(url string) (bool, error) {
	m.downloadedURLs = append(m.downloadedURLs, url)
	if m.shouldFail {
		return false, fmt.Errorf("mock download error")
	}
	return m.shouldComplete, nil
}

func TestDownloadURL(t *testing.T) {
	// mock := &mockDownloader{shouldComplete: true}
	// ctx := &Context{
	// 	Config:     &config.Config{},
	// 	Downloader: mock,
	// }
	//
	// url := "https://example.com/file.zip"
	// downloadURL(ctx, url)
	//
	// if len(mock.downloadedURLs) != 1 {
	// 	t.Fatalf("expected 1 download, got %d", len(mock.downloadedURLs))
	// }
	// if mock.downloadedURLs[0] != url {
	// 	t.Errorf("expected URL %q, got %q", url, mock.downloadedURLs[0])
	// }
}

func TestDownloadQueue(t *testing.T) {
	// tmpQueue, err := os.CreateTemp("", "queue-*.txt")
	// if err != nil {
	// 	t.Fatalf("failed to create temp queue: %v", err)
	// }
	// defer os.Remove(tmpQueue.Name())
	//
	// tmpCompleted, err := os.CreateTemp("", "completed-*.txt")
	// if err != nil {
	// 	t.Fatalf("failed to create temp completed: %v", err)
	// }
	// defer os.Remove(tmpCompleted.Name())
	//
	// testURLs := []string{
	// 	"https://example.com/file1.zip",
	// 	"https://example.com/file2.zip",
	// 	"https://example.com/file3.zip",
	// }
	// queueContent := strings.Join(testURLs, "\n") + "\n"
	// if err := os.WriteFile(tmpQueue.Name(), []byte(queueContent), 0o644); err != nil {
	// 	t.Fatalf("failed to write queue: %v", err)
	// }
	//
	// mock := &mockDownloader{shouldComplete: true}
	// ctx := &Context{
	// 	Config: &config.Config{
	// 		QueueFile:     tmpQueue.Name(),
	// 		CompletedFile: tmpCompleted.Name(),
	// 	},
	// 	Downloader: mock,
	// }
	//
	// downloadQueue(ctx)
	//
	// if len(mock.downloadedURLs) != len(testURLs) {
	// 	t.Errorf("expected %d downloads, got %d", len(testURLs), len(mock.downloadedURLs))
	// }
	//
	// queueData, err := os.ReadFile(tmpQueue.Name())
	// if err != nil {
	// 	t.Fatalf("failed to read queue: %v", err)
	// }
	// remainingLines := strings.TrimSpace(string(queueData))
	// if remainingLines != "" {
	// 	t.Errorf("expected empty queue, got: %q", remainingLines)
	// }
	//
	// completedData, err := os.ReadFile(tmpCompleted.Name())
	// if err != nil {
	// 	t.Fatalf("failed to read completed: %v", err)
	// }
	// completedContent := string(completedData)
	// for _, url := range testURLs {
	// 	if !strings.Contains(completedContent, url) {
	// 		t.Errorf("completed file missing URL: %s", url)
	// 	}
	// }
}
