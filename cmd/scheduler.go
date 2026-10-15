package cmd

import (
	"context"
	"errors"
	"sync"

	"github.com/arash-jafarpour/dlm/downloader"
	"github.com/arash-jafarpour/dlm/internal/tui"
	"github.com/arash-jafarpour/dlm/ui"
)

// jobState is the scheduler's internal job lifecycle. It is a superset of
// the board's RowStatus.
type jobState int

const (
	// jobWaiting is a held job: visible on the board, but not dispatchable
	// until the user starts it (space on its row) or switches the run into
	// flowing mode (s). This is the board's default state for every file.
	jobWaiting jobState = iota
	jobQueued
	jobRunning
	jobPaused
	// jobStopping marks a running job that stop-all canceled; its result
	// lands as "held" (jobWaiting) rather than "cancelled", so the board
	// returns to its launch state.
	jobStopping
	jobFailed
	jobDone
	jobCancelled
)

func (s jobState) terminal() bool {
	return s == jobFailed || s == jobDone || s == jobCancelled
}

type schedJob struct {
	id     int
	url    string
	label  string
	state  jobState
	jctx   context.Context
	cancel context.CancelFunc
}

type jobResult struct {
	job       *schedJob
	completed bool
	err       error
}

type controlMsg struct {
	id     int
	action tui.ControlAction
}

// Scheduler runs a queue with per-job playback control — pause, resume,
// retry, cancel — powering the interactive board. Pause is implemented as
// cancel-in-flight: partial .part files remain on disk, so resume simply
// re-runs the download, which continues from the exact byte.
//
// All job state lives in one controller goroutine; Control is the only
// entry point from other goroutines (the board's key handler). The plain
// CLI keeps the simpler QueueRun, which has no playback semantics.
type Scheduler struct {
	ctx   *Context
	store *queueStore
	jobs  []*schedJob

	workers  int
	work     chan *schedJob
	results  chan jobResult
	controls chan controlMsg
	done     chan struct{}

	newReporter func(id int) downloader.Reporter
	notify      func(msg tui.JobStateMsg)

	finishOnce sync.Once
}

// NewScheduler prepares a run over urls. newReporter builds a per-job
// reporter (bound to the job id); notify receives engine-side state changes
// for the board.
func NewScheduler(
	ctx *Context,
	store *queueStore,
	urls []string,
	workers int,
	newReporter func(id int) downloader.Reporter,
	notify func(msg tui.JobStateMsg),
) *Scheduler {
	if workers < 1 {
		workers = 1
	}
	s := &Scheduler{
		ctx:         ctx,
		store:       store,
		workers:     workers,
		work:        make(chan *schedJob, len(urls)+1),
		results:     make(chan jobResult, len(urls)+1),
		controls:    make(chan controlMsg, 16),
		done:        make(chan struct{}),
		newReporter: newReporter,
		notify:      notify,
	}
	for i, u := range urls {
		s.jobs = append(s.jobs, &schedJob{
			id:    i,
			url:   u,
			label: tuiLabel(u),
			state: jobWaiting,
		})
	}
	return s
}

// Start launches workers and the controller; the returned channel closes
// once every job reached a terminal state. Canceling ctx stops the run.
func (s *Scheduler) Start(ctx context.Context) <-chan struct{} {
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}
	go func() {
		s.control(ctx)
		wg.Wait()
	}()
	go func() {
		<-ctx.Done()
		s.finishOnce.Do(func() { close(s.done) })
	}()
	return s.done
}

// Control posts a playback action for one job. Safe from any goroutine;
// never blocks indefinitely (the board has already quit then).
func (s *Scheduler) Control(id int, action tui.ControlAction) {
	select {
	case s.controls <- controlMsg{id: id, action: action}:
	default:
	}
}

func (s *Scheduler) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.work:
			rep := s.newReporter(j.id)
			runner := DownloadRunner(s.ctx.Downloader)
			if concrete, ok := runner.(*downloader.Downloader); ok {
				runner = concrete.WithReporter(rep)
			}

			completed, err := runner.Download(j.ctxOr(ctx), j.url)
			if err == nil && completed {
				if storeErr := s.store.completeAndRemove(j.url); storeErr != nil {
					err = storeErr
				}
			}
			select {
			case s.results <- jobResult{job: j, completed: completed, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ctxOr returns the job's own cancelable context, created at dispatch.
func (j *schedJob) ctxOr(fallback context.Context) context.Context {
	if j.jctx != nil {
		return j.jctx
	}
	return fallback
}

// control is the single owner of job state and scheduling.
func (s *Scheduler) control(ctx context.Context) {
	inflight := 0
	queued := []*schedJob{} // nothing is dispatchable until started
	autoStart := false      // flowing mode: s key / ControlStartAll

	report := func(j *schedJob, state tui.RowStatus, detail string) {
		if s.notify != nil {
			s.notify(tui.JobStateMsg{ID: j.id, Label: j.label, State: state, Detail: detail})
		}
	}

	// startEnqueue moves one held/stopped job into the dispatch queue. The
	// board shows it as "starting": the app is on it from this moment.
	startEnqueue := func(j *schedJob) {
		if j.state != jobWaiting && j.state != jobPaused &&
			j.state != jobFailed && j.state != jobCancelled {
			return
		}
		j.state = jobQueued
		queued = append(queued, j)
		report(j, tui.RowStarting, "")
	}

	dispatch := func() {
		if autoStart {
			// Flowing mode: every held job becomes dispatchable, in queue
			// order, so finishing jobs pull the next waiting file.
			queued = nil
			for _, j := range s.jobs {
				if j.state == jobWaiting {
					j.state = jobQueued
					report(j, tui.RowStarting, "")
				}
				if j.state == jobQueued {
					queued = append(queued, j)
				}
			}
		}
		for inflight < s.workers && len(queued) > 0 {
			j := queued[0]
			queued = queued[1:]
			if j.state != jobQueued {
				continue // cancelled while it sat in the queue
			}
			jctx, cancel := context.WithCancel(ctx)
			j.jctx = jctx
			j.cancel = cancel
			j.state = jobRunning
			inflight++
			s.work <- j
		}
		if inflight == 0 && len(queued) == 0 {
			allTerminal := true
			for _, j := range s.jobs {
				if !j.state.terminal() {
					allTerminal = false
					break
				}
			}
			if allTerminal {
				s.finishOnce.Do(func() { close(s.done) })
			}
		}
	}

	dispatch()
	for {
		select {
		case <-ctx.Done():
			for _, j := range s.jobs {
				if j.cancel != nil {
					j.cancel()
				}
			}
			return

		case res := <-s.results:
			inflight--
			j := res.job
			j.cancel = nil
			switch {
			case j.state == jobPaused, j.state == jobCancelled:
				// The stop was user-requested and already reported.
			case j.state == jobStopping:
				// stop-all wound this transfer down: back to held.
				j.state = jobWaiting
				report(j, tui.RowQueued, "")
			case res.err == nil:
				j.state = jobDone
				if !res.completed {
					report(j, tui.RowSaved, "")
				}
			case errors.Is(res.err, context.Canceled):
				j.state = jobCancelled
				report(j, tui.RowCancelled, "")
			default:
				j.state = jobFailed
				report(j, tui.RowFailed, res.err.Error())
			}
			dispatch()

		case c := <-s.controls:
			if c.action == tui.ControlStartAll {
				autoStart = true
				dispatch()
				continue
			}
			if c.action == tui.ControlStopAll {
				// Back to the launch state: flowing mode off, active
				// transfers wound down, unfinished jobs held again.
				autoStart = false
				for _, job := range s.jobs {
					switch job.state {
					case jobRunning:
						job.state = jobStopping
						if job.cancel != nil {
							job.cancel()
						}
						report(job, tui.RowQueued, "")
					case jobQueued, jobPaused:
						job.state = jobWaiting
						report(job, tui.RowQueued, "")
					}
				}
				dispatch()
				continue
			}
			if c.id < 0 || c.id >= len(s.jobs) {
				continue
			}
			j := s.jobs[c.id]
			switch c.action {
			case tui.ControlPause:
				switch j.state {
				case jobRunning:
					j.state = jobPaused
					j.cancel()
					report(j, tui.RowPaused, "")
				case jobQueued:
					// Still waiting for a slot: put it back to held.
					j.state = jobWaiting
					report(j, tui.RowQueued, "")
				}
			case tui.ControlStart, tui.ControlResume, tui.ControlRetry:
				startEnqueue(j)
				dispatch()
			case tui.ControlCancel:
				switch j.state {
				case jobRunning, jobPaused:
					j.state = jobCancelled
					if j.cancel != nil {
						j.cancel()
					}
					report(j, tui.RowCancelled, "")
				case jobWaiting, jobQueued:
					j.state = jobCancelled
					report(j, tui.RowCancelled, "")
				}
			}
			// Any state change can complete the run (e.g. cancelling the
			// last held job), so re-evaluate scheduling and the finish
			// condition after every control.
			dispatch()
		}
	}
}

// tuiLabel is the file name shown for a job before any transfer exists.
func tuiLabel(rawURL string) string {
	return ui.FileName(rawURL)
}
