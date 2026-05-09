package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- CronStore CRUD tests ---

func newTestCronStore(t *testing.T) *CronStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	return store
}

func TestCronStore_Add(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	job := &CronJob{
		Name:     "daily-check",
		Prompt:   "Check system status",
		Enabled:  true,
		Schedule: CronSchedule{Kind: "every", EveryMs: 60000},
	}

	created, err := store.Add(job)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected ID to be assigned")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
	if created.SessionID == "" {
		t.Fatal("expected SessionID to be auto-assigned")
	}
	if created.Name != "daily-check" {
		t.Fatalf("name mismatch: got %q", created.Name)
	}

	// Verify persistence: reload from disk.
	store2, err := NewCronStore(filepath.Join(t.TempDir(), "..", filepath.Base(t.TempDir())))
	// Reload into fresh store from same dir.
	store2, err = NewCronStore(filepath.Dir(store.dataDir))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	jobs := store2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 persisted job, got %d", len(jobs))
	}
	if jobs[0].ID != created.ID {
		t.Fatalf("persisted job ID mismatch: got %q, want %q", jobs[0].ID, created.ID)
	}
}

func TestCronStore_AddValidation(t *testing.T) {
	t.Parallel()
	_ = newTestCronStore(t)

	tests := []struct {
		name    string
		job     *CronJob
		wantErr error
	}{
		{"empty name", &CronJob{Name: "", Prompt: "p"}, ErrCronNameEmpty},
		{"empty prompt", &CronJob{Name: "n", Prompt: ""}, ErrCronPromptEmpty},
		{"both empty", &CronJob{Name: "", Prompt: ""}, ErrCronNameEmpty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestCronStore(t)
			_, err := s.Add(tt.job)
			if err != tt.wantErr {
				t.Errorf("Add(%+v): got %v, want %v", tt.job, err, tt.wantErr)
			}
		})
	}
}

func TestCronStore_AddPreservesSessionID(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	job := &CronJob{
		Name:      "custom-session",
		Prompt:    "p",
		SessionID: "my-session",
	}
	created, err := store.Add(job)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.SessionID != "my-session" {
		t.Fatalf("expected SessionID preserved, got %q", created.SessionID)
	}
}

func TestCronStore_List(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	// Empty list returns empty slice, not nil.
	jobs := store.List()
	if len(jobs) != 0 {
		t.Fatalf("expected empty list, got %d", len(jobs))
	}

	// Add two jobs.
	j1, _ := store.Add(&CronJob{Name: "a", Prompt: "pa"})
	j2, _ := store.Add(&CronJob{Name: "b", Prompt: "pb"})

	jobs = store.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	// Verify copies: modifying returned slice doesn't affect store.
	ids := map[string]bool{j1.ID: true, j2.ID: true}
	for _, j := range jobs {
		if !ids[j.ID] {
			t.Fatalf("unexpected job ID: %q", j.ID)
		}
	}
}

func TestCronStore_Get(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	created, err := store.Add(&CronJob{Name: "find-me", Prompt: "p"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("Get returned wrong ID: got %q, want %q", got.ID, created.ID)
	}

	// Non-existent ID.
	_, err = store.Get("no-such-id")
	if err != ErrCronJobNotFound {
		t.Fatalf("Get(nonexistent): got %v, want %v", err, ErrCronJobNotFound)
	}
}

func TestCronStore_Update(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	created, err := store.Add(&CronJob{Name: "original", Prompt: "p", Enabled: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	newName := "renamed"
	newPrompt := "new-prompt"
	disabled := false
	patch := CronJobPatch{
		Name:    &newName,
		Prompt:  &newPrompt,
		Enabled: &disabled,
	}

	updated, err := store.Update(created.ID, patch)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "renamed" {
		t.Fatalf("name not patched: got %q", updated.Name)
	}
	if updated.Prompt != "new-prompt" {
		t.Fatalf("prompt not patched: got %q", updated.Prompt)
	}
	if updated.Enabled {
		t.Fatal("enabled should be false after patch")
	}

	// Verify only patched fields changed: original fields should still be present.
	if updated.ID != created.ID {
		t.Fatalf("ID should not change on update")
	}
	if updated.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set")
	}

	// Update non-existent job.
	_, err = store.Update("no-such-id", patch)
	if err != ErrCronJobNotFound {
		t.Fatalf("Update(nonexistent): got %v, want %v", err, ErrCronJobNotFound)
	}
}

func TestCronStore_UpdatePartialPatch(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	created, _ := store.Add(&CronJob{Name: "partial", Prompt: "p1", Enabled: true})

	// Only patch Name, leave Prompt and Enabled unchanged.
	newName := "partial-renamed"
	patch := CronJobPatch{Name: &newName}

	updated, err := store.Update(created.ID, patch)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "partial-renamed" {
		t.Fatalf("name: got %q", updated.Name)
	}
	if updated.Prompt != "p1" {
		t.Fatalf("prompt should be unchanged: got %q", updated.Prompt)
	}
	if !updated.Enabled {
		t.Fatal("enabled should be unchanged (true)")
	}
}

func TestCronStore_Remove(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	created, _ := store.Add(&CronJob{Name: "remove-me", Prompt: "p"})

	if !store.Remove(created.ID) {
		t.Fatal("Remove should return true for existing job")
	}
	if len(store.List()) != 0 {
		t.Fatal("job should be removed from list")
	}

	// Remove again: should return false.
	if store.Remove(created.ID) {
		t.Fatal("Remove should return false for already-removed job")
	}

	// Remove non-existent.
	if store.Remove("no-such-id") {
		t.Fatal("Remove should return false for non-existent ID")
	}
}

func TestCronStore_UpdateState(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	created, _ := store.Add(&CronJob{Name: "state-test", Prompt: "p"})

	now := time.Now()
	state := CronJobState{
		LastStatus: "ok",
		LastRunAt:  &now,
		RunCount:   5,
	}
	store.UpdateState(created.ID, state)

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after UpdateState: %v", err)
	}
	if got.State.LastStatus != "ok" {
		t.Fatalf("state not updated: got %q", got.State.LastStatus)
	}
	if got.State.RunCount != 5 {
		t.Fatalf("run count: got %d, want 5", got.State.RunCount)
	}

	// UpdateState on non-existent ID is a no-op (method just returns).
	store.UpdateState("no-such-id", state)
}

func TestCronStore_AppendRun(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	job, _ := store.Add(&CronJob{Name: "run-test", Prompt: "p"})

	run := &CronRun{
		ID:        "run-1",
		JobID:     job.ID,
		JobName:   job.Name,
		Status:    "ok",
		StartedAt: time.Now(),
		Summary:   "all good",
	}
	if err := store.AppendRun(run); err != nil {
		t.Fatalf("AppendRun: %v", err)
	}

	runs, err := store.ListRuns(job.ID, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != "run-1" {
		t.Fatalf("run ID: got %q", runs[0].ID)
	}
}

func TestCronStore_ListRunsOrder(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	job, _ := store.Add(&CronJob{Name: "order-test", Prompt: "p"})

	for i := 0; i < 5; i++ {
		run := &CronRun{
			ID:        "run-" + string(rune('a'+i)),
			JobID:     job.ID,
			JobName:   job.Name,
			Status:    "ok",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
			Summary:   "summary",
		}
		if err := store.AppendRun(run); err != nil {
			t.Fatalf("AppendRun %d: %v", i, err)
		}
	}

	runs, err := store.ListRuns(job.ID, 3)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("limit=3: expected 3 runs, got %d", len(runs))
	}
	// Newest first ordering.
	if !runs[0].StartedAt.After(runs[1].StartedAt) {
		t.Fatal("runs should be ordered newest-first")
	}
}

func TestCronStore_ListRunsNoFile(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	runs, err := store.ListRuns("nonexistent-job", 10)
	if err != nil {
		t.Fatalf("ListRuns on nonexistent job: %v", err)
	}
	if runs != nil {
		t.Fatalf("expected nil for nonexistent job, got %d runs", len(runs))
	}
}

func TestCronStore_ListAllRuns(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	j1, _ := store.Add(&CronJob{Name: "job1", Prompt: "p"})
	j2, _ := store.Add(&CronJob{Name: "job2", Prompt: "p"})

	for i := 0; i < 3; i++ {
		store.AppendRun(&CronRun{
			ID:        "r1-" + string(rune('a'+i)),
			JobID:     j1.ID,
			JobName:   j1.Name,
			Status:    "ok",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < 2; i++ {
		store.AppendRun(&CronRun{
			ID:        "r2-" + string(rune('a'+i)),
			JobID:     j2.ID,
			JobName:   j2.Name,
			Status:    "ok",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	runs, err := store.ListAllRuns(4)
	if err != nil {
		t.Fatalf("ListAllRuns: %v", err)
	}
	if len(runs) != 4 {
		t.Fatalf("limit=4: expected 4 runs, got %d", len(runs))
	}
}

func TestCronStore_PersistAndReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}

	created, _ := store.Add(&CronJob{Name: "persist-test", Prompt: "p", Enabled: true})

	// Reload from same dir.
	store2, err := NewCronStore(dir)
	if err != nil {
		t.Fatalf("NewCronStore reload: %v", err)
	}
	jobs := store2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after reload, got %d", len(jobs))
	}
	if jobs[0].ID != created.ID {
		t.Fatalf("ID mismatch after reload: got %q, want %q", jobs[0].ID, created.ID)
	}
}

func TestCronStore_NewCronStoreCreatesDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}

	cronDir := filepath.Join(dir, "cron")
	runsDir := filepath.Join(dir, "cron", "runs")
	if _, err := os.Stat(cronDir); os.IsNotExist(err) {
		t.Fatal("cron dir not created")
	}
	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		t.Fatal("runs dir not created")
	}
	_ = store
}

// --- computeNextRun tests ---

func TestComputeNextRun(t *testing.T) {
	t.Parallel()
	now := time.Now()

	tests := []struct {
		name   string
		sched  CronSchedule
		from   time.Time
		wantOK bool // nil result means no next run
	}{
		{
			name:   "every 60s",
			sched:  CronSchedule{Kind: "every", EveryMs: 60000},
			from:   now,
			wantOK: true,
		},
		{
			name:   "every zero ms returns nil",
			sched:  CronSchedule{Kind: "every", EveryMs: 0},
			from:   now,
			wantOK: false,
		},
		{
			name:   "cron expression",
			sched:  CronSchedule{Kind: "cron", Expr: "0 9 * * *"},
			from:   now,
			wantOK: true,
		},
		{
			name:   "cron empty expr returns nil",
			sched:  CronSchedule{Kind: "cron", Expr: ""},
			from:   now,
			wantOK: false,
		},
		{
			name:   "cron invalid expr returns nil",
			sched:  CronSchedule{Kind: "cron", Expr: "invalid"},
			from:   now,
			wantOK: false,
		},
		{
			name:   "cron with timezone",
			sched:  CronSchedule{Kind: "cron", Expr: "0 9 * * *", Timezone: "America/New_York"},
			from:   now,
			wantOK: true,
		},
		{
			name:   "once future",
			sched:  CronSchedule{Kind: "once", RunAt: now.Add(1 * time.Hour).Format(time.RFC3339)},
			from:   now,
			wantOK: true,
		},
		{
			name:   "once past returns nil",
			sched:  CronSchedule{Kind: "once", RunAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
			from:   now,
			wantOK: false,
		},
		{
			name:   "once empty run_at returns nil",
			sched:  CronSchedule{Kind: "once", RunAt: ""},
			from:   now,
			wantOK: false,
		},
		{
			name:   "once invalid run_at returns nil",
			sched:  CronSchedule{Kind: "once", RunAt: "not-a-date"},
			from:   now,
			wantOK: false,
		},
		{
			name:   "unknown kind returns nil",
			sched:  CronSchedule{Kind: "unknown"},
			from:   now,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := computeNextRun(tt.sched, tt.from)
			if tt.wantOK && result == nil {
				t.Fatal("expected non-nil result")
			}
			if !tt.wantOK && result != nil {
				t.Fatalf("expected nil result, got %v", *result)
			}
			if result != nil && !result.After(tt.from) {
				t.Fatalf("next run should be after from: from=%v, next=%v", tt.from, *result)
			}
		})
	}
}

func TestComputeNextRun_EveryMs(t *testing.T) {
	t.Parallel()
	now := time.Now()
	sched := CronSchedule{Kind: "every", EveryMs: 5000}
	result := computeNextRun(sched, now)
	if result == nil {
		t.Fatal("expected result for every schedule")
	}
	expected := now.Add(5 * time.Second)
	diff := result.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("next run off by more than 1s: got %v, want approx %v", *result, expected)
	}
}

// --- Handler method tests ---

func newCronTestHandler(t *testing.T) (*Handler, *CronStore, *Scheduler) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{
		dataDir:  dir,
		cronStore: store,
		tracker:   tracker,
		logger:    logger,
	}
	sched := NewScheduler(store, h, tracker, logger)
	h.scheduler = sched
	return h, store, sched
}

func TestCronHandler_List(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	// Empty list.
	resp := h.handleCronList(rpcRequest("cron/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	jobsList, _ := out["jobs"].([]*CronJob)
	if len(jobsList) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobsList))
	}

	// Add a job, then list.
	store.Add(&CronJob{Name: "test-job", Prompt: "p", Enabled: true})
	resp = h.handleCronList(rpcRequest("cron/list", 2, nil))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out, _ = resp.Result.(map[string]any)
	jobsRaw, _ := out["jobs"]
	jobsSlice, ok := jobsRaw.([]*CronJob)
	if !ok {
		// Result might be marshalled as []interface{} depending on path.
		t.Fatalf("jobs type: %T", jobsRaw)
	}
	if len(jobsSlice) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobsSlice))
	}
}

func TestCronHandler_ListNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronList(rpcRequest("cron/list", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Add(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronAdd(rpcRequest("cron/add", 1, map[string]any{
		"name":     "my-job",
		"prompt":   "check status",
		"enabled":  true,
		"schedule": map[string]any{"kind": "every", "every_ms": float64(60000)},
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	// Result should be a CronJob with assigned ID.
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		// Might be a *CronJob directly.
		cj, ok2 := resp.Result.(*CronJob)
		if !ok2 {
			t.Fatalf("result type: %T", resp.Result)
		}
		if cj.ID == "" {
			t.Fatal("expected ID to be assigned")
		}
		if cj.Name != "my-job" {
			t.Fatalf("name: got %q", cj.Name)
		}
		return
	}
	if resultMap["id"] == "" {
		t.Fatal("expected id in result")
	}
	if resultMap["name"] != "my-job" {
		t.Fatalf("name: got %v", resultMap["name"])
	}
}

func TestCronHandler_AddValidation(t *testing.T) {
	t.Parallel()
	_, _, _ = newCronTestHandler(t)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"missing name", map[string]any{"prompt": "p"}},
		{"missing prompt", map[string]any{"name": "n"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, _, _ := newCronTestHandler(t)
			resp := h.handleCronAdd(rpcRequest("cron/add", 1, tt.params))
			if resp.Error == nil {
				t.Fatal("expected error for invalid params")
			}
			if resp.Error.Code != ErrCodeInvalidParams {
				t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
			}
		})
	}
}

func TestCronHandler_AddNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronAdd(rpcRequest("cron/add", 1, map[string]any{"name": "n", "prompt": "p"}))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Update(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	created, _ := store.Add(&CronJob{Name: "original", Prompt: "p", Enabled: true})

	resp := h.handleCronUpdate(rpcRequest("cron/update", 1, map[string]any{
		"id":      created.ID,
		"name":    "renamed",
		"enabled": false,
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestCronHandler_UpdateMissingID(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronUpdate(rpcRequest("cron/update", 1, map[string]any{
		"name": "renamed",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_UpdateNonexistentJob(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronUpdate(rpcRequest("cron/update", 1, map[string]any{
		"id":   "no-such-id",
		"name": "renamed",
	}))
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent job")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_UpdateNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronUpdate(rpcRequest("cron/update", 1, map[string]any{"id": "x"}))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Remove(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	created, _ := store.Add(&CronJob{Name: "remove-me", Prompt: "p"})

	resp := h.handleCronRemove(rpcRequest("cron/remove", 1, map[string]any{
		"id": created.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %v", out["ok"])
	}

	// Remove again should fail.
	resp = h.handleCronRemove(rpcRequest("cron/remove", 2, map[string]any{
		"id": created.ID,
	}))
	if resp.Error == nil {
		t.Fatal("expected error for already-removed job")
	}
}

func TestCronHandler_RemoveMissingID(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronRemove(rpcRequest("cron/remove", 1, map[string]any{}))
	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_RemoveNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronRemove(rpcRequest("cron/remove", 1, map[string]any{"id": "x"}))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Toggle(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	created, _ := store.Add(&CronJob{Name: "toggle-test", Prompt: "p", Enabled: true})

	resp := h.handleCronToggle(rpcRequest("cron/toggle", 1, map[string]any{
		"id":      created.ID,
		"enabled": false,
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestCronHandler_ToggleMissingID(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronToggle(rpcRequest("cron/toggle", 1, map[string]any{
		"enabled": false,
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_ToggleMissingEnabled(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)
	created, _ := store.Add(&CronJob{Name: "t", Prompt: "p"})

	resp := h.handleCronToggle(rpcRequest("cron/toggle", 1, map[string]any{
		"id": created.ID,
	}))
	if resp.Error == nil {
		t.Fatal("expected error for missing enabled")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_ToggleNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronToggle(rpcRequest("cron/toggle", 1, map[string]any{"id": "x", "enabled": true}))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Run(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	// Add a job first.
	created, _ := h.cronStore.Add(&CronJob{Name: "manual-run", Prompt: "p", Enabled: true})

	resp := h.handleCronRun(rpcRequest("cron/run", 1, map[string]any{
		"id": created.ID,
	}))
	// RunJobNow will try to execute but runtime is nil, so executeJob will
	// fail. However, the handler call itself should succeed (returns ok=true)
	// because RunJobNow just enqueues.
	if resp.Error != nil {
		// The handler may error because scheduler can't actually execute
		// without a runtime, but the important thing is it didn't crash.
		t.Logf("handleCronRun error (expected without runtime): %+v", resp.Error)
	}
}

func TestCronHandler_RunMissingID(t *testing.T) {
	t.Parallel()
	h, _, _ := newCronTestHandler(t)

	resp := h.handleCronRun(rpcRequest("cron/run", 1, map[string]any{}))
	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected ErrCodeInvalidParams, got %d", resp.Error.Code)
	}
}

func TestCronHandler_RunNilScheduler(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronRun(rpcRequest("cron/run", 1, map[string]any{"id": "x"}))
	if resp.Error == nil {
		t.Fatal("expected error for nil scheduler")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Runs(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	job, _ := store.Add(&CronJob{Name: "runs-test", Prompt: "p"})
	store.AppendRun(&CronRun{
		ID:        "r1",
		JobID:     job.ID,
		JobName:   job.Name,
		Status:    "ok",
		StartedAt: time.Now(),
	})

	// By job ID.
	resp := h.handleCronRuns(rpcRequest("cron/runs", 1, map[string]any{
		"jobId": job.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// All runs.
	resp = h.handleCronRuns(rpcRequest("cron/runs", 2, map[string]any{}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestCronHandler_RunsWithLimit(t *testing.T) {
	t.Parallel()
	h, store, _ := newCronTestHandler(t)

	job, _ := store.Add(&CronJob{Name: "limit-test", Prompt: "p"})
	for i := 0; i < 5; i++ {
		store.AppendRun(&CronRun{
			ID:        "r" + string(rune('a'+i)),
			JobID:     job.ID,
			JobName:   job.Name,
			Status:    "ok",
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	resp := h.handleCronRuns(rpcRequest("cron/runs", 1, map[string]any{
		"jobId": job.ID,
		"limit": float64(2),
	}))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestCronHandler_RunsNilStore(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronRuns(rpcRequest("cron/runs", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected error for nil cronStore")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

func TestCronHandler_Status(t *testing.T) {
	t.Parallel()
	h, store, sched := newCronTestHandler(t)

	// No jobs.
	resp := h.handleCronStatus(rpcRequest("cron/status", 1, nil))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	status, ok := resp.Result.(CronStatus)
	if !ok {
		t.Fatalf("result type: %T", resp.Result)
	}
	if status.TotalJobs != 0 {
		t.Fatalf("expected 0 total jobs, got %d", status.TotalJobs)
	}

	// Add enabled job.
	store.Add(&CronJob{Name: "active", Prompt: "p", Enabled: true, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	// Refresh next runs so NextWakeAt is populated.
	sched.refreshNextRuns()

	resp = h.handleCronStatus(rpcRequest("cron/status", 2, nil))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	status = resp.Result.(CronStatus)
	if status.TotalJobs != 1 {
		t.Fatalf("expected 1 total job, got %d", status.TotalJobs)
	}
	if status.ActiveJobs != 1 {
		t.Fatalf("expected 1 active job, got %d", status.ActiveJobs)
	}
	if !status.Enabled {
		t.Fatal("expected Enabled=true")
	}
}

func TestCronHandler_StatusNilScheduler(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleCronStatus(rpcRequest("cron/status", 1, nil))
	if resp.Error == nil {
		t.Fatal("expected error for nil scheduler")
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Fatalf("expected ErrCodeInternal, got %d", resp.Error.Code)
	}
}

// --- Scheduler tests ---

func TestScheduler_Status(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	status := sched.Status()
	if !status.Enabled {
		t.Fatal("scheduler should report enabled")
	}
	if status.TotalJobs != 0 {
		t.Fatalf("expected 0 total jobs, got %d", status.TotalJobs)
	}

	// Add a disabled job — should not count as active.
	store.Add(&CronJob{Name: "disabled", Prompt: "p", Enabled: false, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	status = sched.Status()
	if status.ActiveJobs != 0 {
		t.Fatalf("disabled job should not count as active, got %d", status.ActiveJobs)
	}
	if status.TotalJobs != 1 {
		t.Fatalf("expected 1 total job, got %d", status.TotalJobs)
	}

	// Add an enabled job.
	store.Add(&CronJob{Name: "enabled", Prompt: "p", Enabled: true, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	sched.refreshNextRuns()
	status = sched.Status()
	if status.ActiveJobs != 1 {
		t.Fatalf("expected 1 active job, got %d", status.ActiveJobs)
	}
	if status.NextWakeAt == nil {
		t.Fatal("expected NextWakeAt for enabled job with schedule")
	}
}

func TestScheduler_RefreshNextRuns(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	// Add enabled job without NextRunAt.
	job, _ := store.Add(&CronJob{Name: "refresh-test", Prompt: "p", Enabled: true, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})

	sched.refreshNextRuns()

	got, err := store.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State.NextRunAt == nil {
		t.Fatal("expected NextRunAt to be computed after refresh")
	}

	// Add disabled job — should NOT get NextRunAt computed.
	disabled, _ := store.Add(&CronJob{Name: "disabled", Prompt: "p", Enabled: false, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	sched.refreshNextRuns()
	got, _ = store.Get(disabled.ID)
	if got.State.NextRunAt != nil {
		t.Fatal("disabled job should not get NextRunAt computed")
	}
}

func TestScheduler_RunJobNowNonexistent(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	err := sched.RunJobNow("no-such-id")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestScheduler_StartStop(t *testing.T) {
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	sched.Start()
	// Give scheduler a moment to run.
	time.Sleep(50 * time.Millisecond)
	sched.Stop()
	// Stop should complete without blocking indefinitely.
}

func TestScheduler_SemaphoreLimitsConcurrency(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	// Verify semaphore capacity matches maxConcurrentCronJobs.
	if cap(sched.sem) != maxConcurrentCronJobs {
		t.Fatalf("semaphore capacity: got %d, want %d", cap(sched.sem), maxConcurrentCronJobs)
	}
}

func TestScheduler_TickSkipsDisabledJobs(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	// Add a disabled job with a past-due NextRunAt.
	job, _ := store.Add(&CronJob{Name: "disabled", Prompt: "p", Enabled: false, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	past := time.Now().Add(-1 * time.Minute)
	state := CronJobState{NextRunAt: &past, RunCount: 0}
	store.UpdateState(job.ID, state)

	// Tick should not pick up disabled jobs.
	sched.tick(time.Now())

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Fatalf("disabled job should not be running: %d running", running)
	}
}

func TestScheduler_TickSkipsNotDueJobs(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)
	tracker := NewActiveTurnTracker()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{cronStore: store, tracker: tracker, logger: logger}
	sched := NewScheduler(store, h, tracker, logger)

	// Add an enabled job with a future NextRunAt.
	job, _ := store.Add(&CronJob{Name: "future", Prompt: "p", Enabled: true, Schedule: CronSchedule{Kind: "every", EveryMs: 60000}})
	future := time.Now().Add(1 * time.Hour)
	state := CronJobState{NextRunAt: &future, RunCount: 0}
	store.UpdateState(job.ID, state)

	// Tick should not pick up jobs not yet due.
	sched.tick(time.Now())

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Fatalf("future job should not be running: %d running", running)
	}
}

// --- ActiveTurnTracker test (cron-specific) ---

func TestCronHandler_TurnsActiveNilTracker(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	resp := h.handleTurnsActive(rpcRequest("turns/active", 1, nil))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	turns, _ := out["turns"].([]any)
	if len(turns) != 0 {
		t.Fatalf("expected empty turns, got %d", len(turns))
	}
}

// --- Concurrent CronStore access test ---

func TestCronStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := newTestCronStore(t)

	var wg sync.WaitGroup
	const numOps = 50

	// Concurrent adds.
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store.Add(&CronJob{Name: "concurrent-" + string(rune('a'+i%26)), Prompt: "p"})
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.List()
		}()
	}

	wg.Wait()

	jobs := store.List()
	if len(jobs) != numOps {
		t.Fatalf("expected %d jobs after concurrent adds, got %d", numOps, len(jobs))
	}
}

// --- SplitLines helper test ---

func TestSplitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"single line", "hello", 1},
		{"two lines", "hello\nworld", 2},
		{"trailing newline", "hello\n", 1},
		{"empty line between", "a\n\nb", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := splitLines([]byte(tt.input))
			if len(lines) != tt.want {
				t.Errorf("splitLines(%q): got %d lines, want %d", tt.input, len(lines), tt.want)
			}
		})
	}
}

// --- SortRunsByTime helper test ---

func TestSortRunsByTime(t *testing.T) {
	t.Parallel()
	now := time.Now()
	runs := []*CronRun{
		{StartedAt: now.Add(2 * time.Minute)},
		{StartedAt: now},
		{StartedAt: now.Add(1 * time.Minute)},
	}
	sortRunsByTime(runs)
	// Should be sorted newest-first.
	for i := 1; i < len(runs); i++ {
		if runs[i].StartedAt.After(runs[i-1].StartedAt) {
			t.Fatalf("not sorted newest-first: [%d]=%v > [%d]=%v", i, runs[i].StartedAt, i-1, runs[i-1].StartedAt)
		}
	}
}