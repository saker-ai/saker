package conversation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrThreadNotFound is returned when a thread lookup misses (or the
// thread is soft-deleted and the caller didn't opt into IncludeDeleted).
var ErrThreadNotFound = errors.New("conversation: thread not found")

// CreateThread inserts a new thread row. ID is server-assigned (UUIDv4)
// so concurrent creates from different nodes never collide. ProjectID and
// OwnerUserID are required — empty values are rejected because they are
// load-bearing for multi-tenant isolation.
func (s *Store) CreateThread(ctx context.Context, projectID, ownerUserID, title, client string) (*Thread, error) {
	return s.CreateThreadWithID(ctx, uuid.New().String(), projectID, ownerUserID, title, client)
}

// CreateThreadWithID inserts a thread row using a caller-supplied ID so
// external entry points (OpenAI gateway session_id, CLI session id) can
// thread their own stable identifier through the store. Use CreateThread
// when no external id exists. Returns the persisted Thread on success.
//
// Validation mirrors CreateThread: projectID and ownerUserID are
// load-bearing for multi-tenant isolation and rejected when empty. id
// must also be non-empty — pass uuid.New().String() if you don't have
// an external id (or just call CreateThread).
func (s *Store) CreateThreadWithID(ctx context.Context, id, projectID, ownerUserID, title, client string) (*Thread, error) {
	if id == "" {
		return nil, errors.New("conversation.CreateThreadWithID: id required")
	}
	if projectID == "" {
		return nil, errors.New("conversation.CreateThreadWithID: projectID required")
	}
	if ownerUserID == "" {
		return nil, errors.New("conversation.CreateThreadWithID: ownerUserID required")
	}
	now := nowUTC()
	t := &Thread{
		ID:          id,
		ProjectID:   projectID,
		OwnerUserID: ownerUserID,
		Title:       title,
		Client:      client,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.withCtx(ctx).Create(t).Error; err != nil {
		return nil, fmt.Errorf("conversation.CreateThreadWithID: %w", err)
	}
	return t, nil
}

// GetThread fetches a single thread by ID. Returns ErrThreadNotFound if
// the row is missing OR soft-deleted (callers needing the deleted row
// should hit DB() directly with .Unscoped()).
func (s *Store) GetThread(ctx context.Context, threadID string) (*Thread, error) {
	if threadID == "" {
		return nil, errors.New("conversation.GetThread: threadID required")
	}
	var t Thread
	err := s.withCtx(ctx).
		Where("id = ? AND deleted_at IS NULL", threadID).
		Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrThreadNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetThread: %w", err)
	}
	return &t, nil
}

// ListThreads returns threads inside a project, sorted by updated_at
// descending so the most recently active conversations come first
// (matches what every chat UI wants by default).
//
// projectID is required — there is no "list all threads across projects"
// API by design. Cross-project listing must be the explicit responsibility
// of an admin tool that opens a separate path.
func (s *Store) ListThreads(ctx context.Context, projectID string, opts ListThreadsOpts) ([]Thread, error) {
	if projectID == "" {
		return nil, errors.New("conversation.ListThreads: projectID required")
	}
	if opts.Offset < 0 {
		return nil, errors.New("conversation.ListThreads: negative offset rejected")
	}

	q := s.withCtx(ctx).Model(&Thread{}).Where("project_id = ?", projectID)
	if !opts.IncludeDeleted {
		q = q.Where("deleted_at IS NULL")
	}
	if opts.OwnerUserID != "" {
		q = q.Where("owner_user_id = ?", opts.OwnerUserID)
	}
	if opts.Client != "" {
		q = q.Where("client = ?", opts.Client)
	}
	q = q.Order("updated_at DESC").Order("id ASC"). // tiebreak so paging is stable
								Limit(clampLimit(opts.Limit)).
								Offset(opts.Offset)

	var out []Thread
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("conversation.ListThreads: %w", err)
	}
	return out, nil
}

// UpdateThreadTitle replaces the title and bumps updated_at. Returns
// ErrThreadNotFound if the row is missing or soft-deleted.
func (s *Store) UpdateThreadTitle(ctx context.Context, threadID, title string) error {
	if threadID == "" {
		return errors.New("conversation.UpdateThreadTitle: threadID required")
	}
	res := s.withCtx(ctx).
		Model(&Thread{}).
		Where("id = ? AND deleted_at IS NULL", threadID).
		Updates(map[string]any{
			"title":      title,
			"updated_at": nowUTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("conversation.UpdateThreadTitle: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrThreadNotFound
	}
	return nil
}

// SoftDeleteThread marks the thread as deleted. Events are kept on disk
// — P2 may add a hard-delete worker that cascades, but P0 keeps the data
// so a misclick is recoverable for the lifetime of the DB.
func (s *Store) SoftDeleteThread(ctx context.Context, threadID string) error {
	if threadID == "" {
		return errors.New("conversation.SoftDeleteThread: threadID required")
	}
	now := nowUTC()
	res := s.withCtx(ctx).
		Model(&Thread{}).
		Where("id = ? AND deleted_at IS NULL", threadID).
		Updates(map[string]any{
			"deleted_at": &now,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("conversation.SoftDeleteThread: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrThreadNotFound
	}
	return nil
}
