package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrBlobNotFound is returned by GetBlob when the digest doesn't match
// any stored blob. Sentinel; check with errors.Is.
var ErrBlobNotFound = errors.New("conversation: blob not found")

// ErrBlobMissingForRef is returned by AppendEvent when one of the
// supplied BlobRefs digests doesn't match a stored blob. The whole tx
// rolls back — no partial event/refcount writes. Sentinel.
var ErrBlobMissingForRef = errors.New("conversation: blob referenced by event does not exist")

// PutBlob stores data content-addressed by its sha256 digest and
// returns the lowercase-hex digest. Storing the same bytes a second
// time is a no-op (idempotent dedup); ref_count is NOT incremented by
// PutBlob — that happens inside AppendEvent's tx when an event lists
// the digest in BlobRefs.
//
// Empty data is rejected: an empty blob would dedupe to a single
// "magic" digest that every caller would race to insert, and the use
// case for storing zero bytes is nil. Reject at the API boundary.
//
// On conflict (digest already exists) the existing row is preserved
// untouched — the bytes are by definition identical (sha256 inverse
// would be a cryptographic break) so re-writing Content / SizeBytes
// would be wasted I/O.
func (s *Store) PutBlob(ctx context.Context, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("conversation.PutBlob: data required")
	}

	digest := sha256Hex(data)
	now := nowUTC()
	row := &Blob{
		SHA256:    digest,
		SizeBytes: int64(len(data)),
		Content:   data,
		RefCount:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// DoNothing on conflict: the existing row's bytes are by hash
	// identity equal to the new bytes, and we don't want to disturb
	// CreatedAt / RefCount / UpdatedAt on the existing row.
	err := s.withCtx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "sha256"}},
		DoNothing: true,
	}).Create(row).Error
	if err != nil {
		return "", fmt.Errorf("conversation.PutBlob: %w", err)
	}
	return digest, nil
}

// GetBlob returns a blob's bytes by digest. ErrBlobNotFound (sentinel)
// when the digest is unknown.
func (s *Store) GetBlob(ctx context.Context, digest string) ([]byte, error) {
	if digest == "" {
		return nil, errors.New("conversation.GetBlob: digest required")
	}
	var b Blob
	err := s.withCtx(ctx).Where("sha256 = ?", digest).Take(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetBlob: %w", err)
	}
	return b.Content, nil
}

// GetBlobMeta returns metadata for a blob WITHOUT loading Content.
// Useful for size checks, ref_count audits, GC dry-runs.
func (s *Store) GetBlobMeta(ctx context.Context, digest string) (*Blob, error) {
	if digest == "" {
		return nil, errors.New("conversation.GetBlobMeta: digest required")
	}
	var b Blob
	err := s.withCtx(ctx).
		Select("sha256", "size_bytes", "ref_count", "created_at", "updated_at").
		Where("sha256 = ?", digest).
		Take(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetBlobMeta: %w", err)
	}
	return &b, nil
}

// GCBlobs deletes blobs that have ref_count = 0 and have aged past
// olderThan. Returns the number of rows deleted.
//
// olderThan is the safety window for the put-before-event race: a
// caller that PutBlob's a chunk and is about to AppendEvent referencing
// it has a brief moment where the blob is unreferenced. Without the age
// guard, a GC scan during that window would reclaim the blob out from
// under the in-flight event. One hour is a reasonable production
// default; tests pass 0 to bypass the guard for deterministic
// assertions.
//
// The (ref_count, created_at) composite index on Blob makes the WHERE
// clause an O(matching rows) scan — no full-table walk even when most
// blobs are live.
func (s *Store) GCBlobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan < 0 {
		return 0, errors.New("conversation.GCBlobs: olderThan must be non-negative")
	}
	cutoff := nowUTC().Add(-olderThan)
	res := s.withCtx(ctx).
		Where("ref_count = 0 AND created_at < ?", cutoff).
		Delete(&Blob{})
	if res.Error != nil {
		return 0, fmt.Errorf("conversation.GCBlobs: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// incrementBlobRefsTx atomically bumps ref_count for each digest in
// refs by 1 within an open transaction. Used by AppendEvent so the
// event log and blob ref counts can never diverge after a crash.
//
// Strict mode: if any digest in refs doesn't match a stored blob, the
// function returns ErrBlobMissingForRef and the caller (AppendEvent)
// rolls back the whole tx. This makes "AppendEvent succeeded but my
// blob is gone" structurally impossible.
//
// Duplicate digests in refs each count as a +1 (caller responsibility
// to dedupe if they want). For typical event payloads, refs is short
// (≤ 10 entries) so the per-row UPDATE loop is cheap; large fan-outs
// could move to a single `WHERE sha256 IN (?)` UPDATE with a count
// reconciliation step — keep it simple until measurements demand it.
func incrementBlobRefsTx(tx *gorm.DB, refs []string, now time.Time) error {
	for _, ref := range refs {
		if ref == "" {
			return errors.New("incrementBlobRefs: empty digest in refs")
		}
		res := tx.Model(&Blob{}).
			Where("sha256 = ?", ref).
			Updates(map[string]any{
				"ref_count":  gorm.Expr("ref_count + 1"),
				"updated_at": now,
			})
		if res.Error != nil {
			return fmt.Errorf("increment ref_count for %s: %w", ref, res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("%w: %s", ErrBlobMissingForRef, ref)
		}
	}
	return nil
}

// sha256Hex computes the lowercase hex sha256 of data. Defined here to
// keep the digest format consistent across PutBlob (which assigns it)
// and any test helpers that want to compute an expected digest.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
