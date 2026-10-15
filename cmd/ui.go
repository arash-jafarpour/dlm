package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arash-jafarpour/dlm/config"
	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/internal/tui"
	"github.com/arash-jafarpour/dlm/reader"
	"github.com/arash-jafarpour/dlm/ui"
)

// runUI opens the board on any interactive terminal — empty queue included,
// where the board shows a placeholder and URLs can be added with 'a'. Only
// pipelines and cron (no terminal) get the plain snapshot, the same
// degradation philosophy as the progress bar.
func runUI(ctx *Context, inline bool) error {
	urls, err := readLinksFile(ctx.Config.QueueFile)
	if err != nil {
		return err
	}

	// Not a terminal: print the plain snapshot instead of a board nobody
	// could drive.
	if !ui.IsTerminal(os.Stdout) {
		snapshot, err := uiSnapshot(ctx)
		if err != nil {
			return err
		}
		fmt.Print(snapshot)
		return nil
	}

	return runBoard(ctx, urls, inline)
}

// boardQueue binds the board's queue-editing hooks to the queue file and
// the scheduler, keeping a URL↔job-id map so both stay in sync. All methods
// are called from the board's single Update goroutine.
type boardQueue struct {
	store *queueStore
	sched *Scheduler
	warn  func(string)

	idsByURL map[string]int
	urlByID  map[int]string
}

func newBoardQueue(store *queueStore, sched *Scheduler, urls []string, warn func(string)) *boardQueue {
	b := &boardQueue{
		store:    store,
		sched:    sched,
		warn:     warn,
		idsByURL: map[string]int{},
		urlByID:  map[int]string{},
	}
	for i, u := range urls {
		b.idsByURL[u] = i
		b.urlByID[i] = u
	}
	return b
}

// add stores URLs and registers them with the engine, returning their ids.
func (b *boardQueue) add(urls []string) []int {
	ids := make([]int, 0, len(urls))
	for _, u := range urls {
		id, ok := b.sched.Add(u)
		if !ok {
			continue
		}
		if err := b.store.append(u); err != nil {
			b.warn(err.Error())
		}
		b.idsByURL[u] = id
		b.urlByID[id] = u
		ids = append(ids, id)
	}
	return ids
}

// delete removes a job and its queue-file line.
func (b *boardQueue) delete(id int) {
	url, ok := b.urlByID[id]
	if !ok {
		return
	}
	if err := b.store.remove(url); err != nil {
		b.warn(err.Error())
	}
	b.sched.Remove(id)
	delete(b.idsByURL, url)
	delete(b.urlByID, id)
}

// reorder persists the queue file in display order and updates the engine's
// flow priority.
func (b *boardQueue) reorder(ids []int) {
	urls := make([]string, 0, len(ids))
	for _, id := range ids {
		if u, ok := b.urlByID[id]; ok {
			urls = append(urls, u)
		}
	}
	if err := b.store.rewrite(urls); err != nil {
		b.warn(err.Error())
	}
	b.sched.Reorder(ids)
}

// reload reconciles board jobs with the queue file after an external edit:
// surviving URLs keep their ids, new ones are added, vanished ones removed.
func (b *boardQueue) reload() []tui.JobRef {
	urls, err := readLinksFile(b.store.queueFile)
	if err != nil {
		b.warn(err.Error())
		return nil
	}

	seen := map[string]bool{}
	jobs := make([]tui.JobRef, 0, len(urls))
	for _, u := range urls {
		seen[u] = true
		id, ok := b.idsByURL[u]
		if !ok {
			newID, added := b.sched.Add(u)
			if !added {
				continue
			}
			b.idsByURL[u] = newID
			b.urlByID[newID] = u
			id = newID
		}
		jobs = append(jobs, tui.JobRef{ID: id, URL: u})
	}

	for u, id := range b.idsByURL {
		if !seen[u] {
			b.sched.Remove(id)
			delete(b.urlByID, id)
			delete(b.idsByURL, u)
		}
	}
	return jobs
}

// runBoard starts the controllable queue run and hands the terminal to the
// bubbletea board. Quitting cancels the run context: active transfers stop
// at their next context check and partial .part files remain for resume.
func runBoard(ctx *Context, urls []string, inline bool) error {
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

	store := newQueueStore(ctx)
	provider := newRunnerProvider(ctx.Config)
	sched = NewScheduler(
		store,
		urls,
		maxJobs(ctx.Config.QueueJobs),
		provider.New,
		func(id int) downloader.Reporter { return tui.NewBoardReporter(id, send) },
		func(msg tui.JobStateMsg) { send(msg) },
	)

	bq := newBoardQueue(store, sched, urls, func(text string) {
		send(tui.WarnMsg{Text: text})
	})

	cfgPath, cfgPathErr := configPath()
	saveCfg := func() error {
		if cfgPathErr != nil {
			return cfgPathErr
		}
		return ctx.Config.Save(cfgPath)
	}

	model := tui.NewModel(tui.Options{
		QueueURLs: urls,
		Completed: completedDisplayEntries(ctx.Config.CompletedFile),
		Settings:  settingsRows(ctx.Config),
		Jobs:      maxJobs(ctx.Config.QueueJobs),
		Summary:   boardSummary(ctx),
		OnSummary: func() tea.Cmd {
			// Recomputed on the cmd side: the roll-up reads the history
			// file, the queue file, and stats the output directory.
			return func() tea.Msg { return tui.SummaryMsg{Summary: boardSummary(ctx)} }
		},
		OnControl: func(jobID int, action tui.ControlAction) {
			sched.Control(jobID, action)
		},
		OnSettingSet: func(key, value string) error {
			if err := applySetting(ctx.Config, key, value); err != nil {
				return err
			}
			return saveCfg()
		},
		OnSettingReset: func(key string) (string, error) {
			d := settingByKey(key)
			if d == nil {
				return "", fmt.Errorf("unknown config key %q", key)
			}
			if err := d.Set(ctx.Config, d.Default(ctx.Config)); err != nil {
				return "", err
			}
			if err := saveCfg(); err != nil {
				return "", err
			}
			return d.Get(ctx.Config), nil
		},
		OnSettingsResetAll: func() ([]tui.Setting, error) {
			*ctx.Config = *config.Default()
			if err := saveCfg(); err != nil {
				return nil, err
			}
			return settingsRows(ctx.Config), nil
		},
		OnDeleteCompleted: func(ref string) error {
			return storeForCompleted(ctx).removeCompletedLine(ref)
		},
		OnClearCompleted: func() error {
			return storeForCompleted(ctx).clearCompleted()
		},
		OnAdd:     bq.add,
		OnDelete:  bq.delete,
		OnReorder: bq.reorder,
		OnOpenEditor: func() tea.Cmd {
			cmd := editorCommand(ctx.Config.QueueFile)
			return tea.ExecProcess(cmd, func(err error) tea.Msg {
				if err != nil {
					return tui.WarnMsg{Text: fmt.Sprintf("editor: %v", err)}
				}
				return tui.QueueReloadMsg{Jobs: bq.reload()}
			})
		},
		OnOpenConfig: func() tea.Cmd {
			cfgPath, err := configPath()
			if err != nil {
				return func() tea.Msg { return tui.WarnMsg{Text: err.Error()} }
			}
			cmd := editorCommand(cfgPath)
			return tea.ExecProcess(cmd, func(err error) tea.Msg {
				if err != nil {
					return tui.WarnMsg{Text: fmt.Sprintf("editor: %v", err)}
				}
				// Re-read the file so external edits land in the board and
				// in-memory config.
				if reloaded, err := config.Load(cfgPath); err == nil {
					*ctx.Config = *reloaded
				} else {
					return tui.WarnMsg{Text: fmt.Sprintf("config: %v", err)}
				}
				return tui.SettingsMsg{Settings: settingsRows(ctx.Config)}
			})
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

// completedDisplayEntries formats the completed history for the board's
// completed tab: newest first, date+time, file name, size and duration
// (never the raw URL). Ref carries the raw line so the board can delete
// exactly that entry.
func completedDisplayEntries(path string) []tui.HistoryEntry {
	raw := completedHistoryLines(path)
	entries := make([]tui.HistoryEntry, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		entries = append(entries, tui.HistoryEntry{
			Text: completedLineForDisplay(raw[i]),
			Ref:  raw[i],
		})
	}
	return entries
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
		"the interactive board opens when this runs on a terminal",
	))
	return b.String(), nil
}

// downloadsDirSummary describes the downloads directory: file count and
// total size, "empty"/"missing" instead of errors when it does not matter.
func downloadsDirSummary(dir string) string {
	count, total, err := dirStats(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("%s (missing)", dir)
		}
		return dir // unreadable: show the plain path
	}
	return fmt.Sprintf("%d files, %s in %s", count, humanBytes(total), dir)
}

// dirStats counts the files in dir and sums their sizes. Directories are
// skipped; a missing or unreadable dir returns the error.
func dirStats(dir string) (count int, total int64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		count++
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return count, total, nil
}

// boardSummary computes the board's dashboard numbers: totals across the
// whole history, the output directory's state, the queue depth, and the
// environment. It is the board-side counterpart of the snapshot's recap and
// is re-run whenever the completed/summary tab opens, a run finishes, or the
// user refreshes.
func boardSummary(ctx *Context) tui.Summary {
	lines := completedHistoryLines(ctx.Config.CompletedFile)
	s := tui.Summary{Count: len(lines)}
	for _, l := range lines {
		s.Bytes += completedLineSize(l)
	}
	s.Dir = ctx.Config.OutputDir
	if count, total, err := dirStats(s.Dir); err == nil {
		s.Files, s.DirBytes = count, total
	}
	// Queue depth: a missing file reads as empty, matching the snapshot.
	if urls, err := readLinksFile(ctx.Config.QueueFile); err == nil {
		s.Pending = len(urls)
	}
	s.Settings = fmt.Sprintf("chunks=%d retries=%d queue_jobs=%d",
		ctx.Config.NumChunks, ctx.Config.MaxRetries, ctx.Config.QueueJobs)
	s.Platform = runtime.GOOS + "/" + runtime.GOARCH
	return s
}

// completedLineSize extracts the byte-size field from a history line, 0
// when absent or malformed (legacy two-field lines).
func completedLineSize(line string) int64 {
	parts := strings.Split(line, "|")
	if len(parts) < 4 {
		return 0
	}
	size, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	if err != nil || size < 0 {
		return 0
	}
	return size
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

// completedLineForDisplay turns a history line into
// "<timestamp>  <file name>  <size>  <duration>". The size and duration
// columns are shown when present; older two-field lines stay readable.
func completedLineForDisplay(line string) string {
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return truncateLine(line, 64)
	}
	out := strings.TrimSpace(parts[0]) + "  " + ui.FileName(strings.TrimSpace(parts[1]))
	if len(parts) >= 4 {
		if size, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil && size > 0 {
			out += "  " + humanBytes(size)
		}
		if d, err := time.ParseDuration(strings.TrimSpace(parts[3])); err == nil && d > 0 {
			out += "  " + humanDuration(d)
		}
	}
	return out
}

// humanDuration renders a transfer duration compactly: "12.3s", "3m07s",
// "1h04m".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// truncateLine shortens a line, keeping its tail (the URL part of
// completed entries matters more than the timestamp).
func truncateLine(line string, max int) string {
	if len(line) <= max {
		return line
	}
	return "…" + line[len(line)-max+1:]
}

// editorCommand builds the editor invocation for file: $EDITOR, then
// $VISUAL, then a platform default (notepad on Windows, vi elsewhere).
func editorCommand(file string) *exec.Cmd {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		if editor := os.Getenv(env); editor != "" {
			return exec.Command(editor, file)
		}
	}
	if runtime.GOOS == "windows" {
		return exec.Command("notepad", file)
	}
	return exec.Command("vi", file)
}

// storeForCompleted returns a store for completed-file operations; the
// store itself is cheap and mutex-guarded, and the board only calls one of
// these per keypress.
func storeForCompleted(ctx *Context) *queueStore {
	return newQueueStore(ctx)
}
