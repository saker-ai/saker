package aigo

import (
	"testing"

	sdk "github.com/godeps/aigo"
)

func TestBuildVideoTaskSupportsMultipleReferenceImages(t *testing.T) {
	params := map[string]interface{}{
		"prompt": "a sunset drive",
		"reference_images": []interface{}{
			"https://example.com/img-1.jpg",
			"https://example.com/img-2.jpg",
		},
		"reference_video": "https://example.com/input.mp4",
	}

	task := buildTask("generate_video", params)

	if len(task.References) != 3 {
		t.Fatalf("expected 3 references, got %d (%v)", len(task.References), task.References)
	}

	if task.References[0] != (sdk.ReferenceAsset{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img-1.jpg"}) {
		t.Fatalf("unexpected first image reference: %+v", task.References[0])
	}

	if task.References[1] != (sdk.ReferenceAsset{Type: sdk.ReferenceTypeImage, URL: "https://example.com/img-2.jpg"}) {
		t.Fatalf("unexpected second image reference: %+v", task.References[1])
	}

	if task.References[2] != (sdk.ReferenceAsset{Type: sdk.ReferenceTypeVideo, URL: "https://example.com/input.mp4"}) {
		t.Fatalf("unexpected video reference: %+v", task.References[2])
	}
}
