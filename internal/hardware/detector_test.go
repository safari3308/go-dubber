package hardware

import (
	"context"
	"testing"

	"github.com/safari3308/go-dubber/internal/config"
)

func TestParseEncodersOutput(t *testing.T) {
	sample := `Encoders:
 V..... = Video
 ------
 V....D libx265              libx265 H.265 / HEVC (codec hevc)
 V....D hevc_videotoolbox    VideoToolbox H.265 Encoder (codec hevc)
 V....D hevc_nvenc           NVIDIA NVENC hevc encoder (codec hevc)
`
	got := ParseEncodersOutput(sample)
	for _, want := range []string{"libx265", "hevc_videotoolbox", "hevc_nvenc"} {
		if !got[want] {
			t.Errorf("expected encoder %q to be parsed", want)
		}
	}
	if got["V....."] {
		t.Errorf("header line should not be parsed as encoder")
	}
}

func TestDetectWithProber_PriorityAndFallback(t *testing.T) {
	// NVENC listed but probe fails (no NVIDIA GPU); VideoToolbox listed and succeeds.
	listFn := func(ctx context.Context) (map[string]bool, string) {
		return map[string]bool{
			"hevc_nvenc":        true,
			"hevc_videotoolbox": true,
			"libx265":           true,
		}, "ffmpeg version 7.0"
	}
	probeFn := func(ctx context.Context, codec string) bool {
		return codec == "hevc_videotoolbox"
	}

	profile := detectWithProber(context.Background(), listFn, probeFn)
	if !profile.HasGPU {
		t.Fatalf("expected HasGPU=true")
	}
	if profile.SelectedEncoder != "hevc_videotoolbox" {
		t.Errorf("SelectedEncoder = %q; want hevc_videotoolbox", profile.SelectedEncoder)
	}
	if profile.RecommendedFFmpeg.GPUCodec != "hevc_videotoolbox" {
		t.Errorf("RecommendedFFmpeg.GPUCodec = %q; want hevc_videotoolbox", profile.RecommendedFFmpeg.GPUCodec)
	}
	if profile.RecommendedFFmpeg.GPUCq != 60 {
		t.Errorf("RecommendedFFmpeg.GPUCq = %d; want 60 for videotoolbox", profile.RecommendedFFmpeg.GPUCq)
	}
}

func TestDetectWithProber_CPUFallback(t *testing.T) {
	listFn := func(ctx context.Context) (map[string]bool, string) {
		return map[string]bool{
			"hevc_nvenc": true,
			"libx265":    true,
		}, "ffmpeg version 7.0"
	}
	probeFn := func(ctx context.Context, codec string) bool {
		return false // no working GPU
	}

	profile := detectWithProber(context.Background(), listFn, probeFn)
	if profile.HasGPU {
		t.Errorf("expected HasGPU=false when all GPU probes fail")
	}
	if profile.SelectedEncoder != "libx265" {
		t.Errorf("SelectedEncoder = %q; want libx265", profile.SelectedEncoder)
	}
}

func TestResolveFFmpegConfig(t *testing.T) {
	hw := &HardwareProfile{
		HasGPU:            true,
		SelectedEncoder:   "hevc_videotoolbox",
		AvailableEncoders: []string{"hevc_videotoolbox", "libx265"},
		RecommendedFFmpeg: buildRecommendedFFmpeg("hevc_videotoolbox", 8),
	}

	// AutoDetectHardware ignores unavailable user GPUCodec (e.g. hevc_nvenc from Linux config)
	cfg := &config.Config{
		UseGPU:             true,
		AutoDetectHardware: true,
		FFmpeg: config.FFmpegConfig{
			GPUCodec:     "hevc_nvenc",
			AudioBitrate: "256k",
		},
	}

	useGPU, resolved := ResolveFFmpegConfig(cfg, hw)
	if !useGPU {
		t.Errorf("expected useGPU=true")
	}
	if resolved.GPUCodec != "hevc_videotoolbox" {
		t.Errorf("GPUCodec = %q; want hevc_videotoolbox", resolved.GPUCodec)
	}
	if resolved.AudioBitrate != "256k" {
		t.Errorf("AudioBitrate = %q; want user override 256k", resolved.AudioBitrate)
	}
}

func TestDetect_RealMachine(t *testing.T) {
	profile := Detect(context.Background())
	if profile.CPUCores <= 0 {
		t.Errorf("expected CPUCores > 0, got %d", profile.CPUCores)
	}
	if profile.SelectedEncoder == "" {
		t.Errorf("expected non-empty SelectedEncoder")
	}
	t.Logf("Detected machine: %s/%s (%d cores) -> %s (GPU=%v, encoders=%v)",
		profile.OS, profile.Arch, profile.CPUCores, profile.SelectedEncoder, profile.HasGPU, profile.AvailableEncoders)
}
