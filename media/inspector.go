package media

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type DispositionInfo struct {
	AttachedPic int `json:"attached_pic"`
}

type StreamInfo struct {
	Index       int               `json:"index"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"` // Codec identifier (hevc, h264...)
	Width       int               `json:"width"`
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

type EmbeddedSubInfo struct {
	SubIndex  int    `json:"sub_index"`  // Relative 0-based subtitle stream index
	StreamIdx int    `json:"stream_idx"` // Absolute stream index in video file
	Language  string `json:"language"`   // "vi", "en", "unknown"
	Title     string `json:"title"`
	CodecName string `json:"codec_name"` // "subrip", "ass", "dvd_subtitle", etc.
	IsBitmap  bool   `json:"is_bitmap"`   // true if graphic/image subtitle
}

// IsBitmapSubtitleCodec checks if subtitle codec is image/bitmap based (e.g. VobSub, PGS)
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


// Store audio track information
type AudioStreamDetails struct {
	AudioIndex int               // Relative index between audio tracks (0, 1, 2...)
	IsKokoro   bool
	Tags       map[string]string
}

type VideoInfo struct {
	VideoCodec           string // "hevc", "h264", "vp9", "av1"...
	IsHEVC               bool   // true if video is already HEVC/H.265
	IsAV1                bool   // true if video is already AV1
	Width                int
	Bitrate              int64  // bps
	Duration             float64
	IsWellCompressed     bool   // true if bitrate is already optimized for resolution
	HasKokoroTrack       bool
	HasGenericDubTrack   bool   // true if existing Vietnamese dub track is present
	HasEnglishAudio      bool   // true if English audio track is present
	AudioTrackCount      int
	OriginalAudioIndices []int
	AudioStreams         []AudioStreamDetails // List of audio tracks
	OriginalSubIndices   []int
	EmbeddedSubStreams   []EmbeddedSubInfo
}

// HasAudioLanguage checks if any audio track (original or dubbed) matches targetLang
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

// 🌟 Function to check if audio track matches the required language
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

			// 🌟 Added ISO mapping for Chinese, Japanese, English, Vietnamese
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

// 🌟 Function to select original audio index using 3-tier fallback
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

// PromptUserSelectSub displays a list of embedded subtitles and allows users to select manually
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

// normalizeLanguage inspects stream tags to identify Vietnamese ("vi") or English ("en")
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
		strings.Contains(titleVal, "viet") || strings.Contains(titleVal, "tiếng việt") || strings.Contains(titleVal, "thuyết minh") {
		return "vi"
	}
	if langVal == "eng" || langVal == "en" || langVal == "english" ||
		strings.Contains(titleVal, "english") || strings.Contains(titleVal, "eng") {
		return "en"
	}
	return "unknown"
}

// InspectVideo probes video resolution, codec, bitrate, and optimization matrix
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
		OriginalSubIndices:   []int{},
		EmbeddedSubStreams:   []EmbeddedSubInfo{},
	}

	audioStreamCounter := 0
	subStreamCounter := 0

	for _, s := range probe.Streams {
		// 1. Inspect main video stream (ignoring cover images / attached pictures)
		if s.CodecType == "video" && info.VideoCodec == "" && s.Disposition.AttachedPic == 0 {
			info.VideoCodec = strings.ToLower(s.CodecName)
			info.Width = s.Width
			if info.VideoCodec == "hevc" || info.VideoCodec == "h265" {
				info.IsHEVC = true
			}
			if info.VideoCodec == "av1" {
				info.IsAV1 = true
				info.IsWellCompressed = true // AV1 is modern and optimal
			}

			if s.BitRate != "" {
				if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil && br > 0 {
					info.Bitrate = br
				}
			}
		}

		// 2. Check stream tags for existing dubbed tracks
		tagDump := ""
		for k, v := range s.Tags {
			tagDump += " " + strings.ToLower(k) + ":" + strings.ToLower(v)
		}

		isViet := matchLanguage(s.Tags, "vi") || normalizeLanguage(s.Tags) == "vi"

		// Flag Kokoro track for any audio stream containing kokoro or ai dubbed
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
			info.AudioStreams = append(info.AudioStreams, AudioStreamDetails{
				AudioIndex: audioStreamCounter,
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

	// Extract bitrate from Container Format if stream bitrate is missing
	if info.Bitrate == 0 && probe.Format.BitRate != "" {
		if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil && br > 0 {
			info.Bitrate = br
		}
	}

	// Fallback bitrate calculation from size / duration if needed
	if info.Bitrate == 0 && probe.Format.Size != "" && probe.Format.Duration != "" {
		if sizeBytes, err1 := strconv.ParseInt(probe.Format.Size, 10, 64); err1 == nil {
			if durationSec, err2 := strconv.ParseFloat(probe.Format.Duration, 64); err2 == nil && durationSec > 0 {
				info.Bitrate = int64(float64(sizeBytes*8) / durationSec)
			}
		}
	}

	// 3. SAFE SKIP ENCODE MATRIX
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

	// 🌟 1. Get duration from Container Format
	if probe.Format.Duration != "" && probe.Format.Duration != "N/A" {
		if dur, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && dur > 0 {
			info.Duration = dur
		}
	}

	// 🌟 2. FALLBACK: If Container doesn't have, scan through Streams (Video/Audio stream)
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


