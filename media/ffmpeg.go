package media

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/safari3308/go-dubber/config"
)

// ExtractAudioAnchor extracts a reference audio track to send to the Server for timeline alignment
func ExtractAudioAnchor(videoPath, outputPath string) error {
	cmd := exec.Command("ffmpeg", "-y",
		"-i", videoPath,
		"-vn", "-ac", "1", "-ar", "16000", "-b:a", "32k",
		outputPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract anchor audio: %s", stderr.String())
	}
	return nil
}

// RemuxVideo performs AI voice audio mixing and video muxing/encoding via FFmpeg using a decoupled 2-pass architecture
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
	// 🌟 =========================================================================
	// PASS 1: CPU AUDIO PRE-MIXING (ISOLATED AUDIO MIX - TAKES ONLY 1-2 SECONDS)
	// =========================================================================
	mixFilter := fmt.Sprintf(
		"[1:a]aresample=48000:async=1,pan=stereo|c0=c0|c1=c0,volume=%s,apad[tts];"+
			"[0:a:0]aresample=48000:async=1[bg];"+
			"[bg][tts]amix=inputs=2:duration=first:dropout_transition=0:normalize=0[mix_layer]",
		cfg.FFmpeg.VolumeBoost,
	)

	noBgAudioFilter := fmt.Sprintf(
		"[1:a]aresample=48000,pan=stereo|c0=c0|c1=c0,volume=%s[mix_layer]",
		cfg.FFmpeg.VolumeBoost,
	)

	activeMixFilter := mixFilter
	if info.AudioTrackCount == 0 {
		activeMixFilter = noBgAudioFilter
	}

	tempMixedAudio := filepath.Join(filepath.Dir(outTempPath), "temp_mixed_"+filepath.Base(wavPath)+".aac")
	defer os.Remove(tempMixedAudio)

	audioCmdArgs := []string{
		"-y",
		"-i", videoPath,
		"-i", wavPath,
		"-filter_complex", activeMixFilter,
		"-map", "[mix_layer]",
		"-c:a", "aac",
		"-b:a", "320k",
		tempMixedAudio,
	}

	cmdAudio := exec.Command("ffmpeg", audioCmdArgs...)
	var stderrAudio bytes.Buffer
	cmdAudio.Stderr = &stderrAudio
	if err := cmdAudio.Run(); err != nil {
		return fmt.Errorf("audio mixing error (Pass 1): %s", stderrAudio.String())
	}

	// 🌟 =========================================================================
	// PASS 2: CLEAN VIDEO MUX / ENCODE VIA GPU (NOT BOTTLENECKED BY CPU AUDIO FILTER)
	// =========================================================================
	var ffmpegArgs []string
	if isExternalSub {
		ffmpegArgs = []string{"-y", "-i", videoPath, "-i", tempMixedAudio, "-i", subPath}
	} else {
		ffmpegArgs = []string{"-y", "-i", videoPath, "-i", tempMixedAudio}
	}

	// Route stream track mapping (including font attachments 0:t?)
	if isExternalSub {
		if info.AudioTrackCount > 0 {
			ffmpegArgs = append(ffmpegArgs,
				"-map", "0:v:0", // Original video
				"-map", "0:a", // All original audio tracks
				"-map", "1:a", // AI dubbed audio track mixed in Pass 1
				"-map", "0:s?", // Original subtitles in video file
				"-map", "2:s", // External subtitle file
				"-map", "0:t?", // Embedded font attachments
			)
		} else {
			ffmpegArgs = append(ffmpegArgs,
				"-map", "0:v:0",
				"-map", "1:a",
				"-map", "0:s?",
				"-map", "2:s",
				"-map", "0:t?",
			)
		}
	} else {
		if info.AudioTrackCount > 0 {
			ffmpegArgs = append(ffmpegArgs,
				"-map", "0:v:0",
				"-map", "0:a",
				"-map", "1:a",
				"-map", "0:s?",
				"-map", "0:t?",
			)
		} else {
			ffmpegArgs = append(ffmpegArgs,
				"-map", "0:v:0",
				"-map", "1:a",
				"-map", "0:s?",
				"-map", "0:t?",
			)
		}
	}

	// Configure Video Codec
	if info.IsHEVC || info.IsAV1 || info.IsWellCompressed {
		// Ultra-fast Stream Copy (5-10s)
		ffmpegArgs = append(ffmpegArgs, "-c:v", "copy")
	} else if cfg.UseGPU {
		// GPU encoding using hardware-appropriate parameters
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
				gpuPixFmt = "p010le" // 10-bit color depth to eliminate anime banding
			} else {
				gpuPixFmt = "yuv420p10le"
			}
		}

		ffmpegArgs = append(ffmpegArgs, "-c:v", gpuCodec)
		if gpuPixFmt != "" {
			ffmpegArgs = append(ffmpegArgs, "-pix_fmt", gpuPixFmt)
		}

		gpuCq := cfg.FFmpeg.GPUCq
		if strings.Contains(gpuCodec, "videotoolbox") {
			// Apple VideoToolbox (MacBook M1/M2/M3/M4)
			// Uses -q:v for quality control (scale ~1-100, default 60).
			// Does NOT support -cq or -preset flags.
			if gpuCq <= 0 {
				gpuCq = 60
			}
			ffmpegArgs = append(ffmpegArgs, "-q:v", strconv.Itoa(gpuCq))
		} else if strings.Contains(gpuCodec, "nvenc") {
			// NVIDIA NVENC
			if gpuCq <= 0 {
				gpuCq = 21
			}
			ffmpegArgs = append(ffmpegArgs, "-cq", strconv.Itoa(gpuCq))
			gpuPreset := cfg.FFmpeg.GPUPreset
			if gpuPreset == "" {
				gpuPreset = "p6"
			}
			ffmpegArgs = append(ffmpegArgs, "-preset", gpuPreset, "-spatial-aq", "1", "-temporal-aq", "1")
		} else if strings.Contains(gpuCodec, "qsv") {
			// Intel QuickSync
			if gpuCq <= 0 {
				gpuCq = 21
			}
			ffmpegArgs = append(ffmpegArgs, "-global_quality", strconv.Itoa(gpuCq))
			if cfg.FFmpeg.GPUPreset != "" {
				ffmpegArgs = append(ffmpegArgs, "-preset", cfg.FFmpeg.GPUPreset)
			}
		} else {
			// Fallback / Custom GPU encoder
			if gpuCq > 0 {
				ffmpegArgs = append(ffmpegArgs, "-q:v", strconv.Itoa(gpuCq))
			}
			if cfg.FFmpeg.GPUPreset != "" {
				ffmpegArgs = append(ffmpegArgs, "-preset", cfg.FFmpeg.GPUPreset)
			}
		}

		if len(cfg.FFmpeg.GPUExtraArgs) > 0 {
			ffmpegArgs = append(ffmpegArgs, cfg.FFmpeg.GPUExtraArgs...)
		}
	} else {
		cpuPreset := cfg.FFmpeg.CPUPreset
		if cpuPreset == "" {
			cpuPreset = "medium"
		}
		cpuCrf := cfg.FFmpeg.CPUCrf
		if cpuCrf <= 0 {
			cpuCrf = 20
		}

		ffmpegArgs = append(ffmpegArgs,
			"-c:v", "libx265",
			"-crf", strconv.Itoa(cpuCrf),
			"-preset", cpuPreset,
			"-tune", "animation",
			"-pix_fmt", "yuv420p10le",
		)
	}

	// Audio & Subtitle are finalized -> use direct Stream Copy!
	ffmpegArgs = append(ffmpegArgs,
		"-c:a", "copy",
		"-c:s", "copy",
	)

	// Set metadata names for dubbed audio track & new subtitle
	trackLang := "vie"
	subTitle := "Vietnamese"
	if lang == "en" {
		trackLang = "eng"
		subTitle = "English"
	}

	newAudioIndex := info.AudioTrackCount
	ffmpegArgs = append(ffmpegArgs,
		fmt.Sprintf("-metadata:s:a:%d", newAudioIndex), "title=AI Dubbed (Kokoro AI)",
		fmt.Sprintf("-metadata:s:a:%d", newAudioIndex), "language="+trackLang,
	)

	if isExternalSub {
		newSubIndex := len(info.EmbeddedSubStreams)
		ffmpegArgs = append(ffmpegArgs,
			fmt.Sprintf("-metadata:s:s:%d", newSubIndex), "title="+subTitle,
			fmt.Sprintf("-metadata:s:s:%d", newSubIndex), "language="+trackLang,
		)
	}

	ffmpegArgs = append(ffmpegArgs, outTempPath)

	cmdVideo := exec.Command("ffmpeg", ffmpegArgs...)
	var stderrVideo bytes.Buffer
	cmdVideo.Stderr = &stderrVideo

	if err := cmdVideo.Run(); err != nil {
		return fmt.Errorf("video processing error (Pass 2): %s", stderrVideo.String())
	}

	return nil
}

// ExtractEmbeddedSubtitle extracts an embedded subtitle track from the video to a temporary .srt file
func ExtractEmbeddedSubtitle(videoPath, outSrtPath string, subIndex int) error {
	// subIndex is the subtitle track position (e.g. 0, 1, 2)
	mapArg := fmt.Sprintf("0:s:%d", subIndex)
	
	cmd := exec.Command("ffmpeg", 
		"-y", 
		"-i", videoPath, 
		"-map", mapArg, 
		outSrtPath,
	)
	
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("FFmpeg subtitle extraction error: %s", stderr.String())
	}
	return nil
}
