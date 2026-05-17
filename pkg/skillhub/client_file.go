package skillhub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// GetFile fetches a single file from a skill version without downloading
// the full zip archive. The server endpoint is
// GET /api/v1/skills/:slug/file?version=&path=.
// When path is empty it defaults to "SKILL.md" on the server side.
func (c *Client) GetFile(ctx context.Context, slug, version, path string) ([]byte, error) {
	q := url.Values{}
	if version != "" {
		q.Set("version", version)
	}
	if path != "" {
		q.Set("path", path)
	}
	endpoint := "/api/v1/skills/" + url.PathEscape(slug) + "/file"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skillhub get-file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return nil, &APIError{Status: resp.StatusCode, Body: string(buf)}
	}

	const maxFileSize = 512 << 10 // 512KB per file
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize))
	if err != nil {
		return nil, fmt.Errorf("skillhub get-file read: %w", err)
	}
	return data, nil
}

// ListAllSkills pages through the full skill catalog and returns every
// visible Skill. It stops after maxPages pages (0 = unlimited) to guard
// against runaway pagination.
func (c *Client) ListAllSkills(ctx context.Context, maxPages int) ([]Skill, error) {
	var all []Skill
	cursor := ""
	for page := 0; maxPages == 0 || page < maxPages; page++ {
		res, err := c.List(ctx, "", "", cursor, 100)
		if err != nil {
			return all, err
		}
		all = append(all, res.Data...)
		if res.NextCursor == "" || len(res.Data) == 0 {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}
