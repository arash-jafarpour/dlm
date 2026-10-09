package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"dlm/reader"
	"dlm/ui"

	"github.com/spf13/cobra"
)

func newDownloadCmd(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download files",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:               "url <url>",
			Short:             "Download a single URL",
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: noFileCompletion,
			RunE: func(cmd *cobra.Command, args []string) error {
				downloadURL(ctx, args[0])
				return nil
			},
		},
		&cobra.Command{
			Use:   "queue",
			Short: "Download all URLs from the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				downloadQueue(ctx)
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Show output directory path",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(ctx.Config.OutputDir)
				return nil
			},
		},
	)

	return cmd
}

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
