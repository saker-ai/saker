package describe

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoreAnnotation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		ann          Annotation
		query        string
		wantZero     bool
		wantPositive bool
	}{
		{
			name:     "empty annotation scores zero",
			ann:      Annotation{},
			query:    "anything",
			wantZero: true,
		},
		{
			name: "visual match scores positively",
			ann: Annotation{
				Visual: "A red car driving on a highway",
			},
			query:        "red car",
			wantPositive: true,
		},
		{
			name: "search_tags match has highest weight",
			ann: Annotation{
				SearchTags: []string{"red car", "highway"},
			},
			query:        "red car",
			wantPositive: true,
		},
		{
			name: "entity match has second highest weight",
			ann: Annotation{
				Entity: "red sedan",
			},
			query:        "red sedan",
			wantPositive: true,
		},
		{
			name: "no matching terms scores zero",
			ann: Annotation{
				Visual:     "A sunset over mountains",
				Scene:      "outdoor, evening",
				SearchTags: []string{"sunset", "mountains"},
			},
			query:    "airplane",
			wantZero: true,
		},
		{
			name: "multi-word query matches across tracks",
			ann: Annotation{
				Visual:     "A dog running in park",
				Action:     "running fast",
				SearchTags: []string{"dog", "park", "running"},
			},
			query:        "dog running",
			wantPositive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			queryTerms := strings.Fields(strings.ToLower(tt.query))
			score := scoreAnnotation(&tt.ann, queryTerms)
			if tt.wantZero && score != 0 {
				t.Errorf("scoreAnnotation = %v, want 0", score)
			}
			if tt.wantPositive && score <= 0 {
				t.Errorf("scoreAnnotation = %v, want > 0", score)
			}
		})
	}
}

func TestScoreAnnotationTrackWeights(t *testing.T) {
	t.Parallel()
	// Verify that search_tags (weight 3.0) produces a higher score than
	// visual (weight 1.5) for the same single-word match.
	queryTerms := strings.Fields("testword")

	tagAnn := Annotation{SearchTags: []string{"testword"}}
	tagScore := scoreAnnotation(&tagAnn, queryTerms)

	visualAnn := Annotation{Visual: "testword"}
	visualScore := scoreAnnotation(&visualAnn, queryTerms)

	if tagScore <= visualScore {
		t.Errorf("search_tags score (%v) should be > visual score (%v) for same word", tagScore, visualScore)
	}

	// Verify the exact weight: search_tags=3.0, visual=1.5.
	// For a single occurrence: score = weight * Log1p(1) = weight * ln(2).
	expectedTag := 3.0 * math.Log1p(1)
	expectedVisual := 1.5 * math.Log1p(1)
	if math.Abs(tagScore-expectedTag) > 0.01 {
		t.Errorf("tag score = %v, want ~%v", tagScore, expectedTag)
	}
	if math.Abs(visualScore-expectedVisual) > 0.01 {
		t.Errorf("visual score = %v, want ~%v", visualScore, expectedVisual)
	}
}

func TestScoreAnnotationMultipleOccurrences(t *testing.T) {
	t.Parallel()
	// Two occurrences of a word should use Log1p(count) not count*Log1p(1).
	queryTerms := strings.Fields("testword")

	once := Annotation{Visual: "testword"}
	twice := Annotation{Visual: "testword testword testword"}

	onceScore := scoreAnnotation(&once, queryTerms)
	twiceScore := scoreAnnotation(&twice, queryTerms)

	expectedOnce := 1.5 * math.Log1p(1)
	expectedTwice := 1.5 * math.Log1p(3)
	if math.Abs(onceScore-expectedOnce) > 0.01 {
		t.Errorf("once score = %v, want ~%v", onceScore, expectedOnce)
	}
	if math.Abs(twiceScore-expectedTwice) > 0.01 {
		t.Errorf("twice score = %v, want ~%v", twiceScore, expectedTwice)
	}
}

func TestMultiStoreSearch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	store1 := NewStore(filepath.Join(dir, "video1.jsonl"))
	_ = store1.Append(&Annotation{
		Segment:    Segment{SourceFile: "v1.mp4", StartTime: 0, EndTime: 30},
		Visual:     "A red car",
		SearchTags: []string{"red car"},
	})

	store2 := NewStore(filepath.Join(dir, "video2.jsonl"))
	_ = store2.Append(&Annotation{
		Segment:    Segment{SourceFile: "v2.mp4", StartTime: 0, EndTime: 30},
		Visual:     "A blue bicycle",
		SearchTags: []string{"blue bicycle"},
	})

	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644)

	ms := NewMultiStore(dir)
	results, err := ms.Search("car", 5)
	if err != nil {
		t.Fatalf("MultiStore.Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Segment.SourceFile != "v1.mp4" {
		t.Errorf("expected v1.mp4, got %s", results[0].Segment.SourceFile)
	}
}

func TestMultiStoreSearchNonexistentDir(t *testing.T) {
	t.Parallel()
	ms := NewMultiStore("/nonexistent/path/12345")
	results, err := ms.Search("anything", 5)
	if err != nil {
		t.Fatalf("unexpected error for nonexistent dir: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent dir, got %d", len(results))
	}
}

func TestMultiStoreSearchEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ms := NewMultiStore(dir)
	results, err := ms.Search("anything", 5)
	if err != nil {
		t.Fatalf("unexpected error for empty dir: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty dir, got %d", len(results))
	}
}

func TestMultiStoreSearchMaxResults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	store := NewStore(filepath.Join(dir, "data.jsonl"))
	for i := 0; i < 10; i++ {
		_ = store.Append(&Annotation{
			Segment:    Segment{SourceFile: fmt.Sprintf("v%d.mp4", i), StartTime: float64(i * 30)},
			Visual:     fmt.Sprintf("scene number %d", i),
			SearchTags: []string{fmt.Sprintf("scene%d", i)},
		})
	}

	ms := NewMultiStore(dir)
	results, err := ms.Search("scene", 3)
	if err != nil {
		t.Fatalf("MultiStore.Search: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results with maxResults=3, got %d", len(results))
	}
}

func TestAnnotateSegmentNilModel(t *testing.T) {
	t.Parallel()
	a := &Annotator{Model: nil}
	_, err := a.AnnotateSegment(context.Background(), Segment{SourceFile: "test.mp4"}, []string{"frame1.jpg"})
	if err == nil {
		t.Error("expected error for nil model")
	}
	if !strings.Contains(err.Error(), "model not configured") {
		t.Errorf("error = %q, want it to contain 'model not configured'", err.Error())
	}
}

func TestAnnotateSegmentNoFrames(t *testing.T) {
	t.Parallel()
	a := &Annotator{Model: nil}
	_, err := a.AnnotateSegment(context.Background(), Segment{}, nil)
	if err == nil {
		t.Error("expected error for nil model with nil frames")
	}
}

func TestAnnotateSegmentEmptyFrames(t *testing.T) {
	t.Parallel()
	a := &Annotator{Model: nil}
	_, err := a.AnnotateSegment(context.Background(), Segment{}, []string{})
	if err == nil {
		t.Error("expected error for nil model with empty frames")
	}
}

func TestNewAnnotator(t *testing.T) {
	t.Parallel()
	a := NewAnnotator(nil)
	if a == nil {
		t.Error("expected non-nil Annotator")
	}
	if a.Model != nil {
		t.Error("expected nil Model in Annotator constructed with nil")
	}
}

func TestStoreAppendCreatesDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	store := NewStore(filepath.Join(dir, "annotations.jsonl"))

	ann := &Annotation{
		Segment:    Segment{SourceFile: "test.mp4"},
		Visual:     "a scene",
		SearchTags: []string{"scene"},
	}

	if err := store.Append(ann); err != nil {
		t.Fatalf("Append should create directory: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("directory was not created by Append")
	}
}