package media

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/safari3308/go-dubber/internal/subtitle"
)

type DispositionInfo struct {
	AttachedPic int `json:"attached_pic"`
}

type StreamInfo struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"` // Codec identifier (hevc, h264...)
	Width       int               `json:"width"`
	Channels    int               `json:"channels"`
	BitRate     string            `json:"bit_rate"`
	Duration    string            `json:"duration"`
	Tags        map[string]string `json:"tags"`
	Disposition DispositionInfo   `json:"disposition"`
}

type FormatInfo struct {
	BitRate  string `json:"bit_rate"`
	Duration string `json:"duration"`
	Size     string `json:"size"`
}

type FFProbeOutput struct {
	Streams []StreamInfo `json:"streams"`
	Format  FormatInfo   `json:"format"`
}

// EmbeddedSubInfo aliases subtitle.EmbeddedSubInfo so callers can reference either package.
type EmbeddedSubInfo = subtitle.EmbeddedSubInfo

// IsBitmapSubtitleCodec checks if subtitle codec is image/bitmap based (e.g. VobSub, PGS).
func IsBitmapSubtitleCodec(codecName string) bool {
	c := strings.ToLower(strings.TrimSpace(codecName))
	if c == "" {
		return false
	}
	return c == "dvd_subtitle" || c == "dvdsub" ||
		c == "hdmv_pgs_subtitle" || c == "pgssub" || c == "pgs" ||
		c == "xsub" || c == "dvb_subtitle" || c == "dvbsub" ||
		c == "arib_caption" || c == "vobsub"
}

// AudioStreamDetails stores audio track information.
type AudioStreamDetails struct {
	AudioIndex int               `json:"audio_index"` // Relative index between audio tracks (0, 1, 2...)
	StreamIdx  int               `json:"stream_idx"`  // Absolute stream index in container
	CodecName  string            `json:"codec_name"`
	Channels   int               `json:"channels"`
	Language   string            `json:"language"`
	Title      string            `json:"title"`
	IsKokoro   bool              `json:"is_kokoro"`
	Tags       map[string]string `json:"tags"`
}

type VideoInfo struct {
	VideoCodec           string               `json:"video_codec"`
	IsHEVC               bool                 `json:"is_hevc"`
	IsAV1                bool                 `json:"is_av1"`
	Width                int                  `json:"width"`
	Bitrate              int64                `json:"bitrate"`
	Duration             float64              `json:"duration"`
	IsWellCompressed     bool                 `json:"is_well_compressed"`
	HasKokoroTrack       bool                 `json:"has_kokoro_track"`
	HasGenericDubTrack   bool                 `json:"has_generic_dub_track"`
	HasEnglishAudio      bool                 `json:"has_english_audio"`
	AudioTrackCount      int                  `json:"audio_track_count"`
	OriginalAudioIndices []int                `json:"original_audio_indices"`
	AudioStreams         []AudioStreamDetails `json:"audio_streams"`
	OriginalSubIndices   []int                `json:"original_sub_indices"`
	EmbeddedSubStreams   []EmbeddedSubInfo    `json:"embedded_sub_streams"`
}

// HasAudioLanguage checks if any audio track (original or dubbed) matches targetLang.
func (v *VideoInfo) HasAudioLanguage(targetLang string) bool {
	if targetLang == "" {
		return false
	}
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	for _, stream := range v.AudioStreams {
		if matchLanguage(stream.Tags, targetLang) {
			return true
		}
		norm := normalizeLanguage(stream.Tags)
		if norm != "unknown" && norm == targetLang {
			return true
		}
	}
	return false
}

func matchLanguage(tags map[string]string, targetLang string) bool {
	if tags == nil || targetLang == "" {
		return false
	}
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	for k, v := range tags {
		lk := strings.ToLower(k)
		lv := strings.ToLower(v)
		if lk == "language" || lk == "lang" || lk == "title" {
			if lv == targetLang || strings.HasPrefix(lv, targetLang) {
				return true
			}

			isChinese := (targetLang == "cn" || targetLang == "zh" || targetLang == "chi" || targetLang == "zho") &&
				(lv == "chi" || lv == "zho" || lv == "zh" || lv == "cn" || strings.Contains(lv, "chin"))
			isJapanese := (targetLang == "ja" || targetLang == "jp") && (lv == "jpn" || strings.Contains(lv, "japan"))
			isEnglish := (targetLang == "en") && (lv == "eng" || strings.Contains(lv, "english"))
			isVietnamese := (targetLang == "vi") && (lv == "vie" || strings.Contains(lv, "viet") || strings.Contains(lv, "thuyết minh") || strings.Contains(lv, "tiếng việt"))

			if isChinese || isJapanese || isEnglish || isVietnamese {
				return true
			}
		}
	}
	return false
}

// SelectOriginalAudioIndex selects the original audio index using a 3-tier fallback.
func (v *VideoInfo) SelectOriginalAudioIndex(targetLang string, defaultIndex int) int {
	if v.AudioTrackCount == 0 {
		return 0
	}

	// Tier 1: Find by required language (OriginalLanguage) among non-Kokoro tracks
	if targetLang != "" {
		for _, stream := range v.AudioStreams {
			if !stream.IsKokoro && matchLanguage(stream.Tags, targetLang) {
				fmt.Printf("    ✅ Found Audio Track #%d matching language '%s'\n", stream.AudioIndex, targetLang)
				return stream.AudioIndex
			}
		}
		fmt.Printf("    ⚠️ Not found Audio Track '%s'. Moving to Fallback Index...\n", targetLang)
	}

	// Tier 2: Fallback to configured Audio Track Index (If valid within range and non-Kokoro)
	if defaultIndex >= 0 && defaultIndex < len(v.AudioStreams) {
		if !v.AudioStreams[defaultIndex].IsKokoro {
			fmt.Printf("    🔄 Using Fallback Audio Track Index: #%d\n", defaultIndex)
			return defaultIndex
		}
	}

	// Tier 3: Fallback to first non-Kokoro audio track
	for _, stream := range v.AudioStreams {
		if !stream.IsKokoro {
			return stream.AudioIndex
		}
	}

	return 0
}

// PromptUserSelectSub displays a list of embedded subtitles and allows CLI users to select manually.
func PromptUserSelectSub(subs []EmbeddedSubInfo) *EmbeddedSubInfo {
	if len(subs) == 0 {
		fmt.Println("⚠️ File does not contain any embedded subtitle tracks.")
		return nil
	}

	fmt.Println("\n==================================================")
	fmt.Println("🔍 [INTERACTIVE MODE] List of embedded subtitles:")
	for i, sub := range subs {
		title := sub.Title
		if title == "" {
			title = "<No title>"
		}
		typeLabel := sub.CodecName
		if typeLabel == "" {
			typeLabel = "unknown"
		}
		if sub.IsBitmap {
			typeLabel += " (Bitmap - unsupported for TTS)"
		}
		fmt.Printf("  [%d] Track Sub #%d | Language: %s | Format: %s | Title: %s\n", i+1, sub.SubIndex, sub.Language, typeLabel, title)
	}
	fmt.Println("  [0] Skip (Do not use embedded sub)")
	fmt.Println("==================================================")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("👉 Enter the track number you want to use as VietSub [0-", len(subs), "]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		choice, err := strconv.Atoi(input)
		if err == nil {
			if choice == 0 {
				return nil
			}
			if choice >= 1 && choice <= len(subs) {
				selected := subs[choice-1]
				fmt.Printf("✅ Selected Track Sub #%d (%s)\n", selected.SubIndex, selected.Title)
				return &selected
			}
		}
		fmt.Println("❌ Invalid choice, please try again.")
	}
}

func normalizeLanguage(tags map[string]string) string {
	if tags == nil {
		return "unknown"
	}
	langVal := ""
	titleVal := ""
	for k, v := range tags {
		lk := strings.ToLower(k)
		lv := strings.ToLower(v)
		if lk == "language" || lk == "lang" {
			langVal = lv
		}
		if lk == "title" {
			titleVal = lv
		}
	}

	if langVal == "vie" || langVal == "vi" || langVal == "vietnamese" || langVal == "viet" ||
		strings.Contains(titleVal, "viet") || strings.Contains(titleVal, "tiếng việt") || strings.Contains(titleVal, "tieng viet") ||
		strings.Contains(titleVal, "thuyết minh") || strings.Contains(titleVal, "vietsub") || strings.Contains(titleVal, "vsub") {
		return "vi"
	}
	if langVal == "eng" || langVal == "en" || langVal == "english" ||
		strings.Contains(titleVal, "english") || strings.Contains(titleVal, "engsub") {
		return "en"
	}
	if langVal == "jpn" || langVal == "ja" || langVal == "jp" || langVal == "japanese" ||
		strings.Contains(titleVal, "japan") || strings.Contains(titleVal, "japsub") {
		return "ja"
	}
	if langVal == "zho" || langVal == "chi" || langVal == "zh" || langVal == "cn" || langVal == "chinese" ||
		strings.Contains(titleVal, "chinese") || strings.Contains(titleVal, "chinsub") {
		return "zh"
	}
	if langVal != "" && langVal != "und" && langVal != "unk" && langVal != "unknown" && len(langVal) <= 3 {
		return langVal
	}
	return "unknown"
}

// InspectVideo probes video resolution, codec, bitrate, and streams via ffprobe.
func InspectVideo(videoPath string) (*VideoInfo, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-show_streams",
		"-show_format",
		"-of", "json",
		videoPath,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe error: %w", err)
	}

	var probe FFProbeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("json parse error: %w", err)
	}

	info := &VideoInfo{
		HasKokoroTrack:       false,
		OriginalAudioIndices: []int{},
		AudioStreams:         []AudioStreamDetails{},
		OriginalSubIndices:   []int{},
		EmbeddedSubStreams:   []EmbeddedSubInfo{},
	}

	audioStreamCounter := 0
	subStreamCounter := 0

	for _, s := range probe.Streams {
		if s.CodecType == "video" && info.VideoCodec == "" && s.Disposition.AttachedPic == 0 {
			info.VideoCodec = strings.ToLower(s.CodecName)
			info.Width = s.Width
			if info.VideoCodec == "hevc" || info.VideoCodec == "h265" {
				info.IsHEVC = true
			}
			if info.VideoCodec == "av1" {
				info.IsAV1 = true
				info.IsWellCompressed = true
			}

			if s.BitRate != "" {
				if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil && br > 0 {
					info.Bitrate = br
				}
			}
		}

		tagDump := ""
		for k, v := range s.Tags {
			tagDump += " " + strings.ToLower(k) + ":" + strings.ToLower(v)
		}

		isViet := matchLanguage(s.Tags, "vi") || normalizeLanguage(s.Tags) == "vi"

		isKokoro := (s.CodecType == "audio") && (strings.Contains(tagDump, "kokoro") || strings.Contains(tagDump, "ai dubbed"))
		isGenericDub := (s.CodecType == "audio") && (isViet || strings.Contains(tagDump, "thuyết minh") || strings.Contains(tagDump, "synced embedded") || strings.Contains(tagDump, "fallback"))

		if isKokoro {
			info.HasKokoroTrack = true
		}
		if isGenericDub {
			info.HasGenericDubTrack = true
		}

		switch s.CodecType {
		case "audio":
			info.AudioTrackCount++
			if matchLanguage(s.Tags, "en") {
				info.HasEnglishAudio = true
			}
			audioTitle := ""
			for k, v := range s.Tags {
				if strings.EqualFold(k, "title") {
					audioTitle = v
					break
				}
			}
			info.AudioStreams = append(info.AudioStreams, AudioStreamDetails{
				AudioIndex: audioStreamCounter,
				StreamIdx:  s.Index,
				CodecName:  s.CodecName,
				Channels:   s.Channels,
				Language:   normalizeLanguage(s.Tags),
				Title:      audioTitle,
				IsKokoro:   isKokoro,
				Tags:       s.Tags,
			})
			if !isKokoro {
				info.OriginalAudioIndices = append(info.OriginalAudioIndices, audioStreamCounter)
			}
			audioStreamCounter++
		case "subtitle":
			if !isKokoro {
				info.OriginalSubIndices = append(info.OriginalSubIndices, subStreamCounter)

				lang := normalizeLanguage(s.Tags)
				title := ""
				for k, v := range s.Tags {
					if strings.EqualFold(k, "title") {
						title = v
						break
					}
				}

				isBitmap := IsBitmapSubtitleCodec(s.CodecName)
				info.EmbeddedSubStreams = append(info.EmbeddedSubStreams, EmbeddedSubInfo{
					SubIndex:  subStreamCounter,
					StreamIdx: s.Index,
					Language:  lang,
					Title:     title,
					CodecName: s.CodecName,
					IsBitmap:  isBitmap,
				})
			}
			subStreamCounter++
		}
	}

	if info.Bitrate == 0 && probe.Format.BitRate != "" {
		if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil && br > 0 {
			info.Bitrate = br
		}
	}

	if info.Bitrate == 0 && probe.Format.Size != "" && probe.Format.Duration != "" {
		if sizeBytes, err1 := strconv.ParseInt(probe.Format.Size, 10, 64); err1 == nil {
			if durationSec, err2 := strconv.ParseFloat(probe.Format.Duration, 64); err2 == nil && durationSec > 0 {
				info.Bitrate = int64(float64(sizeBytes*8) / durationSec)
			}
		}
	}

	// SAFE SKIP ENCODE MATRIX
	bitrateKbps := info.Bitrate / 1000
	if bitrateKbps > 0 {
		if info.Width >= 3840 && bitrateKbps <= 7500 {
			info.IsWellCompressed = true
		} else if info.Width >= 1920 && bitrateKbps <= 2500 {
			info.IsWellCompressed = true
		} else if info.Width >= 1280 && bitrateKbps <= 1400 {
			info.IsWellCompressed = true
		} else if info.Width > 0 && info.Width < 1280 && bitrateKbps <= 800 {
			info.IsWellCompressed = true
		}
	}

	if probe.Format.Duration != "" && probe.Format.Duration != "N/A" {
		if dur, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && dur > 0 {
			info.Duration = dur
		}
	}

	if info.Duration == 0 {
		for _, s := range probe.Streams {
			if s.Duration != "" && s.Duration != "N/A" {
				if dur, err := strconv.ParseFloat(s.Duration, 64); err == nil && dur > 0 {
					info.Duration = dur
					break
				}
			}
		}
	}

	return info, nil
}
