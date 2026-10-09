package cmd

import (
	"fmt"
	"os"

	"dlm/ui"

	"github.com/spf13/cobra"
)

func newCompletedCmd(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completed",
		Short: "Manage completed downloads",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "clear",
			Short: "Clear completed downloads list",
			RunE: func(cmd *cobra.Command, args []string) error {
				completedClear(ctx)
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Show completed file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(ctx.Config.CompletedFile)
				return nil
			},
		},
	)

	return cmd
}

func completedClear(ctx *Context) {
	if err := os.WriteFile(ctx.Config.CompletedFile, []byte(""), 0o644); err != nil {
		fmt.Printf("%s %v\n", ui.Red("✗ failed to clear completed:"), err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", ui.Green("✓ completed list cleared"))
}
