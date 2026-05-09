package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CronJob represents a scheduled agent task.
type CronJob struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Enabled     bool         `json:"enabled"`
	Schedule    CronSchedule `json:"schedule"`
	Prompt      string       `json:"prompt"`
	SessionID   string       `json:"session_id"`
	Timeout     int          `json:"timeout,omitempty"` // seconds, 0 = default
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	State       CronJobState `json:"state"`
}

// CronSchedule defines when a job should run.
type CronSchedule struct {
	Kind     string `json:"kind"`               // "every" | "cron" | "once"
	Expr     string `json:"expr,omitempty"`     // cron expression (kind=cron)
	EveryMs  int64  `json:"every_ms,omitempty"` // interval in ms (kind=every)
	Timezone string `json:"timezone,omitempty"` // IANA timezone (kind=cron)
	RunAt    string `json:"run_at,omitempty"`   // ISO8601 datetime (kind=once)
}

// CronJobState tracks the runtime state of a job.
type CronJobState struct {
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"` // "ok" | "error" | "running"
	LastError  string     `json:"last_error,omitempty"`
	RunCount   int        `json:"run_count"`
}

// CronRun records one execution of a cron job.
type CronRun struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	JobName    string     `json:"job_name"`
	Status     string     `json:"status"` // "running" | "ok" | "error"
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	Error      string     `json:"error,omitempty"`
	SessionID  string     `json:"session_id"`
}

// CronStatus is the global cron system status summary.
type CronStatus struct {
	Enabled    bool       `json:"enabled"`
	TotalJobs  int        `json:"total_jobs"`
	ActiveJobs int        `json:"active_jobs"`
	NextWakeAt *time.Time `json:"next_wake_at,omitempty"`
}

var (
	ErrCronJobNotFound = errors.New("cron: job not found")
	ErrCronNameEmpty   = errors.New("cron: name is required")
	ErrCronPromptEmpty = errors.New("cron: prompt is required")
)

// CronStore manages cron job persistence using JSON files.
type CronStore struct {
	mu      sync.RWMutex
	jobs    []*CronJob
	dataDir string // {serverDataDir}/cron
	runsDir string // {serverDataDir}/cron/runs
}

// NewCronStore creates a store, loading persisted jobs from dataDir.
func NewCronStore(serverDataDir string) (*CronStore, error) {
	dataDir := filepath.Join(serverDataDir, "cron")
	runsDir := filepath.Join(dataDir, "runs")

	for _, d := range []string{dataDir, runsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("cron store: create dir %s: %w", d, err)
		}
	}

	s := &CronStore{
		jobs:    make([]*CronJob, 0),
		dataDir: dataDir,
		runsDir: runsDir,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// List returns all jobs.
func (s *CronStore) List() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*CronJob, len(s.jobs))
	for i, j := range s.jobs {
		cp := *j
		out[i] = &cp
	}
	return out
}

// Get returns a job by ID.
func (s *CronStore) Get(id string) (*CronJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.jobs {
		if j.ID == id {
			cp := *j
			return &cp, nil
		}
	}
	return nil, ErrCronJobNotFound
}

// Add creates a new cron job.
func (s *CronStore) Add(job *CronJob) (*CronJob, error) {
	if job.Name == "" {
		return nil, ErrCronNameEmpty
	}
	if job.Prompt == "" {
		return nil, ErrCronPromptEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	job.ID = uuid.New().String()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.SessionID == "" {
		job.SessionID = "cron:" + job.ID
	}

	s.jobs = append(s.jobs, job)
	s.persist()
	cp := *job
	return &cp, nil
}

// Update modifies an existing job. Only non-zero fields in the patch are applied.
func (s *CronStore) Update(id string, patch CronJobPatch) (*CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var job *CronJob
	for _, j := range s.jobs {
		if j.ID == id {
			job = j
			break
		}
	}
	if job == nil {
		return nil, ErrCronJobNotFound
	}

	if patch.Name != nil {
		job.Name = *patch.Name
	}
	if patch.Description != nil {
		job.Description = *patch.Description
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if patch.Schedule != nil {
		job.Schedule = *patch.Schedule
	}
	if patch.Prompt != nil {
		job.Prompt = *patch.Prompt
	}
	if patch.Timeout != nil {
		job.Timeout = *patch.Timeout
	}
	job.UpdatedAt = time.Now()

	s.persist()
	cp := *job
	return &cp, nil
}

// CronJobPatch contains optional fields for updating a job.
type CronJobPatch struct {
	Name        *string       `json:"name,omitempty"`
	Description *string       `json:"description,omitempty"`
	Enabled     *bool         `json:"enabled,omitempty"`
	Schedule    *CronSchedule `json:"schedule,omitempty"`
	Prompt      *string       `json:"prompt,omitempty"`
	Timeout     *int          `json:"timeout,omitempty"`
}

// Remove deletes a job by ID.
func (s *CronStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			s.persist()
			return true
		}
	}
	return false
}

// UpdateState updates the runtime state of a job.
func (s *CronStore) UpdateState(id string, state CronJobState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, j := range s.jobs {
		if j.ID == id {
			j.State = state
			s.persist()
			return
		}
	}
}

// AppendRun records a run result for a job (JSONL file per job).
func (s *CronStore) AppendRun(run *CronRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	path := filepath.Join(s.runsDir, run.JobID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// ListRuns returns recent runs for a job (newest first), up to limit.
func (s *CronStore) ListRuns(jobID string, limit int) ([]*CronRun, error) {
	path := filepath.Join(s.runsDir, jobID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var runs []*CronRun
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var r CronRun
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		runs = append(runs, &r)
	}

	// Reverse for newest-first.
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}

	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

// ListAllRuns returns recent runs across all jobs (newest first), up to limit.
func (s *CronStore) ListAllRuns(limit int) ([]*CronRun, error) {
	entries, err := os.ReadDir(s.runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var all []*CronRun
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		jobID := e.Name()[:len(e.Name())-len(".jsonl")]
		runs, err := s.ListRuns(jobID, 0)
		if err != nil {
			continue
		}
		all = append(all, runs...)
	}

	// Sort by started_at descending.
	sortRunsByTime(all)

	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// persistence

func (s *CronStore) persist() {
	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dataDir, "jobs.json"), data, 0o644)
}

func (s *CronStore) load() error {
	path := filepath.Join(s.dataDir, "jobs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(raw, &s.jobs)
}

// helpers

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func sortRunsByTime(runs []*CronRun) {
	// Simple insertion sort (small lists).
	for i := 1; i < len(runs); i++ {
		for j := i; j > 0 && runs[j].StartedAt.After(runs[j-1].StartedAt); j-- {
			runs[j], runs[j-1] = runs[j-1], runs[j]
		}
	}
}
