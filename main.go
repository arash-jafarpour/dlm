package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"modules/downloader"
	"modules/reader"
)

func markCompleted(urlStr string) error {
	// Append to completed.txt with timestamp
	f, err := os.OpenFile("completed.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, urlStr)
	return err
}

func removeFromLinks(urlStr string) error {
	// Read all links
	data, err := os.ReadFile("queue.txt")
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != urlStr && line != "" {
			kept = append(kept, line)
		}
	}

	// Write back
	return os.WriteFile("queue.txt", []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

func main() {
	// --- flags ---
	queueFile := flag.String("queue", "queue.txt", "path to queue file")
	outputDir := flag.String("out", "./downloads", "output directory")
	singleURL := flag.String("url", "", "download a single URL directly")
	chunks := flag.Int("chunks", 8, "number of parallel chunks")
	flag.Parse()

	dl := downloader.New(*outputDir, *chunks)

	// single URL mode
	if *singleURL != "" {
		fmt.Printf("→ downloading: %s\n", *singleURL)
		completed, err := dl.Download(*singleURL)
		if err != nil {
			fmt.Printf("✗ failed: %v\n", err)
			os.Exit(1)
		}
		if completed {
			fmt.Println("✓ done")
		}
		return
	}

	// auto / queue mode (existing behavior)
	lf, err := reader.ReadLinks(*queueFile)
	if err != nil {
		fmt.Printf("error reading queue: %v\n", err)
		os.Exit(1)
	}
	if len(lf.Links) == 0 {
		fmt.Println("no links found in queue")
		return
	}

	for _, urlStr := range lf.Links {
		fmt.Printf("→ downloading: %s\n", urlStr)

		completed, err := dl.Download(urlStr)
		if err != nil {
			fmt.Printf("✗ failed: %v\n", err)
			continue
		}

		if completed {
			// Only mark as completed if actually downloaded
			if err := markCompleted(urlStr); err != nil {
				fmt.Printf("Warning: couldn't mark as completed: %v\n", err)
			}
			if err := removeFromLinks(urlStr); err != nil {
				fmt.Printf("Warning: couldn't remove from queue.txt: %v\n", err)
			}
		}
	}
}
