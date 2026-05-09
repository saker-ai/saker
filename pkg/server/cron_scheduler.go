package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// silentMarker is the exact response an agent can return to suppress delivery
// of a cron job result when there is nothing new to report.
const silentMarker = "[SILENT]"

// silentPromptSuffix is appended to every cron job prompt so the agent knows
// it may use the [SILENT] marker.
const silentPromptSuffix = "\n\nSILENT: If there is genuinely nothing new to report, respond with exactly \"[SILENT]\" (nothing else) to suppress delivery. Never combine [SILENT] with content — either report your findings normally, or say [SILENT] and nothing more."

// maxConcurrentCronJobs bounds how many cron jobs may execute simultaneously.
const maxConcurrentCronJobs = 5

// Scheduler manages cron job scheduling and execution.
type Scheduler struct {
	store   *CronStore
	handler *Handler
	tracker *ActiveTurnTracker
	logger  *slog.Logger

	mu      sync.Mutex
	running map[string]bool // jobID → currently executing
	sem     chan struct{}    // semaphore bounding concurrent executions
	stopCh  chan struct{}
	stopped chan struct{}
}

// NewScheduler creates a scheduler that checks for due jobs periodically.
func NewScheduler(store *CronStore, handler *Handler, tracker *ActiveTurnTracker, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:   store,
		handler: handler,
		tracker: tracker,
		logger:  logger,
		running: make(map[string]bool),
		sem:     make(chan struct{}, maxConcurrentCronJobs),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Start begins the scheduler tick loop.
func (s *Scheduler) Start() {
	go s.loop()
	s.logger.Info("cron scheduler started")
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	close(s.stopCh)
	<-s.stopped
	s.logger.Info("cron scheduler stopped")
}

func (s *Scheduler) loop() {
	defer close(s.stopped)

	// Compute initial next-run times for all jobs.
	s.refreshNextRuns()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	jobs := s.store.List()
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		if job.State.NextRunAt == nil || now.Before(*job.State.NextRunAt) {
			continue
		}

		s.mu.Lock()
		alreadyRunning := s.running[job.ID]
		if !alreadyRunning {
			s.running[job.ID] = true
		}
		s.mu.Unlock()

		if alreadyRunning {
			s.logger.Debug("cron job still running, skipping", "job_id", job.ID, "name", job.Name)
			continue
		}

		// Acquire semaphore slot; skip if at capacity.
		select {
		case s.sem <- struct{}{}:
		default:
			s.mu.Lock()
			delete(s.running, job.ID)
			s.mu.Unlock()
			s.logger.Warn("cron concurrency limit reached, skipping", "job_id", job.ID, "limit", maxConcurrentCronJobs)
			continue
		}

		go s.executeJob(job)
	}
}

func (s *Scheduler) executeJob(job *CronJob) {
	defer func() {
		s.mu.Lock()
		delete(s.running, job.ID)
		s.mu.Unlock()
		<-s.sem // release semaphore slot
	}()

	runID := uuid.New().String()
	turnID := uuid.New().String()
	startTime := time.Now()

	s.logger.Info("cron job executing", "job_id", job.ID, "name", job.Name, "run_id", runID)

	// Record run start.
	run := &CronRun{
		ID:        runID,
		JobID:     job.ID,
		JobName:   job.Name,
		Status:    "running",
		StartedAt: startTime,
		SessionID: job.SessionID,
	}
	_ = s.store.AppendRun(run)

	// Update job state to running.
	state := job.State
	state.LastRunAt = &startTime
	state.LastStatus = "running"
	state.RunCount++
	s.store.UpdateState(job.ID, state)

	// Notify subscribers about run start.
	s.handler.notifyAllClients("cron/run_started", run)

	// Register active turn.
	threadTitle := fmt.Sprintf("Cron: %s", job.Name)
	s.tracker.RegisterCron(turnID, job.SessionID, threadTitle, job.Prompt, job.ID)
	defer s.tracker.Unregister(turnID)

	// Execute the agent turn with a bounded context.
	// When job.Timeout is 0 (unset), apply a 10-minute default to
	// prevent runaway cron jobs from blocking the scheduler.
	ctx := context.Background()
	timeout := time.Duration(job.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inject [SILENT] instruction so the agent can suppress empty reports.
	prompt := job.Prompt + silentPromptSuffix

	ch, err := s.handler.runtime.RunStream(ctx, api.Request{Prompt: prompt, SessionID: job.SessionID})
	finishTime := time.Now()

	if err != nil {
		s.finishRun(job, run, &finishTime, "error", "", err.Error())
		return
	}

	// Consume stream events.
	var summary string
	for evt := range ch {
		s.tracker.UpdateFromEvent(turnID, evt)
		if evt.Delta != nil && evt.Delta.Text != "" {
			summary += evt.Delta.Text
		}
	}

	finishTime = time.Now()

	// Check for [SILENT] marker — agent says nothing new to report.
	if isSilentResponse(summary) {
		s.logger.Info("cron job silent (nothing to report)", "job_id", job.ID, "name", job.Name)
		s.finishRun(job, run, &finishTime, "silent", "", "")
		return
	}

	// Truncate summary.
	if len(summary) > 500 {
		summary = summary[:500] + "..."
	}
	s.finishRun(job, run, &finishTime, "ok", summary, "")
}

func (s *Scheduler) finishRun(job *CronJob, run *CronRun, finishTime *time.Time, status, summary, errMsg string) {
	run.Status = status
	run.FinishedAt = finishTime
	run.DurationMs = finishTime.Sub(run.StartedAt).Milliseconds()
	run.Summary = summary
	run.Error = errMsg
	_ = s.store.AppendRun(run)

	// Update job state.
	state := job.State
	state.LastStatus = status
	state.LastError = errMsg
	// Compute next run.
	next := computeNextRun(job.Schedule, *finishTime)
	state.NextRunAt = next
	s.store.UpdateState(job.ID, state)

	s.logger.Info("cron job finished", "job_id", job.ID, "name", job.Name, "status", status,
		"duration_ms", run.DurationMs)

	// Suppress client notification for silent runs — the whole point is
	// to avoid noise when nothing changed.
	if status == "silent" {
		return
	}

	// Notify subscribers about run completion.
	s.handler.notifyAllClients("cron/run_finished", run)
}

// isSilentResponse checks whether the agent response consists solely of the
// [SILENT] marker, ignoring surrounding whitespace.
func isSilentResponse(response string) bool {
	return strings.TrimSpace(response) == silentMarker
}

// refreshNextRuns computes NextRunAt for all enabled jobs that don't have one set.
func (s *Scheduler) refreshNextRuns() {
	now := time.Now()
	for _, job := range s.store.List() {
		if !job.Enabled {
			continue
		}
		if job.State.NextRunAt != nil {
			continue
		}
		next := computeNextRun(job.Schedule, now)
		if next != nil {
			state := job.State
			state.NextRunAt = next
			s.store.UpdateState(job.ID, state)
		}
	}
}

// RunJobNow triggers an immediate execution of a job (manual run).
func (s *Scheduler) RunJobNow(jobID string) error {
	job, err := s.store.Get(jobID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.running[jobID] {
		s.mu.Unlock()
		return fmt.Errorf("cron: job %s is already running", jobID)
	}
	s.running[jobID] = true
	s.mu.Unlock()

	// Acquire semaphore slot (blocking for manual runs so the request doesn't silently drop).
	s.sem <- struct{}{}

	go s.executeJob(job)
	return nil
}

// Status returns the global cron system status.
func (s *Scheduler) Status() CronStatus {
	jobs := s.store.List()
	active := 0
	var nextWake *time.Time
	for _, j := range jobs {
		if j.Enabled {
			active++
			if j.State.NextRunAt != nil {
				if nextWake == nil || j.State.NextRunAt.Before(*nextWake) {
					t := *j.State.NextRunAt
					nextWake = &t
				}
			}
		}
	}
	return CronStatus{
		Enabled:    true,
		TotalJobs:  len(jobs),
		ActiveJobs: active,
		NextWakeAt: nextWake,
	}
}

// computeNextRun calculates the next execution time for a schedule.
func computeNextRun(sched CronSchedule, from time.Time) *time.Time {
	switch sched.Kind {
	case "every":
		if sched.EveryMs <= 0 {
			return nil
		}
		next := from.Add(time.Duration(sched.EveryMs) * time.Millisecond)
		return &next

	case "cron":
		if sched.Expr == "" {
			return nil
		}
		loc := time.Local
		if sched.Timezone != "" {
			if l, err := time.LoadLocation(sched.Timezone); err == nil {
				loc = l
			}
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(sched.Expr)
		if err != nil {
			return nil
		}
		next := schedule.Next(from.In(loc))
		next = next.In(time.Local)
		return &next

	case "once":
		if sched.RunAt == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, sched.RunAt)
		if err != nil {
			return nil
		}
		if t.Before(from) {
			return nil // already past
		}
		return &t

	default:
		return nil
	}
}
