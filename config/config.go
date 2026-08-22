package config

import (
	"encoding/json"
	"os"
	"strings"
)

type FFmpegConfig struct {
	GPUCodec        string   `json:"gpu_codec"`
	GPUPreset       string   `json:"gpu_preset"`
	GPUCq           int      `json:"gpu_cq"`
	GPUPixFmt       string   `json:"gpu_pix_fmt"`
	GPUExtraArgs    []string `json:"gpu_extra_args"`
	CPUPreset       string   `json:"cpu_preset"`
	CPUCrf          int      `json:"cpu_crf"`
	AudioBitrate    string   `json:"audio_bitrate"`
	AudioSampleRate string   `json:"audio_sample_rate"`
	AudioChannels   string   `json:"audio_channels"`
	VolumeBoost     string   `json:"volume_boost"`
}

type Config struct {
	NasDir              string       `json:"nas_dir"`
	ApiUrl              string       `json:"api_url"`
	ApiToken            string       `json:"api_token"`
	ForceFallbackSub    bool         `json:"force_fallback_sub"`
	InteractiveMode     bool         `json:"interactive_mode"`
	DefaultSubIndex     int          `json:"default_sub_index"`
	DubLanguages        []string     `json:"dub_languages"`     // e.g., ["en", "vi"]
	SubLanguage         string       `json:"sub_language"`      // legacy single target language
	OriginalLanguage    string       `json:"original_language"` // e.g., "ja", "en", "vi"
	OriginalAudioIndex  int          `json:"original_audio_index"` // fallback index if not found
	UseGPU              bool         `json:"use_gpu"`
	Workers             int          `json:"workers"`
	ForceReprocess      bool         `json:"force_reprocess"`
	SkipEncode          bool         `json:"skip_encode"`
	OnlyCheckKokoro     bool         `json:"only_check_kokoro"`
	TTSSpeed            float64      `json:"tts_speed"`
	FFmpeg              FFmpegConfig `json:"ffmpeg"`
	SkipSubSync         bool         `json:"skip_sub_sync"`
	AITrackAsFirstTrack bool         `json:"ai_track_as_first_track"`
	DropSongSubtitles   bool         `json:"drop_song_subtitles"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := Config{
		DropSongSubtitles: true,
	}
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.TTSSpeed <= 0 {
		cfg.TTSSpeed = 1.1
	}

	// Normalize DubLanguages
	var normLangs []string
	seen := make(map[string]bool)
	for _, l := range cfg.DubLanguages {
		cleaned := strings.ToLower(strings.TrimSpace(l))
		if cleaned != "" && !seen[cleaned] {
			seen[cleaned] = true
			normLangs = append(normLangs, cleaned)
		}
	}

	if len(normLangs) == 0 {
		subLang := strings.ToLower(strings.TrimSpace(cfg.SubLanguage))
		if subLang != "" {
			normLangs = append(normLangs, subLang)
		} else {
			normLangs = append(normLangs, "vi")
		}
	}
	cfg.DubLanguages = normLangs

	return &cfg, nil
}
