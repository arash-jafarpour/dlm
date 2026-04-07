package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"modules/config"
	"modules/downloader"
	"modules/reader"
)

func markCompleted(cfg *config.Config, urlStr string) error {
	// Append to completed.txt with timestamp
	f, err := os.OpenFile(cfg.CompletedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, urlStr)
	return err
}

func removeFromLinks(cfg *config.Config, urlStr string) error {
	// Read all links
	data, err := os.ReadFile(cfg.QueueFile)
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
	return os.WriteFile(cfg.QueueFile, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

func main() {
	// start with safe defaults
	cfg := config.Default()

	// override with flags
	flag.StringVar(&cfg.QueueFile, "queue", cfg.QueueFile, "path to queue file")
	flag.StringVar(&cfg.OutputDir, "out", cfg.OutputDir, "download output directory")
	flag.IntVar(&cfg.NumChunks, "chunks", cfg.NumChunks, "number of parallel chunks")
	flag.BoolVar(
		&cfg.InsecureSkipVerify,
		"insecure",
		cfg.InsecureSkipVerify,
		"skip TLS verification",
	)
	flag.StringVar(&cfg.SingleURL, "url", "", "download a single URL directly")
	flag.Parse()

	// validate before doing anything
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(1)
	}

	dl := downloader.New(cfg)

	if cfg.SingleURL != "" {
		fmt.Printf("→ downloading: %s\n", cfg.SingleURL)
		completed, err := dl.Download(cfg.SingleURL)
		if err != nil {
			fmt.Printf("✗ failed: %v\n", err)
			os.Exit(1)
		}
		if completed {
			fmt.Println("✓ done")
		}
		return
	}

	lf, err := reader.ReadLinks(cfg.QueueFile)
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
			if err := markCompleted(cfg, urlStr); err != nil {
				fmt.Printf("Warning: couldn't mark as completed: %v\n", err)
			}
			if err := removeFromLinks(cfg, urlStr); err != nil {
				fmt.Printf("Warning: couldn't remove from queue.txt: %v\n", err)
			}
		}
	}
}
