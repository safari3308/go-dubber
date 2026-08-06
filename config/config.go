package config

import (
	"encoding/json"
	"os"
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
	NasDir          string       `json:"nas_dir"`
	ApiUrl          string       `json:"api_url"`
	ApiToken        string       `json:"api_token"`
	InteractiveMode bool         `json:"interactive_mode"`
	DefaultSubIndex int          `json:"default_sub_index"`
	SubLanguage     string       `json:"sub_language"`
	UseGPU          bool         `json:"use_gpu"`
	Workers         int          `json:"workers"`
	ForceReprocess  bool         `json:"force_reprocess"`
	SkipEncode      bool         `json:"skip_encode"`
	OnlyCheckKokoro bool         `json:"only_check_kokoro"`
	TTSSpeed        float64      `json:"tts_speed"`
	FFmpeg          FFmpegConfig `json:"ffmpeg"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cfg Config
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.TTSSpeed <= 0 {
		cfg.TTSSpeed = 1.1
	}
	return &cfg, nil
}
