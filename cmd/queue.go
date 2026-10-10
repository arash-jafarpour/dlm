package cmd

import (
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
				queueAdd(ctx, args[0])
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all URLs in the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				queueList(ctx)
				return nil
			},
		},
		&cobra.Command{
			Use:   "clear",
			Short: "Clear all URLs from the queue",
			RunE: func(cmd *cobra.Command, args []string) error {
				queueClear(ctx)
				return nil
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

func queueAdd(ctx *Context, url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		fmt.Printf("%s \n", ui.Red("✗ URL cannot be empty:"))
		os.Exit(1)
	}

	f, err := os.OpenFile(ctx.Config.QueueFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to open queue file:"), err)
		os.Exit(1)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, url); err != nil {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to write to queue:"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", ui.Green("✓ added to queue:"), url)
}

func queueList(ctx *Context) {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("queue is empty")
			return
		}
		fmt.Printf("%s %v\n", ui.Red("✗ failed to read queue:"), err)
		os.Exit(1)
	}

	if len(lf.Links) == 0 {
		fmt.Println("queue is empty")
		return
	}

	for i, link := range lf.Links {
		fmt.Printf("%d. %s\n", i+1, link)
	}
}

func queueClear(ctx *Context) {
	queueClearWithConfirm(ctx, true)
}

func queueClearWithConfirm(ctx *Context, requireConfirm bool) {
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	if err != nil && !os.IsNotExist(err) {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to read queue:"), err)
		os.Exit(1)
	}

	if lf == nil || len(lf.Links) == 0 {
		fmt.Println("queue is already empty")
		return
	}

	if requireConfirm {
		fmt.Printf("This will remove %d item(s) from the queue. Continue? [y/N]: ", len(lf.Links))
		var input string
		fmt.Scanln(&input)
		input = strings.ToLower(strings.TrimSpace(input))

		if input != "y" && input != "yes" {
			fmt.Println("operation cancelled")
			return
		}
	}

	if err := os.WriteFile(ctx.Config.QueueFile, []byte(""), 0o644); err != nil {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to clear queue:"), err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", ui.Green("✓ queue cleared"))
}
