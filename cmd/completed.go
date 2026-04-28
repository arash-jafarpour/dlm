package cmd

import (
	"fmt"
	"os"

	"dlm/ui"
)

func completedCmd(ctx *Context, args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		if len(args) > 1 {
			generateCommandUsage([]string{"completed", args[1]})
		} else {
			generateCommandUsage([]string{"completed"})
		}
		return
	}

	if len(args) < 1 {
		fmt.Println("completed subcommands: clear")
		fmt.Println("Run 'dlm completed --help' for more information")
		os.Exit(1)
	}

	switch args[0] {
	case "clear":
		completedClear(ctx)
	default:
		fmt.Printf("unknown completed subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func completedClear(ctx *Context) {
	if err := os.WriteFile(ctx.Config.CompletedFile, []byte(""), 0o644); err != nil {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to clear completed:"), err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", ui.Green("✓ completed list cleared"))
}
