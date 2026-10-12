package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/internal/tui"
	"github.com/arash-jafarpour/dlm/reader"
	"github.com/arash-jafarpour/dlm/ui"
	"github.com/spf13/cobra"
)

func newUICmd(ctx *Context) *cobra.Command {
	var inline bool

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Interactive download board (snapshot output when not a terminal)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(ctx, inline)
		},
	}
	cmd.Flags().BoolVar(&inline, "inline", false, "run the board inline instead of the alternate screen")

	return cmd
}

// runUI picks the board for interactive terminals with work to do, and the
// plain snapshot everywhere else (pipes, cron, empty queue) — the same
// degradation philosophy as the progress bar.
func runUI(ctx *Context, inline bool) error {
	snapshot, err := uiSnapshot(ctx)
	if err != nil {
		return err
	}

	urls, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		return err
	}

	if !ui.IsTerminal(os.Stdout) || len(urls) == 0 {
		if !ui.IsTerminal(os.Stdout) {
			fmt.Print(snapshot)
			return nil
		}
		// Interactive, but nothing queued: show the snapshot plus a hint
		// rather than an idle board.
		fmt.Print(snapshot)
		return nil
	}

	return runBoard(ctx, urls, snapshot, inline)
}

// runBoard starts the controllable queue run and hands the terminal to the
// bubbletea board. Quitting cancels the run context: active transfers stop
// at their next context check and partial .part files remain for resume.
func runBoard(ctx *Context, urls []string, settings string, inline bool) error {
	opts := []tea.ProgramOption{}
	if !inline {
		opts = append(opts, tea.WithAltScreen())
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	var (
		prog  *tea.Program
		sched *Scheduler
	)
	quit := make(chan struct{})
	send := func(msg any) {
		if prog == nil {
			return
		}
		select {
		case <-quit:
		default:
			prog.Send(msg)
		}
	}

	sched = NewScheduler(
		ctx,
		newQueueStore(ctx),
		urls,
		maxJobs(ctx.Config.QueueJobs),
		func(id int) downloader.Reporter { return tui.NewBoardReporter(id, send) },
		func(msg tui.JobStateMsg) { send(msg) },
	)

	model := tui.NewModel(tui.Options{
		QueueURLs: urls,
		Completed: completedHistoryLines(ctx.Config.CompletedFile),
		Settings:  settings,
		Jobs:      maxJobs(ctx.Config.QueueJobs),
		OnControl: func(jobID int, action tui.ControlAction) {
			sched.Control(jobID, action)
		},
	})
	prog = tea.NewProgram(model, opts...)

	finished := sched.Start(runCtx)
	go func() {
		<-finished
		send(tui.RunFinishedMsg{})
	}()

	_, err := prog.Run()
	close(quit)
	cancelRun()

	return err
}

// maxJobs mirrors the config validation floor: the board always runs at
// least one worker even if the config value is somehow zero.
func maxJobs(jobs int) int {
	if jobs < 1 {
		return 1
	}
	return jobs
}

// completedHistoryLines returns the completed history for the board tab.
func completedHistoryLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return nonEmptyLines(string(data))
}

// uiSnapshot renders the plain-text dashboard snapshot shown when the
// terminal cannot host the board (pipes, cron) or the queue is empty:
// pending queue, last completed entries, downloads directory state, and
// the running configuration.
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
			fmt.Fprintf(&b, "            %d. %s\n", i+1, ui.FileName(link))
		}
	}

	// Completed history: count and the most recent entries. Lines are
	// "timestamp | url" as written by markCompleted; only the file name is
	// worth showing.
	if data, err := os.ReadFile(ctx.Config.CompletedFile); err == nil && len(data) > 0 {
		lines := nonEmptyLines(string(data))
		fmt.Fprintf(&b, "completed:  %s\n", ui.Bold(fmt.Sprintf("%d downloads", len(lines))))
		for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
			fmt.Fprintf(&b, "            %s\n", completedLineForDisplay(lines[i]))
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

// completedLineForDisplay turns a "<timestamp> | <url>" history line into
// "<timestamp> | <file name>"; unrecognized lines pass through.
func completedLineForDisplay(line string) string {
	ts, url, ok := strings.Cut(line, "|")
	if !ok {
		return truncateLine(line, 64)
	}
	return strings.TrimSpace(ts) + " | " + ui.FileName(strings.TrimSpace(url))
}

// truncateLine shortens a line, keeping its tail (the URL part of
// completed entries matters more than the timestamp).
func truncateLine(line string, max int) string {
	if len(line) <= max {
		return line
	}
	return "…" + line[len(line)-max+1:]
}
