package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"dlm/reader"
	"dlm/ui"
)

func downloadCmd(ctx *Context, args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		if len(args) > 1 {
			generateCommandUsage([]string{"download", args[1]})
		} else {
			generateCommandUsage([]string{"download"})
		}
		return
	}

	if len(args) < 1 {
		fmt.Println("download subcommands: url | queue")
		fmt.Println("Run 'dlm download --help' for more information")
		os.Exit(1)
	}

	switch args[0] {
	case "url":
		if len(args) < 2 {
			// fmt.Println("usage: dlm download url <url>")
			fmt.Println("usage: dlm download url <url> [flags]")
			os.Exit(1)
		}
		// downloadURL(ctx, args[1:])
		downloadURL(ctx, args[1])
	case "queue":
		// downloadQueue(ctx, args[1:])
		downloadQueue(ctx)
	default:
		fmt.Printf("unknown download subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// func downloadURL(ctx *Context, args []string) {
// 	if len(args) < 1 {
// 		fmt.Println("usage: dlm download url <url> [flags]")
// 		os.Exit(1)
// 	}
//
// 	fs := flag.NewFlagSet("download-url", flag.ExitOnError)
// 	fs.StringVar(&ctx.Config.QueueFile, "queue", ctx.Config.QueueFile, "path to queue file")
// 	fs.StringVar(&ctx.Config.OutputDir, "out", ctx.Config.OutputDir, "output directory")
// 	fs.IntVar(&ctx.Config.NumChunks, "chunks", ctx.Config.NumChunks, "number of parallel chunks")
// 	fs.BoolVar(
// 		&ctx.Config.InsecureSkipVerify,
// 		"insecure",
// 		ctx.Config.InsecureSkipVerify,
// 		"skip TLS verification",
// 	)
// 	fs.Parse(args[1:])
//
// 	url := strings.TrimSpace(args[0])
// 	if url == "" {
// 		fmt.Println("error: URL cannot be empty")
// 		os.Exit(1)
// 	}
//
// 	fmt.Printf("%s", ui.Cyan("→ downloading "))
// 	completed, err := ctx.Downloader.Download(url)
// 	if err != nil {
// 		logError(err)
// 		os.Exit(1)
// 	}
// 	if completed {
// 		fmt.Printf("%s\n", ui.Green("✓ done"))
// 	}
// }
//
// func downloadQueue(ctx *Context, args []string) {
// 	fs := flag.NewFlagSet("download-queue", flag.ExitOnError)
// 	fs.StringVar(&ctx.Config.QueueFile, "queue", ctx.Config.QueueFile, "path to queue file")
// 	fs.StringVar(&ctx.Config.OutputDir, "out", ctx.Config.OutputDir, "output directory")
// 	fs.IntVar(&ctx.Config.NumChunks, "chunks", ctx.Config.NumChunks, "number of parallel chunks")
// 	fs.BoolVar(
// 		&ctx.Config.InsecureSkipVerify,
// 		"insecure",
// 		ctx.Config.InsecureSkipVerify,
// 		"skip TLS verification",
// 	)
// 	fs.Parse(args)
//
// 	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
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
// 		fmt.Printf("%s", ui.Cyan("→ downloading "))
//
// 		completed, err := ctx.Downloader.Download(urlStr)
// 		if err != nil {
// 			logError(err)
// 			continue
// 		}
//
// 		if completed {
// 			if err := markCompleted(ctx, urlStr); err != nil {
// 				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't mark as completed:"), err)
// 			}
// 			if err := removeFromQueue(ctx, urlStr); err != nil {
// 				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't remove from queue:"), err)
// 			}
// 		}
// 	}
// }

func downloadURL(ctx *Context, url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		fmt.Println("error: URL cannot be empty")
		os.Exit(1)
	}

	fmt.Printf("%s", ui.Cyan("→ downloading "))
	completed, err := ctx.Downloader.Download(url)
	if err != nil {
		logError(err)
		os.Exit(1)
	}
	if completed {
		fmt.Printf("%s\n", ui.Green("✓ done"))
	}
}

func downloadQueue(ctx *Context) {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
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

		completed, err := ctx.Downloader.Download(urlStr)
		if err != nil {
			logError(err)
			continue
		}

		if completed {
			if err := markCompleted(ctx, urlStr); err != nil {
				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't mark as completed:"), err)
			}
			if err := removeFromQueue(ctx, urlStr); err != nil {
				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't remove from queue:"), err)
			}
		}
	}
}

func markCompleted(ctx *Context, urlStr string) error {
	f, err := os.OpenFile(ctx.Config.CompletedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s | %s\n", timestamp, urlStr)
	return err
}

func removeFromQueue(ctx *Context, urlStr string) error {
	data, err := os.ReadFile(ctx.Config.QueueFile)
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

	return os.WriteFile(ctx.Config.QueueFile, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}
