package conversation

import (
	"context"
	"errors"
	"fmt"
)

// Search runs an FTS5 query (SQLite-only in P1) over the messages
// projection. Postgres tsvector lands later as part of the multi-driver
// work; on Postgres this returns a clear "FTS not available" error so
// callers can degrade to LIKE-based search rather than crash.
//
// The query is passed verbatim to FTS5 MATCH, so callers can use FTS5
// syntax (NEAR, OR, prefix*, "phrase queries"). To search for a literal
// FTS5 special character (`-` is the NOT operator, `:` is column scope,
// etc.), callers must wrap the term in double quotes — e.g. searching
// for `"forty-two"` matches the literal hyphenated string.
//
// projectID is enforced via JOIN on messages so cross-tenant leakage is
// impossible at the SQL layer (defense in depth on top of application
// authz). ThreadID, when set, narrows further.
//
// Hits are ranked by negated bm25 (so higher score = better match,
// matching caller intuition where DESC sort puts the best hit first).
// Snippets use FTS5's snippet() with a 16-token excerpt and no markup —
// callers wanting richer highlighting should fetch the message content
// and apply their own.
func (s *Store) Search(ctx context.Context, projectID, query string, opts SearchOpts) ([]SearchHit, error) {
	if projectID == "" {
		return nil, errors.New("conversation.Search: projectID required")
	}
	if query == "" {
		return nil, errors.New("conversation.Search: query required")
	}
	if s.driver != "sqlite" {
		return nil, fmt.Errorf("conversation.Search: FTS not available on driver %q", s.driver)
	}

	limit := clampLimit(opts.Limit)

	sql := `SELECT m.thread_id, m.id, m.pos, m.role,
       snippet(messages_fts, 0, '', '', '...', 16) AS snippet,
       -bm25(messages_fts) AS score
FROM messages_fts
JOIN messages m ON m.id = messages_fts.rowid
WHERE messages_fts MATCH ?
  AND m.project_id = ?`
	args := []any{query, projectID}
	if opts.ThreadID != "" {
		sql += `
  AND m.thread_id = ?`
		args = append(args, opts.ThreadID)
	}
	sql += `
ORDER BY score DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.withCtx(ctx).Raw(sql, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("conversation.Search: query: %w", err)
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ThreadID, &h.MessageID, &h.Pos, &h.Role, &h.Snippet, &h.Score); err != nil {
			return nil, fmt.Errorf("conversation.Search: scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation.Search: rows: %w", err)
	}
	return out, nil
}
