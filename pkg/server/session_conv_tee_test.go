package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/conversation"
)

// openConvStoreForServerTest opens a fresh SQLite-backed conversation.Store
// inside the test's TempDir and registers cleanup. Returns the store ready
// to be wrapped in newConvTee.
func openConvStoreForServerTest(t *testing.T) *conversation.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := conversation.Open(conversation.Config{FallbackPath: filepath.Join(dir, "conv.db")})
	if err != nil {
		t.Fatalf("conversation.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newSessionStoreWithTee returns a SessionStore wired to a fresh
// conversation.Store under the given projectID. Both the session store and
// the conv store share the test's TempDir so they're cleaned up together.
func newSessionStoreWithTee(t *testing.T, projectID string) (*SessionStore, *conversation.Store) {
	t.Helper()
	conv := openConvStoreForServerTest(t)
	store, err := NewSessionStore()
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	store.AttachConvTee(newConvTee(conv, projectID, nil))
	return store, conv
}

func TestConvTee_NilSafe(t *testing.T) {
	t.Parallel()
	// nil tee receiver: every record* call must be a no-op.
	var nilTee *convTee
	nilTee.recordThreadCreate("tid", "title")
	nilTee.recordThreadDelete("tid")
	nilTee.recordThreadTitleUpdate("tid", "title")
	nilTee.recordItem("tid", "user", "hi", "turn", nil)
	nilTee.recordToolItem("tid", "search", "hit", "turn", nil)

	// AttachConvTee(nil) must keep SessionStore working without dual-write.
	store, err := NewSessionStore()
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	store.AttachConvTee(nil)
	thread := store.CreateThread("plain")
	if thread.ID == "" {
		t.Fatal("CreateThread should still return a thread when no tee attached")
	}
	store.AppendItem(thread.ID, "user", "hello", "t1")
	if got := store.GetItems(thread.ID); len(got) != 1 {
		t.Fatalf("GetItems = %d, want 1", len(got))
	}
}

func TestConvTee_CreateThreadMirrors(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")

	thread := store.CreateThread("hello world")
	got, err := conv.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("conv.GetThread: %v", err)
	}
	if got.ID != thread.ID {
		t.Errorf("conv thread.ID = %q, want %q", got.ID, thread.ID)
	}
	if got.ProjectID != "default" {
		t.Errorf("conv thread.ProjectID = %q, want %q", got.ProjectID, "default")
	}
	if got.OwnerUserID != convTeeWebOwnerUserID {
		t.Errorf("conv thread.OwnerUserID = %q, want %q", got.OwnerUserID, convTeeWebOwnerUserID)
	}
	if got.Client != convTeeWebClient {
		t.Errorf("conv thread.Client = %q, want %q", got.Client, convTeeWebClient)
	}
	if got.Title != "hello world" {
		t.Errorf("conv thread.Title = %q, want %q", got.Title, "hello world")
	}
}

func TestConvTee_AppendItemMirrors(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "proj-1")
	thread := store.CreateThread("t")

	store.AppendItem(thread.ID, "user", "ping", "turn-1")
	store.AppendItem(thread.ID, "assistant", "pong", "turn-1")

	events, err := conv.GetEvents(context.Background(), thread.ID, conversation.GetEventsOpts{Limit: 100})
	if err != nil {
		t.Fatalf("conv.GetEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Kind != string(conversation.EventKindUserMessage) {
		t.Errorf("events[0].Kind = %q, want %q", events[0].Kind, conversation.EventKindUserMessage)
	}
	if events[0].ContentText != "ping" {
		t.Errorf("events[0].ContentText = %q, want ping", events[0].ContentText)
	}
	if events[0].TurnID != "turn-1" {
		t.Errorf("events[0].TurnID = %q, want turn-1", events[0].TurnID)
	}
	if events[1].Kind != string(conversation.EventKindAssistantText) {
		t.Errorf("events[1].Kind = %q, want %q", events[1].Kind, conversation.EventKindAssistantText)
	}
	if events[1].ContentText != "pong" {
		t.Errorf("events[1].ContentText = %q, want pong", events[1].ContentText)
	}
	// Both events share the same turn id (same HTTP request).
	if events[0].TurnID != events[1].TurnID {
		t.Errorf("events should share turn id: %q vs %q", events[0].TurnID, events[1].TurnID)
	}
	if events[0].ProjectID != "proj-1" {
		t.Errorf("events[0].ProjectID = %q, want proj-1", events[0].ProjectID)
	}
}

func TestConvTee_AppendItemWithArtifactsEncodesArtifacts(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")
	thread := store.CreateThread("t")

	artifacts := []Artifact{{Type: "image", URL: "https://example.com/x.png", Name: "x.png"}}
	store.AppendItemWithArtifacts(thread.ID, "user", "see", "turn-1", artifacts)

	events, err := conv.GetEvents(context.Background(), thread.ID, conversation.GetEventsOpts{Limit: 100})
	if err != nil {
		t.Fatalf("conv.GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Kind != string(conversation.EventKindUserMessage) {
		t.Errorf("events[0].Kind = %q, want UserMessage", events[0].Kind)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].ContentJSON, &payload); err != nil {
		t.Fatalf("decode ContentJSON: %v (raw=%q)", err, string(events[0].ContentJSON))
	}
	arts, ok := payload["artifacts"].([]any)
	if !ok || len(arts) != 1 {
		t.Fatalf("artifacts in payload = %v, want one entry", payload["artifacts"])
	}
}

func TestConvTee_AppendToolItemDemotedToSystem(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")
	thread := store.CreateThread("t")

	// SessionStore loses tool_call_id, so the tee must demote to System and
	// keep tool_name in ContentJSON for forensics.
	store.AppendToolItem(thread.ID, "search_web", `{"hits":3}`, "turn-1", nil)

	events, err := conv.GetEvents(context.Background(), thread.ID, conversation.GetEventsOpts{Limit: 100})
	if err != nil {
		t.Fatalf("conv.GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Kind != string(conversation.EventKindSystem) {
		t.Errorf("events[0].Kind = %q, want System (demoted)", events[0].Kind)
	}
	if events[0].Role != "tool" {
		t.Errorf("events[0].Role = %q, want tool (preserved)", events[0].Role)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].ContentJSON, &payload); err != nil {
		t.Fatalf("decode ContentJSON: %v", err)
	}
	if name, _ := payload["tool_name"].(string); name != "search_web" {
		t.Errorf("payload.tool_name = %v, want search_web", payload["tool_name"])
	}
}

func TestConvTee_UpdateThreadTitleMirrors(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")
	thread := store.CreateThread("orig")

	if !store.UpdateThreadTitle(thread.ID, "renamed") {
		t.Fatal("UpdateThreadTitle returned false for known thread")
	}

	got, err := conv.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("conv.GetThread: %v", err)
	}
	if got.Title != "renamed" {
		t.Errorf("conv thread.Title = %q, want renamed", got.Title)
	}
}

func TestConvTee_DeleteThreadSoftDeletes(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")
	thread := store.CreateThread("doomed")
	store.AppendItem(thread.ID, "user", "alive", "turn-1")

	if !store.DeleteThread(thread.ID) {
		t.Fatal("DeleteThread returned false for known thread")
	}

	// Soft delete: GetThread treats soft-deleted rows as not found, and
	// ListThreads must hide it. Pre-existing events survive — that's the
	// whole point of soft delete (forensics still need them).
	if _, err := conv.GetThread(context.Background(), thread.ID); err == nil {
		t.Error("conv.GetThread after soft delete: want ErrThreadNotFound, got nil")
	}
	threads, err := conv.ListThreads(context.Background(), "default", conversation.ListThreadsOpts{})
	if err != nil {
		t.Fatalf("conv.ListThreads: %v", err)
	}
	for _, th := range threads {
		if th.ID == thread.ID {
			t.Errorf("soft-deleted thread should not appear in ListThreads")
		}
	}
	events, err := conv.GetEvents(context.Background(), thread.ID, conversation.GetEventsOpts{Limit: 100})
	if err != nil {
		t.Fatalf("conv.GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events after soft delete = %d, want 1", len(events))
	}
}

func TestConvTee_ProjectIsolation(t *testing.T) {
	t.Parallel()
	conv := openConvStoreForServerTest(t)

	storeA, err := NewSessionStore()
	if err != nil {
		t.Fatalf("NewSessionStore A: %v", err)
	}
	storeA.AttachConvTee(newConvTee(conv, "proj-A", nil))

	storeB, err := NewSessionStore()
	if err != nil {
		t.Fatalf("NewSessionStore B: %v", err)
	}
	storeB.AttachConvTee(newConvTee(conv, "proj-B", nil))

	threadA := storeA.CreateThread("A1")
	threadB := storeB.CreateThread("B1")

	listA, err := conv.ListThreads(context.Background(), "proj-A", conversation.ListThreadsOpts{})
	if err != nil {
		t.Fatalf("ListThreads proj-A: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != threadA.ID {
		t.Errorf("proj-A list = %+v, want only threadA=%q", listA, threadA.ID)
	}
	listB, err := conv.ListThreads(context.Background(), "proj-B", conversation.ListThreadsOpts{})
	if err != nil {
		t.Fatalf("ListThreads proj-B: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != threadB.ID {
		t.Errorf("proj-B list = %+v, want only threadB=%q", listB, threadB.ID)
	}
}

func TestConvTee_EmptyTurnIDSynthesized(t *testing.T) {
	t.Parallel()
	store, conv := newSessionStoreWithTee(t, "default")
	thread := store.CreateThread("t")

	// AppendEvent rejects empty turnID — the tee must synthesize one rather
	// than dropping the event.
	store.AppendItem(thread.ID, "user", "no turn", "")

	events, err := conv.GetEvents(context.Background(), thread.ID, conversation.GetEventsOpts{Limit: 100})
	if err != nil {
		t.Fatalf("conv.GetEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1 (event was dropped)", len(events))
	}
	if events[0].TurnID == "" {
		t.Error("synthesized turn id should be non-empty")
	}
}

func TestConvTee_RoleClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role     string
		wantKind conversation.EventKind
		wantRole string
	}{
		{"user", conversation.EventKindUserMessage, "user"},
		{"USER", conversation.EventKindUserMessage, "user"},
		{"assistant", conversation.EventKindAssistantText, "assistant"},
		{"system", conversation.EventKindSystem, "system"},
		{"developer", conversation.EventKindSystem, "system"},
		{"tool", conversation.EventKindSystem, "tool"},
		{"function", conversation.EventKindSystem, "tool"},
		{"weird", conversation.EventKindSystem, "weird"},
	}
	for _, tc := range cases {
		gotKind, gotRole := classifyConvTeeEvent(tc.role)
		if gotKind != tc.wantKind {
			t.Errorf("role %q: kind = %q, want %q", tc.role, gotKind, tc.wantKind)
		}
		if gotRole != tc.wantRole {
			t.Errorf("role %q: normalized = %q, want %q", tc.role, gotRole, tc.wantRole)
		}
	}
}

func TestConvTee_BlankProjectIDFallback(t *testing.T) {
	t.Parallel()
	conv := openConvStoreForServerTest(t)
	tee := newConvTee(conv, "   ", nil)
	if tee == nil {
		t.Fatal("newConvTee should not return nil for blank projectID")
	}
	if tee.projectID != "default" {
		t.Errorf("tee.projectID = %q, want default", tee.projectID)
	}
}

func TestConvTee_NilStoreReturnsNilTee(t *testing.T) {
	t.Parallel()
	if got := newConvTee(nil, "any", nil); got != nil {
		t.Errorf("newConvTee(nil, ...) = %+v, want nil", got)
	}
}
