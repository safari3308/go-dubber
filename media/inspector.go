package media

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type StreamInfo struct {
	Index     int               `json:"index"`
	CodecType string            `json:"codec_type"`
	CodecName string            `json:"codec_name"` // Codec identifier (hevc, h264...)
	Width     int               `json:"width"`
	BitRate   string            `json:"bit_rate"`
	Tags      map[string]string `json:"tags"`
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
	SubIndex int    `json:"sub_index"`
	Language string `json:"language"` // "vi", "en", "unknown"
	Title    string `json:"title"`
}

type VideoInfo struct {
	VideoCodec           string // "hevc", "h264", "vp9"...
	IsHEVC               bool   // true if video is already HEVC/H.265
	Width                int
	Bitrate              int64  // bps
	IsWellCompressed     bool   // true if bitrate is already optimized for resolution
	HasKokoroTrack       bool
	HasGenericDubTrack   bool
	AudioTrackCount      int
	OriginalAudioIndices []int
	OriginalSubIndices   []int
	EmbeddedSubStreams   []EmbeddedSubInfo
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
		// 1. Inspect codec & resolution
		if s.CodecType == "video" && info.VideoCodec == "" {
			info.VideoCodec = strings.ToLower(s.CodecName)
			info.Width = s.Width
			if info.VideoCodec == "hevc" || info.VideoCodec == "h265" {
				info.IsHEVC = true
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

		isKokoro := strings.Contains(tagDump, "kokoro") || strings.Contains(tagDump, "ai dubbed")
		isGenericDub := strings.Contains(tagDump, "thuyết minh") ||
			strings.Contains(tagDump, "synced embedded") ||
			strings.Contains(tagDump, "fallback")

		if isKokoro {
			info.HasKokoroTrack = true
		}
		if isGenericDub {
			info.HasGenericDubTrack = true
		}

		switch s.CodecType {
		case "audio":
			info.AudioTrackCount++
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

				info.EmbeddedSubStreams = append(info.EmbeddedSubStreams, EmbeddedSubInfo{
					SubIndex: subStreamCounter,
					Language: lang,
					Title:    title,
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

	return info, nil
}


