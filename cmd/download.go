package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/ui"

	"github.com/spf13/cobra"
)

// DownloadRunner is the downloader behavior the download commands depend
// on. *downloader.Downloader implements it; test doubles can too.
type DownloadRunner interface {
	Download(ctx context.Context, url string) (bool, error)
}

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

	if !ctx.Quiet {
		fmt.Printf("%s", ui.Cyan("→ downloading\n"))
	}
	completed, err := ctx.Downloader.Download(context.Background(), url)
	if err != nil {
		return err
	}
	if completed && !ctx.Quiet {
		fmt.Printf("%s\n", ui.Green("✓ done"))
	}
	return nil
}

func downloadQueue(ctx *Context) error {
	store := newQueueStore(ctx)
	urls, err := store.load()
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		fmt.Println("no links found in queue")
		return nil
	}

	jobs := ctx.Config.QueueJobs
	if jobs > len(urls) {
		jobs = len(urls)
	}
	if jobs > 1 {
		return downloadQueueParallel(ctx, store, urls, jobs)
	}
	return downloadQueueSequential(ctx, store, urls)
}

// downloadQueueSequential keeps the classic one-file-at-a-time flow, with
// the per-file formatting queue users know (blank line between blocks).
func downloadQueueSequential(
	ctx *Context,
	store *queueStore,
	urls []string,
) error {
	for i, urlStr := range urls {
		if !ctx.Quiet {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s", ui.Cyan("→ downloading\n"))
		}

		// A per-file reporter carries this transfer's size and duration
		// into the completed history.
		rep := &cliReporter{quiet: ctx.Quiet}
		runner := DownloadRunner(ctx.Downloader)
		if concrete, ok := runner.(*downloader.Downloader); ok {
			runner = concrete.WithReporter(rep)
		}

		completed, err := runner.Download(context.Background(), urlStr)
		if err != nil {
			logError(err)
			continue
		}

		if completed {
			size, elapsed := transferStats(rep)
			if err := store.completeAndRemove(urlStr, size, elapsed); err != nil {
				fmt.Printf("%s %v\n", ui.Yellow("⚠ couldn't record completed download:"), err)
			}
		}
	}
	return nil
}

// downloadQueueParallel runs up to jobs files concurrently. All output goes
// through one ui.MultiBar block: a live bar row per active file, `✓ saved →`
// and failures as permanent lines, everything keyed by file name (never the
// raw URLs).
func downloadQueueParallel(
	ctx *Context,
	store *queueStore,
	urls []string,
	jobs int,
) error {
	mgr := ui.NewMultiBar()
	defer mgr.Finish()
	if !ctx.Quiet {
		mgr.Line(ui.Cyan(fmt.Sprintf("→ downloading %d files (%d workers)", len(urls), jobs)))
	}

	run := &QueueRun{
		Ctx:   ctx,
		Store: store,
		URLs:  urls,
		Jobs:  jobs,
		NewReporter: func() downloader.Reporter {
			return &parallelReporter{mgr: mgr, quiet: ctx.Quiet}
		},
		OnFileDone: func(_ string, err error, rep downloader.Reporter) {
			if err == nil {
				return
			}
			if pr, ok := rep.(*parallelReporter); ok {
				pr.finishError(err)
			}
		},
	}

	<-run.Start(context.Background())
	return nil
}
