package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
	WorkDir             string       `json:"work_dir,omitempty"`
	ServerPort          int          `json:"server_port,omitempty"`
	ApiUrl              string       `json:"api_url"`
	GrpcUrl             string       `json:"grpc_url,omitempty"`
	ApiToken            string       `json:"api_token"`
	ForceFallbackSub    bool         `json:"force_fallback_sub"`
	InteractiveMode     bool         `json:"interactive_mode"`
	DefaultSubIndex     int          `json:"default_sub_index"`
	DubLanguages        []string     `json:"dub_languages"`     // e.g., ["en", "vi"]
	SubLanguage         string       `json:"sub_language"`      // legacy single target language
	OriginalLanguage    string       `json:"original_language"` // e.g., "ja", "en", "vi"
	OriginalAudioIndex  int          `json:"original_audio_index"`
	UseGPU              bool         `json:"use_gpu"`
	AutoDetectHardware  bool         `json:"auto_detect_hardware,omitempty"`
	Workers             int          `json:"workers"`
	ForceReprocess      bool         `json:"force_reprocess"`
	SkipEncode          bool         `json:"skip_encode"`
	OnlyCheckKokoro     bool         `json:"only_check_kokoro"`
	TTSSpeed            float64      `json:"tts_speed"`
	FFmpeg              FFmpegConfig `json:"ffmpeg"`
	SkipSubSync         bool         `json:"skip_sub_sync"`
	AITrackAsFirstTrack bool         `json:"ai_track_as_first_track"`
	DropSongSubtitles   bool         `json:"drop_song_subtitles"`
	ReuseAudioChunks    bool         `json:"reuse_audio_chunks,omitempty"`
	GeminiEnabled       bool         `json:"gemini_enabled,omitempty"`
	GeminiAPIKey        string       `json:"gemini_api_key,omitempty"`
	GeminiCleanForTTS   bool         `json:"gemini_clean_for_tts,omitempty"`
	GeminiTranslate     bool         `json:"gemini_translate,omitempty"`
	GeminiStyle         string       `json:"gemini_style,omitempty"` // "standard" or "donghua"
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		WorkDir:            "./workdir",
		ServerPort:         8080,
		ApiUrl:             "http://localhost:8000",
		DubLanguages:       []string{"vi"},
		UseGPU:             true,
		AutoDetectHardware: true,
		Workers:            2,
		TTSSpeed:           1.1,
		DropSongSubtitles:  true,
		ReuseAudioChunks:   true,
		GeminiCleanForTTS:  true,
		GeminiStyle:        "standard",
	}
}

// Normalize applies default values and normalizes language lists.
func (cfg *Config) Normalize() {
	if cfg.TTSSpeed <= 0 {
		cfg.TTSSpeed = 1.1
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "./workdir"
	}
	if cfg.ServerPort <= 0 {
		cfg.ServerPort = 8080
	}

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
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := Config{
		DropSongSubtitles:  true,
		AutoDetectHardware: true,
		ReuseAudioChunks:   true,
	}
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}
	cfg.Normalize()

	return &cfg, nil
}

// SaveConfig persists cfg as pretty-printed JSON atomically.
func SaveConfig(path string, cfg *Config) error {
	cfg.Normalize()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// ResolveConfigPath locates config.json, first checking preferredPath,
// then checking next to the executable if not found in the current working directory.
func ResolveConfigPath(preferredPath string) string {
	if preferredPath != "" {
		if _, err := os.Stat(preferredPath); err == nil {
			return preferredPath
		}
	}
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidate := filepath.Join(exeDir, "config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if preferredPath != "" {
		return preferredPath
	}
	return "config.json"
}

// GetConfigFileChecksum computes a SHA-256 checksum of the config file.
func GetConfigFileChecksum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
