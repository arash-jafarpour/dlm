package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/arash-jafarpour/dlm/reader"
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

	f, err := os.OpenFile(ctx.Config.QueueFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open queue file: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, url); err != nil {
		return fmt.Errorf("failed to write to queue: %w", err)
	}

	fmt.Printf("%s %s\n", ui.Green("✓ added to queue:"), url)
	return nil
}

func queueList(ctx *Context) error {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("queue is empty")
			return nil
		}
		return fmt.Errorf("failed to read queue: %w", err)
	}

	if len(lf.Links) == 0 {
		fmt.Println("queue is empty")
		return nil
	}

	for i, link := range lf.Links {
		fmt.Printf("%d. %s\n", i+1, link)
	}
	return nil
}

func queueClear(ctx *Context) error {
	return queueClearWithConfirm(ctx, true)
}

func queueClearWithConfirm(ctx *Context, requireConfirm bool) error {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read queue: %w", err)
	}

	if lf == nil || len(lf.Links) == 0 {
		fmt.Println("queue is already empty")
		return nil
	}

	if requireConfirm {
		fmt.Printf("This will remove %d item(s) from the queue. Continue? [y/N]: ", len(lf.Links))
		var input string
		fmt.Scanln(&input)
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
