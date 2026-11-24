package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/textutil"
)

type FFprobeStreamTag struct {
	Language string `json:"language"`
	Title    string `json:"title"`
}

type FFprobeStream struct {
	Index     int              `json:"index"`
	CodecType string           `json:"codec_type"`
	CodecName string           `json:"codec_name"`
	Channels  int              `json:"channels"`
	Tags      FFprobeStreamTag `json:"tags"`
}

type FFprobeOutput struct {
	Streams []FFprobeStream `json:"streams"`
}

func inspectStreams(videoPath string) ([]FFprobeStream, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		videoPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var probe FFprobeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, err
	}
	return probe.Streams, nil
}

func isAITrackForLanguage(title, trackLang, targetLang string) bool {
	t := strings.ToLower(title)
	if !(strings.Contains(t, "kokoro") || strings.Contains(t, "ai dubbed")) {
		return false
	}
	tags := map[string]string{
		"language": trackLang,
		"title":    title,
	}
	return matchLanguage(tags, targetLang)
}

func isAISubTrack(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "kokoro") ||
		strings.Contains(t, "ai dubbed") ||
		strings.Contains(t, "ai synced") ||
		strings.Contains(t, "synced")
}

func ExtractAudioAnchor(videoPath, outputPath string, audioIndex int) error {
	return ExtractAudioAnchorContext(context.Background(), videoPath, outputPath, audioIndex)
}

func ExtractAudioAnchorContext(ctx context.Context, videoPath, outputPath string, audioIndex int) error {
	mapArg := fmt.Sprintf("0:a:%d", audioIndex)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", videoPath,
		"-map", mapArg,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-b:a", "32k",
		outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to extract anchor audio: %s", stderr.String())
	}
	return nil
}

// RemuxOptions controls per-job audio track selection and replacement during remuxing.
type RemuxOptions struct {
	ReplaceAudioIndex  *int // nil or <0 = add new track; >=0 = replace existing relative audio track #N
	OriginalAudioIndex *int // nil = auto-select via cfg.OriginalLanguage; >=0 = explicit source audio track #N
	NoBackgroundAudio  bool // true = TTS voice only (no original background audio mixed in)
}

func RemuxVideo(
	cfg *config.Config,
	videoPath string,
	wavPath string,
	subPath string,
	outTempPath string,
	info *VideoInfo,
	isExternalSub bool,
	lang string,
) error {
	return RemuxVideoWithOptions(context.Background(), cfg, videoPath, wavPath, subPath, outTempPath, info, isExternalSub, lang, RemuxOptions{})
}

func RemuxVideoContext(
	ctx context.Context,
	cfg *config.Config,
	videoPath string,
	wavPath string,
	subPath string,
	outTempPath string,
	info *VideoInfo,
	isExternalSub bool,
	lang string,
) error {
	return RemuxVideoWithOptions(ctx, cfg, videoPath, wavPath, subPath, outTempPath, info, isExternalSub, lang, RemuxOptions{})
}

func RemuxVideoWithOptions(
	ctx context.Context,
	cfg *config.Config,
	videoPath string,
	wavPath string,
	subPath string,
	outTempPath string,
	info *VideoInfo,
	isExternalSub bool,
	lang string,
	opts RemuxOptions,
) error {
	normLang := strings.ToLower(strings.TrimSpace(lang))
	trackLang := "vie"
	subTitle := "Vietnamese (AI Synced)"
	if normLang == "en" || normLang == "eng" || normLang == "english" {
		trackLang = "eng"
		subTitle = "English (AI Synced)"
	}

	streams, _ := inspectStreams(videoPath)

	// =========================================================================
	// PASS 1: Resample & Mix Audio
	// =========================================================================
	volBoost := cfg.FFmpeg.VolumeBoost
	if strings.TrimSpace(volBoost) == "" || volBoost == "0" {
		volBoost = "2.5"
	}

	audioBitrate := cfg.FFmpeg.AudioBitrate
	if strings.TrimSpace(audioBitrate) == "" {
		audioBitrate = "192k"
	}

	targetAudioIndex := info.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)
	if opts.OriginalAudioIndex != nil && *opts.OriginalAudioIndex >= 0 && *opts.OriginalAudioIndex < info.AudioTrackCount {
		targetAudioIndex = *opts.OriginalAudioIndex
	}

	mixFilter := fmt.Sprintf("[0:a:%d]aresample=48000:async=1,asetpts=PTS-STARTPTS,aformat=channel_layouts=stereo,volume=1.0[bg];"+
		"[1:a]aresample=48000:async=1,asetpts=PTS-STARTPTS,pan=stereo|c0=c0|c1=c0,volume=%s[tts];"+
		"[bg][tts]amix=inputs=2:duration=first:dropout_transition=0,asetpts=PTS-STARTPTS[mix_layer]",
		targetAudioIndex, volBoost,
	)

	noBgAudioFilter := fmt.Sprintf(
		"[1:a]aresample=48000:async=1,asetpts=PTS-STARTPTS,pan=stereo|c0=c0|c1=c0,volume=%s[mix_layer]",
		volBoost,
	)

	activeMixFilter := mixFilter
	if info.AudioTrackCount == 0 || opts.NoBackgroundAudio {
		activeMixFilter = noBgAudioFilter
	}

	tempMixedAudio := filepath.Join(filepath.Dir(outTempPath), "temp_mixed_"+filepath.Base(wavPath)+".wav")
	defer os.Remove(tempMixedAudio)

	audioCmdArgs := []string{
		"-y",
		"-i", videoPath,
		"-i", wavPath,
		"-filter_complex", activeMixFilter,
		"-map", "[mix_layer]",
		"-ac", "2",
		"-ar", "48000",
		"-c:a", "pcm_s16le",
		tempMixedAudio,
	}

	cmdAudio := exec.CommandContext(ctx, "ffmpeg", audioCmdArgs...)
	var stderrAudio bytes.Buffer
	cmdAudio.Stderr = &stderrAudio
	if err := cmdAudio.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("audio mixing error (Pass 1): %s", stderrAudio.String())
	}

	// =========================================================================
	// PASS 2: MUX VIDEO, STREAM COPY AUDIO & EMBED SUBTITLE
	// =========================================================================
	hasSubInput := isExternalSub && subPath != "" && textutil.FileExists(subPath)

	var ffmpegArgs []string
	ffmpegArgs = append(ffmpegArgs, "-y", "-fflags", "+genpts")

	if hasSubInput {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio, "-i", subPath)
	} else {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio)
	}

	ffmpegArgs = append(ffmpegArgs, "-avoid_negative_ts", "make_zero")

	// 1. Map Video
	ffmpegArgs = append(ffmpegArgs, "-map", "0:v:0")

	// 2. Map Audio Streams
	aiAsFirstTrack := cfg.AITrackAsFirstTrack ||
		strings.Contains(strings.ToLower(videoPath), "demo") ||
		info.AudioTrackCount == 0

	shouldSkipAudioStream := func(relAudioIdx int, st FFprobeStream) bool {
		if opts.ReplaceAudioIndex != nil {
			if *opts.ReplaceAudioIndex >= 0 {
				return relAudioIdx == *opts.ReplaceAudioIndex
			}
			// Explicitly adding a new track (ReplaceAudioIndex < 0): keep all existing audio tracks
			return false
		}
		// Default legacy behavior: replace prior AI track for same language if any
		return isAITrackForLanguage(st.Tags.Title, st.Tags.Language, lang)
	}

	keptAudioCount := 0
	newAudioIndex := 0

	if aiAsFirstTrack {
		ffmpegArgs = append(ffmpegArgs, "-map", "1:a:0")
		newAudioIndex = 0

		if len(streams) > 0 {
			relIdx := 0
			for _, st := range streams {
				if st.CodecType == "audio" {
					if !shouldSkipAudioStream(relIdx, st) {
						ffmpegArgs = append(ffmpegArgs, "-map", fmt.Sprintf("0:%d", st.Index))
						keptAudioCount++
					}
					relIdx++
				}
			}
		} else if info.AudioTrackCount > 0 {
			ffmpegArgs = append(ffmpegArgs, "-map", "0:a:0")
			keptAudioCount = 1
		}
	} else {
		if len(streams) > 0 {
			relIdx := 0
			for _, st := range streams {
				if st.CodecType == "audio" {
					if !shouldSkipAudioStream(relIdx, st) {
						ffmpegArgs = append(ffmpegArgs, "-map", fmt.Sprintf("0:%d", st.Index))
						keptAudioCount++
					}
					relIdx++
				}
			}
		} else if info.AudioTrackCount > 0 {
			ffmpegArgs = append(ffmpegArgs, "-map", "0:a:0")
			keptAudioCount = 1
		}

		ffmpegArgs = append(ffmpegArgs, "-map", "1:a:0")
		newAudioIndex = keptAudioCount
	}

	// 3. Map Subtitles
	ext := strings.ToLower(filepath.Ext(outTempPath))
	isMP4Container := ext == ".mp4" || ext == ".mov" || ext == ".m4v"
	isAVIContainer := ext == ".avi"

	keptSubCount := 0
	if isAVIContainer {
		fmt.Println("    ⚠️ AVI container format detected: Skipping embedded subtitle stream mapping as AVI does not support subtitle tracks.")
	} else {
		if len(streams) > 0 {
			for _, st := range streams {
				if st.CodecType == "subtitle" {
					if hasSubInput && isAISubTrack(st.Tags.Title) {
						continue
					}
					if isMP4Container && IsBitmapSubtitleCodec(st.CodecName) {
						fmt.Printf("    ⚠️ Skipping bitmap subtitle track #%d (%s) for MP4/MOV container remuxing.\n", st.Index, st.CodecName)
						continue
					}
					ffmpegArgs = append(ffmpegArgs, "-map", fmt.Sprintf("0:%d", st.Index))
					keptSubCount++
				}
			}
		} else {
			ffmpegArgs = append(ffmpegArgs, "-map", "0:s?")
		}

		if hasSubInput {
			ffmpegArgs = append(ffmpegArgs, "-map", "2:s:0")
		}
	}

	// 4. Map Fonts/Attachments
	ffmpegArgs = append(ffmpegArgs, "-map", "0:t?")

	// Set Default Track
	ffmpegArgs = append(ffmpegArgs,
		"-disposition:a", "0",
		fmt.Sprintf("-disposition:a:%d", newAudioIndex), "default",
	)

	// Configure Video Codec
	if cfg.SkipEncode || info.IsHEVC || info.IsAV1 || info.IsWellCompressed {
		ffmpegArgs = append(ffmpegArgs, "-c:v", "copy")
	} else {
		ffmpegArgs = append(ffmpegArgs, BuildVideoEncodeArgs(cfg.UseGPU, cfg.FFmpeg)...)
	}

	// Configure Audio & Subtitle Codec
	ffmpegArgs = append(ffmpegArgs, "-c:a", "copy")
	ffmpegArgs = append(ffmpegArgs,
		fmt.Sprintf("-c:a:%d", newAudioIndex), "aac",
		fmt.Sprintf("-b:a:%d", newAudioIndex), audioBitrate,
	)

	if !isAVIContainer {
		if isMP4Container {
			ffmpegArgs = append(ffmpegArgs, "-c:s", "mov_text")
		} else {
			ffmpegArgs = append(ffmpegArgs, "-c:s", "copy")
			if hasSubInput {
				newSubIndex := keptSubCount
				subCodec := SubtitleCodecForOutput(subPath, outTempPath)
				ffmpegArgs = append(ffmpegArgs, fmt.Sprintf("-c:s:%d", newSubIndex), subCodec)
			}
		}
	}

	// Metadata
	ffmpegArgs = append(ffmpegArgs,
		fmt.Sprintf("-metadata:s:a:%d", newAudioIndex), "title=AI Dubbed (Kokoro AI)",
		fmt.Sprintf("-metadata:s:a:%d", newAudioIndex), "language="+trackLang,
	)

	if hasSubInput {
		newSubIndex := keptSubCount
		ffmpegArgs = append(ffmpegArgs,
			fmt.Sprintf("-metadata:s:s:%d", newSubIndex), "title="+subTitle,
			fmt.Sprintf("-metadata:s:s:%d", newSubIndex), "language="+trackLang,
		)
	}

	ffmpegArgs = append(ffmpegArgs, "-max_interleave_delta", "0")
	ffmpegArgs = append(ffmpegArgs, outTempPath)

	cmdVideo := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)
	var stderrVideo bytes.Buffer
	cmdVideo.Stderr = &stderrVideo

	if err := cmdVideo.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Printf("\n❌ [PASS 2 MUX ERROR] Error details:\n%s\n", stderrVideo.String())
		return fmt.Errorf("video processing error (Pass 2): %v", err)
	}

	return nil
}

func ExtractEmbeddedSubtitle(videoPath, outSrtPath string, subIndex int) error {
	return ExtractEmbeddedSubtitleContext(context.Background(), videoPath, outSrtPath, subIndex)
}

func ExtractEmbeddedSubtitleContext(ctx context.Context, videoPath, outSrtPath string, subIndex int) error {
	mapArg := fmt.Sprintf("0:s:%d", subIndex)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", videoPath, "-vn", "-an", "-dn", "-map", mapArg, outSrtPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("FFmpeg subtitle extraction error: %s", stderr.String())
	}
	return nil
}

// ExtractEmbeddedSubtitleCopyContext extracts an embedded subtitle track with stream copy (-c:s copy)
// to preserve original formatting (e.g. ASS/SSA styles & descriptions) without transcoding.
func ExtractEmbeddedSubtitleCopyContext(ctx context.Context, videoPath, outPath string, subIndex int) error {
	mapArg := fmt.Sprintf("0:s:%d", subIndex)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", videoPath, "-vn", "-an", "-dn", "-map", mapArg, "-c:s", "copy", outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("FFmpeg subtitle copy extraction error: %s", stderr.String())
	}
	return nil
}

// ConvertToSRT converts any subtitle format (.ass, .ssa, .vtt, .sub, .sbv, etc.) to standard .srt using FFmpeg.
func ConvertToSRT(inputSubPath, outputSrtPath string) error {
	return ConvertToSRTContext(context.Background(), inputSubPath, outputSrtPath)
}

func ConvertToSRTContext(ctx context.Context, inputSubPath, outputSrtPath string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", inputSubPath, outputSrtPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to convert subtitle %s to srt: %s", filepath.Base(inputSubPath), stderr.String())
	}
	return nil
}

// SubtitleCodecForOutput chooses the FFmpeg subtitle codec to preserve ASS/SSA styling in MKV
// while falling back to mov_text for MP4/MOV containers.
func SubtitleCodecForOutput(subPath, outContainerPath string) string {
	containerExt := strings.ToLower(filepath.Ext(outContainerPath))
	if containerExt == ".mp4" || containerExt == ".mov" || containerExt == ".m4v" {
		return "mov_text"
	}
	subExt := strings.ToLower(filepath.Ext(subPath))
	if subExt == ".ass" || subExt == ".ssa" {
		return "ass"
	}
	return "subrip"
}

// BuildVideoEncodeArgs constructs FFmpeg video encoding arguments from a resolved FFmpegConfig.
func BuildVideoEncodeArgs(useGPU bool, ff config.FFmpegConfig) []string {
	if !useGPU {
		cpuCrf := ff.CPUCrf
		if cpuCrf <= 0 {
			cpuCrf = 22
		}
		cpuPreset := ff.CPUPreset
		if cpuPreset == "" {
			cpuPreset = "medium"
		}
		return []string{
			"-c:v", "libx265",
			"-crf", strconv.Itoa(cpuCrf),
			"-preset", cpuPreset,
			"-tune", "animation",
			"-pix_fmt", "yuv420p10le",
		}
	}

	gpuCodec := ff.GPUCodec
	if gpuCodec == "" {
		if runtime.GOOS == "darwin" {
			gpuCodec = "hevc_videotoolbox"
		} else {
			gpuCodec = "hevc_nvenc"
		}
	}

	gpuPixFmt := ff.GPUPixFmt
	if gpuPixFmt == "" {
		if strings.Contains(gpuCodec, "nvenc") || strings.Contains(gpuCodec, "qsv") {
			gpuPixFmt = "p010le"
		} else if strings.Contains(gpuCodec, "videotoolbox") {
			gpuPixFmt = "yuv420p10le"
		} else {
			gpuPixFmt = "yuv420p"
		}
	}

	args := []string{"-c:v", gpuCodec}
	if gpuPixFmt != "" && gpuPixFmt != "vaapi" {
		args = append(args, "-pix_fmt", gpuPixFmt)
	}

	gpuCq := ff.GPUCq
	if gpuCq <= 0 {
		if strings.Contains(gpuCodec, "videotoolbox") {
			gpuCq = 60
		} else {
			gpuCq = 24
		}
	}

	switch {
	case strings.Contains(gpuCodec, "nvenc"):
		if ff.GPUPreset != "" {
			args = append(args, "-preset", ff.GPUPreset)
		}
		args = append(args,
			"-rc:v", "vbr",
			"-cq", strconv.Itoa(gpuCq),
			"-b:v", "0",
			"-maxrate", "20M",
			"-bufsize", "40M",
			"-spatial-aq", "1", "-temporal-aq", "1",
		)
	case strings.Contains(gpuCodec, "qsv"):
		if ff.GPUPreset != "" {
			args = append(args, "-preset", ff.GPUPreset)
		}
		args = append(args, "-global_quality", strconv.Itoa(gpuCq))
	case strings.Contains(gpuCodec, "amf"):
		args = append(args, "-rc", "cqp", "-qp_i", strconv.Itoa(gpuCq), "-qp_p", strconv.Itoa(gpuCq))
	default:
		// videotoolbox / vaapi
		args = append(args, "-q:v", strconv.Itoa(gpuCq))
	}

	if len(ff.GPUExtraArgs) > 0 {
		args = append(args, ff.GPUExtraArgs...)
	}

	return args
}
