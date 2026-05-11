// service_storage.go: storage helpers (slug uniqueness, key prefix munging).
//
// At present only uniqueSlug lives here — it is the storage-layer guard that
// keeps Project.Slug collision-free in the database. Future object/file
// persistence helpers (key prefix management, store factory selection) belong
// alongside it.
package project

import (
	"context"

	"github.com/google/uuid"
)

// uniqueSlug returns the base slug if available, otherwise appends a short
// suffix until the DB no longer reports a collision. This is a friendlier
// first attempt than relying solely on the DB unique constraint.
func (s *Store) uniqueSlug(ctx context.Context, base string) string {
	candidate := base
	for i := 0; i < 8; i++ {
		var count int64
		err := s.db.WithContext(ctx).
			Model(&Project{}).
			Where("slug = ?", candidate).
			Count(&count).Error
		if err != nil {
			return candidate + "-" + uuid.NewString()[:8]
		}
		if count == 0 {
			return candidate
		}
		candidate = base + "-" + uuid.NewString()[:8]
	}
	return candidate
}
