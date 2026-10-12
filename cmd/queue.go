package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/arash-jafarpour/dlm/ui"
	"github.com/spf13/cobra"
)

func newQueueCmd(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage download queue",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:               "add <url>",
			Short:             "Add a URL to the queue",
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: noFileCompletion,
			RunE: func(cmd *cobra.Command, args []string) error {
				return queueAdd(ctx, args[0])
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all URLs in the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				return queueList(ctx)
			},
		},
		&cobra.Command{
			Use:   "clear",
			Short: "Clear all URLs from the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				return queueClear(ctx)
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Show queue file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(ctx.Config.QueueFile)
				return nil
			},
		},
	)

	return cmd
}

func queueAdd(ctx *Context, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("URL cannot be empty")
	}

	if err := newQueueStore(ctx).append(url); err != nil {
		return err
	}

	fmt.Printf("%s %s\n", ui.Green("✓ added to queue:"), url)
	return nil
}

func queueList(ctx *Context) error {
	urls, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		return err
	}

	if len(urls) == 0 {
		fmt.Println("queue is empty")
		return nil
	}

	for i, link := range urls {
		fmt.Printf("%d. %s\n", i+1, link)
	}
	return nil
}

func queueClear(ctx *Context) error {
	return queueClearWithConfirm(ctx, true)
}

func queueClearWithConfirm(ctx *Context, requireConfirm bool) error {
	urls, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		return err
	}

	if len(urls) == 0 {
		fmt.Println("queue is already empty")
		return nil
	}

	if requireConfirm {
		fmt.Printf("This will remove %d item(s) from the queue. Continue? [y/N]: ", len(urls))
		var input string
		_, _ = fmt.Scanln(&input) // prompt; an empty/failed input falls through to the [y/N] default
		input = strings.ToLower(strings.TrimSpace(input))

		if input != "y" && input != "yes" {
			fmt.Println("operation cancelled")
			return nil
		}
	}

	if err := os.WriteFile(ctx.Config.QueueFile, []byte(""), 0o644); err != nil {
		return fmt.Errorf("failed to clear queue: %w", err)
	}

	fmt.Printf("%s\n", ui.Green("✓ queue cleared"))
	return nil
}
