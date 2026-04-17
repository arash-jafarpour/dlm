package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dlm/config"
	"dlm/downloader"
	apperrors "dlm/errors"
	"dlm/reader"
	"dlm/ui"
)

func markCompleted(cfg *config.Config, urlStr string) error {
	// Append to completed.txt with timestamp
	f, err := os.OpenFile(cfg.CompletedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return apperrors.WrapFileError(cfg.CompletedFile, "open", err)
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
		return apperrors.WrapFileError(cfg.QueueFile, "read", err)
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

func logError(err error) {
	var httpErr *apperrors.HTTPError
	var fileErr *apperrors.FileError
	var chunkErr *apperrors.ChunkError

	switch {
	case errors.As(err, &chunkErr):
		if chunkErr.Index == -1 {
			fmt.Printf("%s %v\n", ui.Red("✗ merge failed:"), chunkErr.Err)
		} else {
			fmt.Printf("%s %v\n", ui.Red(fmt.Sprintf("✗ chunk %d failed:", chunkErr.Index)), chunkErr.Err)
		}
	case errors.As(err, &httpErr):
		if httpErr.StatusCode > 0 {
			fmt.Printf(
				"%s %s: %s\n",
				ui.Red(fmt.Sprintf("✗ HTTP %d for", httpErr.StatusCode)),
				httpErr.URL,
				httpErr.Message,
			)
		} else {
			fmt.Printf("%s %s: %s\n", ui.Red("✗ network error for"), httpErr.URL, httpErr.Message)
		}
	case errors.As(err, &fileErr):
		fmt.Printf(
			"%s %q: %v\n",
			ui.Red(fmt.Sprintf("✗ file %s error on", fileErr.Op)),
			fileErr.Path,
			fileErr.Err,
		)
	default:
		fmt.Printf("%s %v\n", ui.Red("✗ failed:"), err)
	}
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
		fmt.Fprintf(os.Stderr, "%s %v\n", ui.Red("✗ invalid config:"), err)
		os.Exit(1)
	}

	dl := downloader.New(cfg)

	if cfg.SingleURL != "" {
		fmt.Printf("%s", ui.Cyan("→ downloading "))
		completed, err := dl.Download(cfg.SingleURL)
		if err != nil {
			logError(err)
			os.Exit(1)
		}
		if completed {
			fmt.Printf("%s\n", ui.Green("✓ done"))
		}
		return
	}

	lf, err := reader.ReadLinks(cfg.QueueFile)
	if err != nil {
		logError(err)
		os.Exit(1)
	}
	if len(lf.Links) == 0 {
		fmt.Println("no links found in queue")
		return
	}

	for _, urlStr := range lf.Links {
		fmt.Printf("%s", ui.Cyan("→ downloading "))

		completed, err := dl.Download(urlStr)
		if err != nil {
			logError(err)
			continue
		}

		if completed {
			// Only mark as completed if actually downloaded
			if err := markCompleted(cfg, urlStr); err != nil {
				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't mark as completed:"), err)
			}
			if err := removeFromLinks(cfg, urlStr); err != nil {
				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't remove from queue.txt:"), err)
			}
		}
	}
}

// package main
//
// import (
// 	"errors"
// 	"flag"
// 	"fmt"
// 	"os"
// 	"strings"
// 	"time"
//
// 	"dlm/config"
// 	"dlm/downloader"
// 	apperrors "dlm/errors"
// 	"dlm/reader"
// )
//
// func markCompleted(cfg *config.Config, urlStr string) error {
// 	// Append to completed.txt with timestamp
// 	f, err := os.OpenFile(cfg.CompletedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
// 	if err != nil {
// 		return apperrors.WrapFileError(cfg.CompletedFile, "open", err)
// 	}
// 	defer f.Close()
//
// 	timestamp := time.Now().Format("2006-01-02 15:04:05")
// 	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, urlStr)
// 	return err
// }
//
// func removeFromLinks(cfg *config.Config, urlStr string) error {
// 	// Read all links
// 	data, err := os.ReadFile(cfg.QueueFile)
// 	if err != nil {
// 		return apperrors.WrapFileError(cfg.QueueFile, "read", err)
// 	}
//
// 	lines := strings.Split(string(data), "\n")
// 	var kept []string
// 	for _, line := range lines {
// 		line = strings.TrimSpace(line)
// 		if line != urlStr && line != "" {
// 			kept = append(kept, line)
// 		}
// 	}
//
// 	// Write back
// 	return os.WriteFile(cfg.QueueFile, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
// }
//
// func logError(err error) {
// 	var httpErr *apperrors.HTTPError
// 	var fileErr *apperrors.FileError
// 	var chunkErr *apperrors.ChunkError
//
// 	switch {
// 	case errors.As(err, &chunkErr):
// 		if chunkErr.Index == -1 {
// 			fmt.Printf("✗ merge failed: %v\n", chunkErr.Err)
// 		} else {
// 			fmt.Printf("✗ chunk %d failed: %v\n", chunkErr.Index, chunkErr.Err)
// 		}
// 	case errors.As(err, &httpErr):
// 		if httpErr.StatusCode > 0 {
// 			fmt.Printf("✗ HTTP %d for %s: %s\n", httpErr.StatusCode, httpErr.URL, httpErr.Message)
// 		} else {
// 			fmt.Printf("✗ network error for %s: %s\n", httpErr.URL, httpErr.Message)
// 		}
// 	case errors.As(err, &fileErr):
// 		fmt.Printf("✗ file %s error on %q: %v\n", fileErr.Op, fileErr.Path, fileErr.Err)
// 	default:
// 		fmt.Printf("✗ failed: %v\n", err)
// 	}
// }
//
// func main() {
// 	// start with safe defaults
// 	cfg := config.Default()
//
// 	// override with flags
// 	flag.StringVar(&cfg.QueueFile, "queue", cfg.QueueFile, "path to queue file")
// 	flag.StringVar(&cfg.OutputDir, "out", cfg.OutputDir, "download output directory")
// 	flag.IntVar(&cfg.NumChunks, "chunks", cfg.NumChunks, "number of parallel chunks")
// 	flag.BoolVar(
// 		&cfg.InsecureSkipVerify,
// 		"insecure",
// 		cfg.InsecureSkipVerify,
// 		"skip TLS verification",
// 	)
// 	flag.StringVar(&cfg.SingleURL, "url", "", "download a single URL directly")
// 	flag.Parse()
//
// 	// validate before doing anything
// 	if err := cfg.Validate(); err != nil {
// 		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
// 		os.Exit(1)
// 	}
//
// 	dl := downloader.New(cfg)
//
// 	if cfg.SingleURL != "" {
// 		fmt.Printf("→ downloading: %s\n", cfg.SingleURL)
// 		completed, err := dl.Download(cfg.SingleURL)
// 		if err != nil {
// 			logError(err)
// 			os.Exit(1)
// 		}
// 		if completed {
// 			fmt.Println("✓ done")
// 		}
// 		return
// 	}
//
// 	lf, err := reader.ReadLinks(cfg.QueueFile)
// 	if err != nil {
// 		logError(err)
// 		os.Exit(1)
// 	}
// 	if len(lf.Links) == 0 {
// 		fmt.Println("no links found in queue")
// 		return
// 	}
//
// 	for _, urlStr := range lf.Links {
// 		fmt.Printf("→ downloading: %s\n", urlStr)
//
// 		completed, err := dl.Download(urlStr)
// 		if err != nil {
// 			logError(err)
// 			continue
// 		}
//
// 		if completed {
// 			// Only mark as completed if actually downloaded
// 			if err := markCompleted(cfg, urlStr); err != nil {
// 				fmt.Printf("Warning: couldn't mark as completed: %v\n", err)
// 			}
// 			if err := removeFromLinks(cfg, urlStr); err != nil {
// 				fmt.Printf("Warning: couldn't remove from queue.txt: %v\n", err)
// 			}
// 		}
// 	}
// }
