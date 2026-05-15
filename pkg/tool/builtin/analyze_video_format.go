package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/media/chunk"
	"github.com/saker-ai/saker/pkg/media/describe"
)

// analyze_video_format.go owns report rendering, on-disk persistence, and
// post-processing helpers (consistency checks). The pipeline that produces
// the inputs lives in analyze_video_pipeline.go.

// buildDeepReport generates a structured markdown report and saves it alongside the video.
func (t *AnalyzeVideoTool) buildDeepReport(videoPath, task string, segments []chunk.Segment, annotations []*describe.Annotation, transcripts []deepTranscript) (string, string) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Video Deep Analysis: %s\n\n", filepath.Base(videoPath)))
	sb.WriteString(fmt.Sprintf("- **Date**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **Source**: %s\n", videoPath))
	sb.WriteString(fmt.Sprintf("- **Segments**: %d\n", len(segments)))
	if len(segments) > 0 {
		totalDur := segments[len(segments)-1].EndTime
		minutes := int(totalDur) / 60
		secs := int(totalDur) % 60
		sb.WriteString(fmt.Sprintf("- **Duration**: %d:%02d\n", minutes, secs))
	}
	if len(transcripts) > 0 {
		sb.WriteString("- **Audio transcription**: available\n")
	} else {
		sb.WriteString("- **Audio transcription**: unavailable\n")
	}
	if task != "" {
		sb.WriteString(fmt.Sprintf("- **Task**: %s\n", task))
	}

	sb.WriteString("\n## Timeline\n\n")
	for i, seg := range segments {
		startMin, startSec := int(seg.StartTime)/60, int(seg.StartTime)%60
		endMin, endSec := int(seg.EndTime)/60, int(seg.EndTime)%60
		fmt.Fprintf(&sb, "### [%02d:%02d - %02d:%02d] Segment %d\n\n", startMin, startSec, endMin, endSec, i+1)

		if i < len(annotations) && annotations[i] != nil {
			ann := annotations[i]
			// Fallback: if Visual looks like a raw JSON blob (all other fields empty),
			// try to re-parse it into the proper fields.
			if ann.Visual != "" && ann.Action == "" && ann.Entity == "" && ann.Scene == "" && strings.HasPrefix(strings.TrimSpace(ann.Visual), "{") {
				var parsed describe.Annotation
				if err := json.Unmarshal([]byte(ann.Visual), &parsed); err == nil {
					parsed.Segment = ann.Segment
					ann = &parsed
					annotations[i] = ann
				}
			}
			if ann.Visual != "" {
				fmt.Fprintf(&sb, "**Visual**: %s\n\n", ann.Visual)
			}
			if ann.Action != "" {
				fmt.Fprintf(&sb, "**Action**: %s\n\n", ann.Action)
			}
			if ann.Entity != "" {
				fmt.Fprintf(&sb, "**Entities**: %s\n\n", ann.Entity)
			}
			if ann.Scene != "" {
				fmt.Fprintf(&sb, "**Scene**: %s\n\n", ann.Scene)
			}
			if ann.Text != "" {
				fmt.Fprintf(&sb, "**Text**: %s\n\n", ann.Text)
			}
			if ann.Audio != "" {
				fmt.Fprintf(&sb, "**Audio (inferred)**: %s\n\n", ann.Audio)
			}
			if len(ann.SearchTags) > 0 {
				fmt.Fprintf(&sb, "**Tags**: %s\n\n", strings.Join(ann.SearchTags, ", "))
			}
		} else {
			sb.WriteString("*Annotation unavailable*\n\n")
		}

		// Attach any audio transcripts that overlap this segment.
		for _, tr := range transcripts {
			if tr.StartTime < seg.EndTime && tr.EndTime > seg.StartTime {
				trMin, trSec := int(tr.StartTime)/60, int(tr.StartTime)%60
				fmt.Fprintf(&sb, "**Audio [%02d:%02d]**: %s\n\n", trMin, trSec, tr.Text)
			}
		}
	}

	// Full transcript section.
	if len(transcripts) > 0 {
		sb.WriteString("## Full Audio Transcript\n\n")
		for _, tr := range transcripts {
			trMin, trSec := int(tr.StartTime)/60, int(tr.StartTime)%60
			fmt.Fprintf(&sb, "[%02d:%02d] %s\n\n", trMin, trSec, tr.Text)
		}
	}

	// Collect all search tags.
	var allTags []string
	seen := map[string]bool{}
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		for _, tag := range ann.SearchTags {
			lower := strings.ToLower(tag)
			if !seen[lower] {
				seen[lower] = true
				allTags = append(allTags, tag)
			}
		}
	}
	if len(allTags) > 0 {
		sb.WriteString("## Search Tags\n\n")
		sb.WriteString(strings.Join(allTags, ", "))
		sb.WriteString("\n")
	}

	// Consistency check: detect conflicting OCR text across segments.
	if notes := detectConsistencyIssues(annotations); notes != "" {
		sb.WriteString("\n## Consistency Notes\n\n")
		sb.WriteString(notes)
	}

	content := sb.String()

	// Save report alongside video.
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	ts := time.Now().Format("20060102-150405")
	reportPath := filepath.Join(dir, fmt.Sprintf("%s_deep_analysis_%s.md", base, ts))

	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		slog.Warn("deep: failed to save report", "error", err)
		return "", content
	}
	return reportPath, content
}

// resolveStoreDir computes the session-aware base directory for JSONL and vector storage.
// Layout: {StoreDir}/{sessionID}/ when session ID is available, otherwise {StoreDir}/.
func (t *AnalyzeVideoTool) resolveStoreDir(ctx context.Context, videoPath string) string {
	base := t.StoreDir
	if base == "" {
		base = filepath.Join(filepath.Dir(videoPath), ".saker", "media")
	}
	if sid := bashSessionID(ctx); sid != "" {
		return filepath.Join(base, sanitizePathComponent(sid))
	}
	return base
}

// persistAnnotations saves annotations to a per-video JSONL store file.
// Each video gets its own file: {storeDir}/{videoBaseName}.jsonl
func (t *AnalyzeVideoTool) persistAnnotations(videoPath string, annotations []*describe.Annotation, storeDir string) string {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		slog.Warn("deep: failed to create store dir", "error", err)
		return ""
	}
	videoBase := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	storePath := filepath.Join(storeDir, videoBase+".jsonl")

	store := describe.NewStore(storePath)
	persisted := 0
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		if err := store.Append(ann); err != nil {
			slog.Warn("deep: failed to append annotation", "error", err)
			continue
		}
		persisted++
	}
	if persisted == 0 {
		return ""
	}
	return storePath
}

// yearPattern matches 4-digit years (1900-2099) for consistency checking.
var yearPattern = regexp.MustCompile(`\b(19|20)\d{2}\b`)

// detectConsistencyIssues scans annotations for conflicting OCR text across segments.
// Returns a markdown string with warnings, or empty if no issues found.
func detectConsistencyIssues(annotations []*describe.Annotation) string {
	// Track which years appear and in which segments.
	yearSegments := map[string][]int{}
	for i, ann := range annotations {
		if ann == nil || ann.Text == "" {
			continue
		}
		years := yearPattern.FindAllString(ann.Text, -1)
		seen := map[string]bool{}
		for _, y := range years {
			if !seen[y] {
				seen[y] = true
				yearSegments[y] = append(yearSegments[y], i+1)
			}
		}
	}

	// If multiple distinct years found, flag as inconsistency.
	var warnings []string
	if len(yearSegments) > 1 {
		var parts []string
		for year, segs := range yearSegments {
			segStrs := make([]string, len(segs))
			for i, s := range segs {
				segStrs[i] = fmt.Sprintf("%d", s)
			}
			parts = append(parts, fmt.Sprintf("'%s' in segment(s) %s", year, strings.Join(segStrs, ", ")))
		}
		warnings = append(warnings, fmt.Sprintf("- Year inconsistency detected: %s. This may indicate OCR recognition variance across frames.", strings.Join(parts, "; ")))
	}

	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n") + "\n"
}
