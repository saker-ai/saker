package conversation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OpenTurn creates a turns row and returns the generated turn ID. The
// thread must exist and not be soft-deleted.
func (s *Store) OpenTurn(ctx context.Context, threadID, parentTurnID string) (string, error) {
	if threadID == "" {
		return "", errors.New("conversation.OpenTurn: threadID required")
	}
	if _, err := s.GetThread(ctx, threadID); err != nil {
		return "", err
	}
	_ = parentTurnID // reserved for future tree-style lineage
	id := uuid.New().String()
	now := nowUTC()
	turn := &Turn{
		ID:        id,
		ThreadID:  threadID,
		Status:    string(TurnStatusOpen),
		StartedAt: now,
	}
	if err := s.withCtx(ctx).Create(turn).Error; err != nil {
		return "", fmt.Errorf("conversation.OpenTurn: %w", err)
	}
	return id, nil
}

// CloseTurn marks a turn terminal and records FinishedAt. The status
// must be one of the recognized terminal values.
func (s *Store) CloseTurn(ctx context.Context, turnID string, status TurnStatus) error {
	if turnID == "" {
		return errors.New("conversation.CloseTurn: turnID required")
	}
	switch status {
	case TurnStatusCompleted, TurnStatusCancelled, TurnStatusFailed:
	default:
		return errors.New("conversation.CloseTurn: invalid terminal status")
	}
	now := nowUTC()
	res := s.withCtx(ctx).
		Model(&Turn{}).
		Where("id = ?", turnID).
		Updates(map[string]any{
			"status":      string(status),
			"finished_at": &now,
		})
	if res.Error != nil {
		return fmt.Errorf("conversation.CloseTurn: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("conversation.CloseTurn: turn %q not found", turnID)
	}
	return nil
}

// GetTurn retrieves a turn by ID. Returns gorm.ErrRecordNotFound when
// the row is absent.
func (s *Store) GetTurn(ctx context.Context, turnID string) (*Turn, error) {
	if turnID == "" {
		return nil, errors.New("conversation.GetTurn: turnID required")
	}
	var t Turn
	err := s.withCtx(ctx).Where("id = ?", turnID).Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("conversation.GetTurn: turn %q not found", turnID)
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetTurn: %w", err)
	}
	return &t, nil
}
