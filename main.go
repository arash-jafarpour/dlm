package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"modules/downloader"
	"modules/reader"
)

func markCompleted(urlstr string) error {
	// Append to completed.txt with timestamp
	f, err := os.OpenFile("completed.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, urlstr)
	return err
}

func removeFromLinks(urlstr string) error {
	// Read all links
	data, err := os.ReadFile("queue.txt")
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != urlstr && line != "" {
			kept = append(kept, line)
		}
	}

	// Write back
	return os.WriteFile("queue.txt", []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

func main() {
	links, err := reader.ReadLinks("queue.txt")
	if err != nil {
		panic(fmt.Sprintf("error reading links: %v", err))
	}
	if len(links.Links) == 0 {
		fmt.Println("no links found in queue to download")
		return
	}

	dl := downloader.New("./downloads")

	for _, url := range links.Links {
		fmt.Printf("→ downloading: %s\n", url)

		completed, err := dl.Download(url)
		if err != nil {
			fmt.Printf("✗ failed: %v\n", err)
			continue
		}

		if completed {
			// Only mark as completed if actually downloaded
			if err := markCompleted(url); err != nil {
				fmt.Printf("Warning: couldn't mark as completed: %v\n", err)
			}
			if err := removeFromLinks(url); err != nil {
				fmt.Printf("Warning: couldn't remove from queue.txt: %v\n", err)
			}
		}
	}
}
