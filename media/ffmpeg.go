package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/safari3308/go-dubber/config"
	"github.com/safari3308/go-dubber/utils"
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

func isAITrack(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "kokoro") || strings.Contains(t, "ai dubbed") || strings.Contains(t, "dubbed")
}

func isAISubTrack(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "kokoro") || 
		strings.Contains(t, "ai dubbed") || 
		strings.Contains(t, "ai synced") || 
		strings.Contains(t, "synced")
}

func ExtractAudioAnchor(videoPath, outputPath string, audioIndex int) error {
	mapArg := fmt.Sprintf("0:a:%d", audioIndex)

	cmd := exec.Command("ffmpeg",
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
		return fmt.Errorf("failed to extract anchor audio: %s", stderr.String())
	}
	return nil
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
	normLang := strings.ToLower(strings.TrimSpace(lang))
	trackLang := "vie"
	// 🌟 Name subtitles to avoid conflicts with original subtitles
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

	// 🌟 1. Identify original audio track to use as background audio
	targetAudioIndex := info.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)

	// 🌟 2. Replace [0:a:0] with [0:a:%d] using targetAudioIndex
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
	if info.AudioTrackCount == 0 {
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

	cmdAudio := exec.Command("ffmpeg", audioCmdArgs...)
	var stderrAudio bytes.Buffer
	cmdAudio.Stderr = &stderrAudio
	if err := cmdAudio.Run(); err != nil {
		return fmt.Errorf("audio mixing error (Pass 1): %s", stderrAudio.String())
	}

	// =========================================================================
	// PASS 2: MUX VIDEO, STREAM COPY AUDIO & EMBED SUBTITLE
	// =========================================================================
	hasSubInput := subPath != "" && utils.FileExists(subPath)

	var ffmpegArgs []string
	
	// Initialize command with -y and -fflags +genpts to normalize original timestamps
	ffmpegArgs = append(ffmpegArgs, "-y", "-fflags", "+genpts")

	if hasSubInput {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio, "-i", subPath)
	} else {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio)
	}

	// 🌟 FIX 2: Prevent FFmpeg from ignoring negative timestamps if video was previously cut
	ffmpegArgs = append(ffmpegArgs, "-avoid_negative_ts", "make_zero")

	// 1. Map Video
	ffmpegArgs = append(ffmpegArgs, "-map", "0:v:0")

	// 2. Map Audio Streams
	// Check if AI track should be placed as Track #0 (for demo/screen recording videos or when configured)
	aiAsFirstTrack := cfg.AITrackAsFirstTrack ||
		strings.Contains(strings.ToLower(videoPath), "demo") ||
		info.AudioTrackCount == 0

	keptAudioCount := 0
	newAudioIndex := 0

	if aiAsFirstTrack {
		// Place NEW AI track as Track #0 (e.g. for demo / screen recording videos)
		ffmpegArgs = append(ffmpegArgs, "-map", "1:a:0")
		newAudioIndex = 0

		if len(streams) > 0 {
			for _, st := range streams {
				if st.CodecType == "audio" {
					if !isAITrack(st.Tags.Title) {
						ffmpegArgs = append(ffmpegArgs, "-map", fmt.Sprintf("0:%d", st.Index))
						keptAudioCount++
					}
				}
			}
		} else if info.AudioTrackCount > 0 {
			ffmpegArgs = append(ffmpegArgs, "-map", "0:a:0")
			keptAudioCount = 1
		}
	} else {
		// Normal AI Dubbing: Map original audio streams first, AI track as secondary default track
		if len(streams) > 0 {
			for _, st := range streams {
				if st.CodecType == "audio" {
					if !isAITrack(st.Tags.Title) {
						ffmpegArgs = append(ffmpegArgs, "-map", fmt.Sprintf("0:%d", st.Index))
						keptAudioCount++
					}
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

	// 🌟 Set Default Track
	ffmpegArgs = append(ffmpegArgs,
		"-disposition:a", "0",
		fmt.Sprintf("-disposition:a:%d", newAudioIndex), "default",
	)

	// Configure Video Codec
	if cfg.SkipEncode || info.IsHEVC || info.IsAV1 || info.IsWellCompressed {
		ffmpegArgs = append(ffmpegArgs, "-c:v", "copy")
	} else if cfg.UseGPU {
		gpuCodec := cfg.FFmpeg.GPUCodec
		if gpuCodec == "" {
			if runtime.GOOS == "darwin" {
				gpuCodec = "hevc_videotoolbox"
			} else {
				gpuCodec = "hevc_nvenc"
			}
		}

		gpuPixFmt := cfg.FFmpeg.GPUPixFmt
		if gpuPixFmt == "" {
			if strings.Contains(gpuCodec, "nvenc") {
				gpuPixFmt = "p010le"
			} else {
				gpuPixFmt = "yuv420p10le"
			}
		}
		ffmpegArgs = append(ffmpegArgs, "-c:v", gpuCodec)
		if gpuPixFmt != "" {
			ffmpegArgs = append(ffmpegArgs, "-pix_fmt", gpuPixFmt)
		}
		gpuCq := cfg.FFmpeg.GPUCq
		if gpuCq <= 0 {
			gpuCq = 26
		}
		
		if strings.Contains(gpuCodec, "nvenc") {
			ffmpegArgs = append(ffmpegArgs,
				"-rc:v", "vbr",
				"-cq", strconv.Itoa(gpuCq),
				"-b:v", "0",
				"-maxrate", "20M",
				"-bufsize", "40M",
				"-spatial-aq", "1", "-temporal-aq", "1",
			)
		} else {
			ffmpegArgs = append(ffmpegArgs, "-q:v", strconv.Itoa(gpuCq))
		}
	} else {
		cpuCrf := cfg.FFmpeg.CPUCrf
		if cpuCrf <= 0 {
			cpuCrf = 22
		}
		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx265",
			"-crf", strconv.Itoa(cpuCrf),
			"-preset", "medium",
			"-tune", "animation",
			"-pix_fmt", "yuv420p10le",
		)
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
				ffmpegArgs = append(ffmpegArgs, fmt.Sprintf("-c:s:%d", newSubIndex), "subrip")
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

	// 🌟 FIX 1: Prevent Muxer Interleave Delta Error - Force FFmpeg to use infinite RAM buffer when mapping multiple source files
	ffmpegArgs = append(ffmpegArgs, "-max_interleave_delta", "0")

	// Output to temp path
	ffmpegArgs = append(ffmpegArgs, outTempPath)

	cmdVideo := exec.Command("ffmpeg", ffmpegArgs...)
	var stderrVideo bytes.Buffer
	cmdVideo.Stderr = &stderrVideo

	if err := cmdVideo.Run(); err != nil {
		fmt.Printf("\n❌ [PASS 2 MUX ERROR] Error details:\n%s\n", stderrVideo.String())
		return fmt.Errorf("video processing error (Pass 2): %v", err)
	}

	return nil
}

func ExtractEmbeddedSubtitle(videoPath, outSrtPath string, subIndex int) error {
	mapArg := fmt.Sprintf("0:s:%d", subIndex)
	cmd := exec.Command("ffmpeg", "-y", "-i", videoPath, "-map", mapArg, outSrtPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("FFmpeg subtitle extraction error: %s", stderr.String())
	}
	return nil
}
