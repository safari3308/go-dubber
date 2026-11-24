package media

import (
	"slices"
	"testing"

	"github.com/safari3308/go-dubber/internal/config"
)

func TestBuildVideoEncodeArgs(t *testing.T) {
	t.Run("NVENC", func(t *testing.T) {
		args := BuildVideoEncodeArgs(true, config.FFmpegConfig{
			GPUCodec:  "hevc_nvenc",
			GPUPreset: "p5",
			GPUCq:     21,
			GPUPixFmt: "p010le",
		})
		if !slices.Contains(args, "hevc_nvenc") || !slices.Contains(args, "p5") || !slices.Contains(args, "21") {
			t.Errorf("unexpected NVENC args: %v", args)
		}
	})

	t.Run("VideoToolbox", func(t *testing.T) {
		args := BuildVideoEncodeArgs(true, config.FFmpegConfig{
			GPUCodec:  "hevc_videotoolbox",
			GPUCq:     60,
			GPUPixFmt: "yuv420p10le",
		})
		if !slices.Contains(args, "hevc_videotoolbox") || !slices.Contains(args, "-q:v") || !slices.Contains(args, "60") {
			t.Errorf("unexpected VideoToolbox args: %v", args)
		}
	})

	t.Run("CPU libx265", func(t *testing.T) {
		args := BuildVideoEncodeArgs(false, config.FFmpegConfig{
			CPUPreset: "fast",
			CPUCrf:    23,
		})
		if !slices.Contains(args, "libx265") || !slices.Contains(args, "fast") || !slices.Contains(args, "23") {
			t.Errorf("unexpected CPU args: %v", args)
		}
	})
}

func TestSubtitleCodecForOutput(t *testing.T) {
	tests := []struct {
		sub  string
		out  string
		want string
	}{
		{"sub.ass", "movie.mkv", "ass"},
		{"sub.ssa", "movie.mkv", "ass"},
		{"sub.srt", "movie.mkv", "subrip"},
		{"sub.ass", "movie.mp4", "mov_text"},
		{"sub.srt", "movie.mov", "mov_text"},
	}
	for _, tt := range tests {
		if got := SubtitleCodecForOutput(tt.sub, tt.out); got != tt.want {
			t.Errorf("SubtitleCodecForOutput(%q, %q) = %q; want %q", tt.sub, tt.out, got, tt.want)
		}
	}
}
