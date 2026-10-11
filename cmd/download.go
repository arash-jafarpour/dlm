package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/arash-jafarpour/dlm/reader"
	"github.com/arash-jafarpour/dlm/ui"

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
				return downloadURL(ctx, args[0])
			},
		},
		&cobra.Command{
			Use:   "queue",
			Short: "Download all URLs from the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				return downloadQueue(ctx)
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

func downloadURL(ctx *Context, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	fmt.Printf("%s", ui.Cyan("→ downloading\n"))
	completed, err := ctx.Downloader.Download(url)
	if err != nil {
		return err
	}
	if completed {
		fmt.Printf("%s\n", ui.Green("✓ done"))
	}
	return nil
}

func downloadQueue(ctx *Context) error {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	if err != nil {
		return err
	}
	if len(lf.Links) == 0 {
		fmt.Println("no links found in queue")
		return nil
	}

	for i, urlStr := range lf.Links {
		// A blank line separates one file's result block from the next
		// download, for readability in queue mode.
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s", ui.Cyan("→ downloading\n"))

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
	return nil
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
