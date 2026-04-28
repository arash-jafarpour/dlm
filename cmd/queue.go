package cmd

import (
	"fmt"
	"os"
	"strings"

	"dlm/reader"
	"dlm/ui"
)

func queueCmd(ctx *Context, args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		if len(args) > 1 {
			generateCommandUsage([]string{"queue", args[1]})
		} else {
			generateCommandUsage([]string{"queue"})
		}
		return
	}

	if len(args) < 1 {
		fmt.Println("queue subcommands: add, list, clear")
		fmt.Println("Run 'dlm queue --help' for more information")
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Println("usage: dlm queue add <url>")
			os.Exit(1)
		}
		queueAdd(ctx, args[1])
	case "list":
		queueList(ctx)
	case "clear":
		queueClear(ctx)
	default:
		fmt.Printf("unknown queue subcommand: %s\n", args[0])
		os.Exit(1)
	}
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
