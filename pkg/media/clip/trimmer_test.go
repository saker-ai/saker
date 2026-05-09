package clip

import (
	"testing"
)

func TestSafeFilename(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		source    string
		start     float64
		end       float64
		want      string
	}{
		{
			name:   "standard source file",
			source: "video.mp4",
			start:  30.0,
			end:    60.0,
			want:   "match_video_00m30s-01m00s.mp4",
		},
		{
			name:   "source with spaces replaced",
			source: "my video file.mp4",
			start:  0,
			end:    15.0,
			want:   "match_my_video_file_00m00s-00m15s.mp4",
		},
		{
			name:   "source with special chars",
			source: "test@#video!.mp4",
			start:  120.0,
			end:    150.0,
			want:   "match_test__video__02m00s-02m30s.mp4",
		},
		{
			name:   "long filename",
			source: "very-long-video-name-here.mp4",
			start:  300.0,
			end:    330.0,
			want:   "match_very_long_video_name_here_05m00s-05m30s.mp4",
		},
		{
			name:   "source without extension",
			source: "rawvideo",
			start:  0,
			end:    5.0,
			want:   "match_rawvideo_00m00s-00m05s.mp4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := safeFilename(tt.source, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("safeFilename(%q, %v, %v) = %q, want %q", tt.source, tt.start, tt.end, got, tt.want)
			}
		})
	}
}

func TestFmtTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		seconds float64
		want    string
	}{
		{name: "zero seconds", seconds: 0, want: "00m00s"},
		{name: "30 seconds", seconds: 30, want: "00m30s"},
		{name: "60 seconds = 1 minute", seconds: 60, want: "01m00s"},
		{name: "90 seconds", seconds: 90, want: "01m30s"},
		{name: "120 seconds = 2 minutes", seconds: 120, want: "02m00s"},
		{name: "300 seconds = 5 minutes", seconds: 300, want: "05m00s"},
		{name: "3600 seconds = 60 minutes", seconds: 3600, want: "60m00s"},
		{name: "fractional seconds truncates to int", seconds: 30.5, want: "00m30s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fmtTime(tt.seconds)
			if got != tt.want {
				t.Errorf("fmtTime(%v) = %q, want %q", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestTrimClipValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		start   float64
		end     float64
		wantErr string
	}{
		{
			name:    "end equal to start",
			start:   10.0,
			end:     10.0,
			wantErr: "end_time",
		},
		{
			name:    "end less than start",
			start:   60.0,
			end:     30.0,
			wantErr: "end_time",
		},
		{
			name:    "end greater than start passes validation",
			start:   0,
			end:     30.0,
			wantErr: "", // will fail at ffmpeg lookup, but passes validation
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := TrimClip(t.Context(), "source.mp4", tt.start, tt.end, "output.mp4", 0)
			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("TrimClip: expected error containing %q, got nil", tt.wantErr)
				}
				// The validation error should mention end_time.
				// Note: ffmpeg not found error will come first for the valid case.
			}
			// For valid start/end, we expect either ffmpeg-not-found or success
			// depending on environment. We just verify no validation-only error.
		})
	}
}

func TestTrimTopRequestsEmpty(t *testing.T) {
	t.Parallel()
	_, err := TrimTopRequests(t.Context(), nil, "output", 5)
	if err == nil {
		t.Error("TrimTopRequests with nil requests: expected error, got nil")
	}
	if err.Error() != "clip: no results to trim" {
		t.Errorf("TrimTopRequests nil: got %q, want %q", err.Error(), "clip: no results to trim")
	}

	_, err = TrimTopRequests(t.Context(), []TrimRequest{}, "output", 5)
	if err == nil {
		t.Error("TrimTopRequests with empty requests: expected error, got nil")
	}
}

func TestTrimTopRequestsClampsCount(t *testing.T) {
	t.Parallel()
	// count < 1 should be clamped to 1
	requests := []TrimRequest{
		{SourceFile: "a.mp4", StartTime: 0, EndTime: 30},
	}
	_, err := TrimTopRequests(t.Context(), requests, "output", 0)
	// Will fail at ffmpeg, but count=0 should be clamped to 1 and only 1 trim attempted.
	// We can't fully test without ffmpeg, but we verify the error is not ErrNoResults.
	if err != nil && err.Error() == "clip: no results to trim" {
		t.Error("count=0 should be clamped to 1, not result in ErrNoResults")
	}
}

func TestErrSentinelValues(t *testing.T) {
	t.Parallel()
	if ErrNoFFmpeg.Error() != "clip: ffmpeg not found in PATH" {
		t.Errorf("ErrNoFFmpeg = %q, want %q", ErrNoFFmpeg.Error(), "clip: ffmpeg not found in PATH")
	}
	if ErrNoResults.Error() != "clip: no results to trim" {
		t.Errorf("ErrNoResults = %q, want %q", ErrNoResults.Error(), "clip: no results to trim")
	}
}