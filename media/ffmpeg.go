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
	return strings.Contains(t, "synced") || strings.Contains(t, "kokoro") || strings.Contains(t, "ai dubbed")
}

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
	subTitle := "Vietnamese"
	if normLang == "en" || normLang == "eng" || normLang == "english" {
		trackLang = "eng"
		subTitle = "English"
	}

	streams, _ := inspectStreams(videoPath)

	// =========================================================================
	// PASS 1: HÒA ÂM CHUẨN KÈM RESET MỐC THỜI GIAN
	// =========================================================================

	volBoost := cfg.FFmpeg.VolumeBoost
	if strings.TrimSpace(volBoost) == "" || volBoost == "0" {
		volBoost = "2.5"
	}

	audioBitrate := cfg.FFmpeg.AudioBitrate
	if strings.TrimSpace(audioBitrate) == "" {
		audioBitrate = "192k"
	}

	// Áp dụng downmix và reset timestamp (asetpts=PTS-STARTPTS) bảo vệ tuyệt đối mốc 0:00
	mixFilter := fmt.Sprintf(
		"[0:a:0]aresample=48000:async=1,asetpts=PTS-STARTPTS,aformat=channel_layouts=stereo,volume=1.0[bg];"+
			"[1:a]aresample=48000:async=1,asetpts=PTS-STARTPTS,pan=stereo|c0=c0|c1=c0,volume=%s[tts];"+
			"[bg][tts]amix=inputs=2:duration=first:dropout_transition=0,asetpts=PTS-STARTPTS[mix_layer]",
		volBoost,
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
	// PASS 2: MUX VIDEO KÈM FLAG BẢO VỆ INTERLEAVE VÀ TIMESTAMP MKV
	// =========================================================================
	hasSubInput := subPath != "" && utils.FileExists(subPath)

	var ffmpegArgs []string
	
	// Khởi tạo lệnh với -y và -fflags +genpts để chuẩn hóa lại timestamp gốc bị lỗi (nếu có)
	ffmpegArgs = append(ffmpegArgs, "-y", "-fflags", "+genpts")

	if hasSubInput {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio, "-i", subPath)
	} else {
		ffmpegArgs = append(ffmpegArgs, "-i", videoPath, "-i", tempMixedAudio)
	}

	// 🌟 FIX 2: Ngăn FFmpeg bỏ qua timestamp âm nếu video bị xén cắt trước đó
	ffmpegArgs = append(ffmpegArgs, "-avoid_negative_ts", "make_zero")

	// 1. Map Video
	ffmpegArgs = append(ffmpegArgs, "-map", "0:v:0")

	// 2. Map Audio Streams: Giữ âm thanh gốc, LOẠI BỎ TẤT CẢ các track AI đã tạo
	keptAudioCount := 0
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

	// Nạp Track AI Mới
	ffmpegArgs = append(ffmpegArgs, "-map", "1:a:0")
	newAudioIndex := keptAudioCount

	// 3. Map Subtitles
	keptSubCount := 0
	if len(streams) > 0 {
		for _, st := range streams {
			if st.CodecType == "subtitle" {
				if hasSubInput && isAISubTrack(st.Tags.Title) {
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

	// 4. Map Fonts/Attachments
	ffmpegArgs = append(ffmpegArgs, "-map", "0:t?")

	// Đặt Default Track
	ffmpegArgs = append(ffmpegArgs,
		"-disposition:a", "0",
		fmt.Sprintf("-disposition:a:%d", newAudioIndex), "default",
	)

	// Cấu hình Codec Video
	if info.IsHEVC || info.IsAV1 || info.IsWellCompressed {
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

	// Cấu hình Codec Audio
	ffmpegArgs = append(ffmpegArgs, "-c:a", "copy")
	ffmpegArgs = append(ffmpegArgs,
		fmt.Sprintf("-c:a:%d", newAudioIndex), "aac",
		fmt.Sprintf("-b:a:%d", newAudioIndex), audioBitrate,
		"-c:s", "copy",
	)

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

	// 🌟 FIX 1: Chống lỗi Muxer Interleave Delta - Ép FFmpeg buffer RAM vô hạn khi map nhiều nguồn file khác nhau
	ffmpegArgs = append(ffmpegArgs, "-max_interleave_delta", "0")

	// Đích ra
	ffmpegArgs = append(ffmpegArgs, outTempPath)

	cmdVideo := exec.Command("ffmpeg", ffmpegArgs...)
	var stderrVideo bytes.Buffer
	cmdVideo.Stderr = &stderrVideo

	if err := cmdVideo.Run(); err != nil {
		fmt.Printf("\n❌ [PASS 2 MUX ERROR] Chi tiết lỗi:\n%s\n", stderrVideo.String())
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
