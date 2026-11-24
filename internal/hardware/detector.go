package hardware

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
)

// HardwareProfile describes the detected machine capabilities and recommended FFmpeg parameters.
type HardwareProfile struct {
	OS                 string              `json:"os"`
	Arch               string              `json:"arch"`
	CPUCores           int                 `json:"cpu_cores"`
	FFmpegAvailable    bool                `json:"ffmpeg_available"`
	FFprobeAvailable   bool                `json:"ffprobe_available"`
	FFmpegVersion      string              `json:"ffmpeg_version"`
	AvailableEncoders  []string            `json:"available_encoders"`
	SelectedEncoder    string              `json:"selected_encoder"`
	EncoderLabel       string              `json:"encoder_label"`
	HasGPU             bool                `json:"has_gpu"`
	RecommendedWorkers int                 `json:"recommended_workers"`
	RecommendedFFmpeg  config.FFmpegConfig `json:"recommended_ffmpeg"`
}

type encoderCandidate struct {
	codec string
	label string
	isGPU bool
}

var hevcCandidates = []encoderCandidate{
	{codec: "hevc_nvenc", label: "NVIDIA NVENC (HEVC)", isGPU: true},
	{codec: "hevc_videotoolbox", label: "Apple VideoToolbox (HEVC)", isGPU: true},
	{codec: "hevc_qsv", label: "Intel QuickSync (HEVC)", isGPU: true},
	{codec: "hevc_amf", label: "AMD AMF (HEVC)", isGPU: true},
	{codec: "hevc_vaapi", label: "VA-API (HEVC)", isGPU: true},
	{codec: "libx265", label: "CPU libx265 (HEVC)", isGPU: false},
}

// ProbeEncoderFunc tests whether a given FFmpeg video encoder actually works on this host.
type ProbeEncoderFunc func(ctx context.Context, codec string) bool

// Detect inspects the host OS, CPU cores, FFmpeg/FFprobe availability, and working hardware encoders.
func Detect(ctx context.Context) *HardwareProfile {
	return detectWithProber(ctx, defaultListEncoders, defaultProbeEncoder)
}

func detectWithProber(
	ctx context.Context,
	listEncodersFn func(context.Context) (map[string]bool, string),
	probeFn ProbeEncoderFunc,
) *HardwareProfile {
	cores := runtime.NumCPU()
	profile := &HardwareProfile{
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		CPUCores:           cores,
		RecommendedWorkers: recommendWorkers(cores),
	}

	if _, err := exec.LookPath("ffmpeg"); err == nil {
		profile.FFmpegAvailable = true
	}
	if _, err := exec.LookPath("ffprobe"); err == nil {
		profile.FFprobeAvailable = true
	}

	listed, version := listEncodersFn(ctx)
	profile.FFmpegVersion = version

	for _, cand := range hevcCandidates {
		if !listed[cand.codec] {
			continue
		}
		if cand.isGPU && !probeFn(ctx, cand.codec) {
			continue
		}
		profile.AvailableEncoders = append(profile.AvailableEncoders, cand.codec)
		if profile.SelectedEncoder == "" {
			profile.SelectedEncoder = cand.codec
			profile.EncoderLabel = cand.label
			profile.HasGPU = cand.isGPU
		}
	}

	if profile.SelectedEncoder == "" {
		profile.SelectedEncoder = "libx265"
		profile.EncoderLabel = "CPU libx265 (HEVC)"
		profile.HasGPU = false
	}

	profile.RecommendedFFmpeg = buildRecommendedFFmpeg(profile.SelectedEncoder, cores)
	return profile
}

func recommendWorkers(cores int) int {
	switch {
	case cores >= 12:
		return 4
	case cores >= 6:
		return 3
	default:
		return 2
	}
}

func buildRecommendedFFmpeg(encoder string, cores int) config.FFmpegConfig {
	cpuPreset := "medium"
	if cores <= 4 {
		cpuPreset = "fast"
	}

	ff := config.FFmpegConfig{
		CPUPreset:       cpuPreset,
		CPUCrf:          22,
		AudioBitrate:    "192k",
		AudioSampleRate: "48000",
		AudioChannels:   "2",
		VolumeBoost:     "2.0",
	}

	switch encoder {
	case "hevc_nvenc":
		ff.GPUCodec = "hevc_nvenc"
		ff.GPUPreset = "p5"
		ff.GPUCq = 24
		ff.GPUPixFmt = "p010le"
	case "hevc_videotoolbox":
		ff.GPUCodec = "hevc_videotoolbox"
		ff.GPUCq = 60 // VideoToolbox -q:v scale (1-100, higher is better)
		ff.GPUPixFmt = "yuv420p10le"
	case "hevc_qsv":
		ff.GPUCodec = "hevc_qsv"
		ff.GPUPreset = "medium"
		ff.GPUCq = 24
		ff.GPUPixFmt = "p010le"
	case "hevc_amf":
		ff.GPUCodec = "hevc_amf"
		ff.GPUCq = 24
		ff.GPUPixFmt = "yuv420p"
	case "hevc_vaapi":
		ff.GPUCodec = "hevc_vaapi"
		ff.GPUCq = 24
		ff.GPUPixFmt = "vaapi"
	}

	return ff
}

// ResolveFFmpegConfig merges the detected hardware profile with user/job config overrides.
func ResolveFFmpegConfig(cfg *config.Config, hw *HardwareProfile) (useGPU bool, ff config.FFmpegConfig) {
	if hw == nil {
		return cfg.UseGPU, cfg.FFmpeg
	}

	ff = hw.RecommendedFFmpeg
	useGPU = cfg.UseGPU
	if cfg.AutoDetectHardware {
		useGPU = cfg.UseGPU && hw.HasGPU
	}

	// Apply explicit user overrides on top of recommended defaults
	u := cfg.FFmpeg
	if !cfg.AutoDetectHardware && u.GPUCodec != "" {
		ff.GPUCodec = u.GPUCodec
	} else if u.GPUCodec != "" && isEncoderAvailable(hw, u.GPUCodec) {
		ff.GPUCodec = u.GPUCodec
	}
	if u.GPUPreset != "" {
		ff.GPUPreset = u.GPUPreset
	}
	if u.GPUCq > 0 {
		ff.GPUCq = u.GPUCq
	}
	if u.GPUPixFmt != "" {
		ff.GPUPixFmt = u.GPUPixFmt
	}
	if len(u.GPUExtraArgs) > 0 {
		ff.GPUExtraArgs = append([]string(nil), u.GPUExtraArgs...)
	}
	if u.CPUPreset != "" {
		ff.CPUPreset = u.CPUPreset
	}
	if u.CPUCrf > 0 {
		ff.CPUCrf = u.CPUCrf
	}
	if u.AudioBitrate != "" {
		ff.AudioBitrate = u.AudioBitrate
	}
	if u.AudioSampleRate != "" {
		ff.AudioSampleRate = u.AudioSampleRate
	}
	if u.AudioChannels != "" {
		ff.AudioChannels = u.AudioChannels
	}
	if u.VolumeBoost != "" {
		ff.VolumeBoost = u.VolumeBoost
	}

	return useGPU, ff
}

func isEncoderAvailable(hw *HardwareProfile, codec string) bool {
	for _, e := range hw.AvailableEncoders {
		if e == codec {
			return true
		}
	}
	return false
}

// ParseEncodersOutput parses the stdout of `ffmpeg -hide_banner -encoders` into a set of encoder names.
func ParseEncodersOutput(output string) map[string]bool {
	encoders := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	pastHeader := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "------") {
			pastHeader = true
			continue
		}
		if !pastHeader || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			encoders[fields[1]] = true
		}
	}
	return encoders
}

func defaultListEncoders(ctx context.Context) (map[string]bool, string) {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var version string
	if verOut, err := exec.CommandContext(tctx, "ffmpeg", "-version").Output(); err == nil {
		if idx := bytes.IndexByte(verOut, '\n'); idx > 0 {
			version = strings.TrimSpace(string(verOut[:idx]))
		}
	}

	out, err := exec.CommandContext(tctx, "ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return map[string]bool{}, version
	}
	return ParseEncodersOutput(string(out)), version
}

func defaultProbeEncoder(ctx context.Context, codec string) bool {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(tctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", "color=black:s=256x256:d=0.1",
		"-frames:v", "1",
		"-c:v", codec,
		"-f", "null",
		"-",
	)
	return cmd.Run() == nil
}
