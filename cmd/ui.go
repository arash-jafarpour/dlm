package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/arash-jafarpour/dlm/reader"
	"github.com/arash-jafarpour/dlm/ui"
	"github.com/spf13/cobra"
)

func newUICmd(ctx *Context) *cobra.Command {
	return &cobra.Command{
		Use:               "ui",
		Short:             "Show a status snapshot; the interactive board lands in v0.6",
		Args:              cobra.NoArgs,
		ValidArgsFunction: noFileCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := uiSnapshot(ctx)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

// uiSnapshot renders the plain-text dashboard snapshot this command shows
// while the interactive board is not built yet: pending queue, last
// completed entries, the downloads directory state, and the running
// configuration.
func uiSnapshot(ctx *Context) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", ui.Cyan("dlm dashboard"))
	fmt.Fprintf(&b, "%s\n", ui.Gray(strings.Repeat("─", 46)))

	// Pending queue
	lf, err := reader.ReadLinks(ctx.Config.QueueFile)
	switch {
	// errors.Is unwraps through FileError; os.IsNotExist would not.
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	case err != nil || len(lf.Links) == 0:
		fmt.Fprintf(&b, "queue:      %s\n", ui.Gray("empty"))
	default:
		fmt.Fprintf(&b, "queue:      %s\n", ui.Bold(fmt.Sprintf("%d pending", len(lf.Links))))
		for i, link := range lf.Links {
			if i >= 5 {
				fmt.Fprintf(&b, "            … and %d more\n", len(lf.Links)-5)
				break
			}
			fmt.Fprintf(&b, "            %d. %s\n", i+1, link)
		}
	}

	// Completed history: count and the most recent entries. Lines are
	// "timestamp | url" as written by markCompleted.
	if data, err := os.ReadFile(ctx.Config.CompletedFile); err == nil && len(data) > 0 {
		lines := nonEmptyLines(string(data))
		fmt.Fprintf(&b, "completed:  %s\n", ui.Bold(fmt.Sprintf("%d downloads", len(lines))))
		for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
			fmt.Fprintf(&b, "            %s\n", truncateLine(lines[i], 64))
		}
	} else {
		fmt.Fprintf(&b, "completed:  %s\n", ui.Gray("none"))
	}

	fmt.Fprintf(&b, "downloads:  %s\n", downloadsDirSummary(ctx.Config.OutputDir))

	// Configuration at a glance.
	fmt.Fprintf(&b, "settings:   chunks=%d retries=%d out=%s (%s/%s)\n",
		ctx.Config.NumChunks, ctx.Config.MaxRetries, ctx.Config.OutputDir,
		runtime.GOOS, runtime.GOARCH)

	fmt.Fprintf(&b, "\n%s\n", ui.Gray(
		"interactive board (live progress, pause/resume) ships in v0.6; `dlm --help` for everything available now",
	))
	return b.String(), nil
}

// downloadsDirSummary describes the downloads directory: file count and
// total size, "empty"/"missing" instead of errors when it does not matter.
func downloadsDirSummary(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("%s (missing)", dir)
		}
		return dir // unreadable: show the plain path
	}
	var count int
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		count++
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return fmt.Sprintf("%d files, %s in %s", count, humanBytes(total), dir)
}

// humanBytes mirrors ui.formatBytes; it is a separate copy because
// formatBytes is unexported in package ui and this is presentation code
// waiting for the v0.6 board refactor (which will lift the shared
// formatting into one place).
func humanBytes(total int64) string {
	switch {
	case total >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(total)/(1<<30))
	case total >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(total)/(1<<20))
	case total >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(total)/(1<<10))
	default:
		return fmt.Sprintf("%d B", total)
	}
}

func nonEmptyLines(data string) []string {
	var lines []string
	for _, l := range strings.Split(data, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// truncateLine shortens a line, keeping its tail (the URL part of
// completed entries matters more than the timestamp).
func truncateLine(line string, max int) string {
	if len(line) <= max {
		return line
	}
	return "…" + line[len(line)-max+1:]
}
