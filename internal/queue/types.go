package queue

import (
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/subtitle"
)

type JobStatus string

const (
	JobPending      JobStatus = "pending"
	JobDownloading  JobStatus = "downloading"
	JobSyncing      JobStatus = "syncing"
	JobSynthesizing JobStatus = "synthesizing"
	JobRemuxing     JobStatus = "remuxing"
	JobUploading    JobStatus = "uploading"
	JobCompleted    JobStatus = "completed"
	JobPaused       JobStatus = "paused"
	JobFailed       JobStatus = "failed"
	JobCancelled    JobStatus = "cancelled"
)

type RunnerStatus string

const (
	RunnerIdle     RunnerStatus = "idle"
	RunnerRunning  RunnerStatus = "running"
	RunnerPausing  RunnerStatus = "pausing"
	RunnerPaused   RunnerStatus = "paused"
	RunnerStopping RunnerStatus = "stopping"
)

// ItemConfig holds per-job dubbing settings that inherit from global Config but can be customized per item.
type ItemConfig struct {
	DubLanguages        []string                   `json:"dub_languages"`
	OriginalLanguage    string                     `json:"original_language"`
	OriginalAudioIndex  int                        `json:"original_audio_index"`
	SourceAudioIndex    *int                       `json:"source_audio_index,omitempty"` // nil = auto, -1 = none (TTS only), >=0 = explicit audio track index
	TTSSpeed            float64                    `json:"tts_speed"`
	Workers             int                        `json:"workers"`
	UseGPU              bool                       `json:"use_gpu"`
	SkipEncode          bool                       `json:"skip_encode"`
	SkipSubSync         bool                       `json:"skip_sub_sync"`
	ForceReprocess      bool                       `json:"force_reprocess"`
	AITrackAsFirstTrack bool                       `json:"ai_track_as_first_track"`
	DropSongSubtitles   bool                       `json:"drop_song_subtitles"`
	VolumeBoost         string                     `json:"volume_boost"`
	LanguageSpecs       []subtitle.LanguageDubSpec `json:"language_specs,omitempty"`
}

// ItemConfigFromGlobal builds a default per-item config from global settings.
func ItemConfigFromGlobal(cfg *config.Config) ItemConfig {
	langs := append([]string(nil), cfg.DubLanguages...)
	if len(langs) == 0 {
		langs = []string{"vi"}
	}
	return ItemConfig{
		DubLanguages:        langs,
		OriginalLanguage:    cfg.OriginalLanguage,
		OriginalAudioIndex:  cfg.OriginalAudioIndex,
		TTSSpeed:            cfg.TTSSpeed,
		Workers:             cfg.Workers,
		UseGPU:              cfg.UseGPU,
		SkipEncode:          cfg.SkipEncode,
		SkipSubSync:         cfg.SkipSubSync,
		ForceReprocess:      cfg.ForceReprocess,
		AITrackAsFirstTrack: cfg.AITrackAsFirstTrack,
		DropSongSubtitles:   cfg.DropSongSubtitles,
		VolumeBoost:         cfg.FFmpeg.VolumeBoost,
	}
}

// JobMetrics stores final execution metrics for a completed job.
type JobMetrics struct {
	OrigSizeBytes int64   `json:"orig_size_bytes"`
	NewSizeBytes  int64   `json:"new_size_bytes"`
	DurationSec   float64 `json:"duration_sec"`
	CompletedDubs int     `json:"completed_dubs"`
}

// SupportedTTSLanguages lists the languages currently supported by the TTS backend.
var SupportedTTSLanguages = []string{"en", "vi"}

// AvailableLanguageInfo describes whether a supported TTS language has available subtitles and/or existing audio tracks.
type AvailableLanguageInfo struct {
	Language             string `json:"language"`
	HasEmbedded          bool   `json:"has_embedded"`
	HasExternal          bool   `json:"has_external"`
	HasUploaded          bool   `json:"has_uploaded,omitempty"`
	HasExistingAudio     bool   `json:"has_existing_audio"`
	ExistingAudioIndices []int  `json:"existing_audio_indices,omitempty"`
	Available            bool   `json:"available"`
}

// JobItem represents a single video file in the dubbing queue.
type JobItem struct {
	ID                 string                  `json:"id"`
	SourcePath         string                  `json:"source_path"`
	FileName           string                  `json:"file_name"`
	Status             JobStatus               `json:"status"`
	Stage              string                  `json:"stage"`
	Progress           float64                 `json:"progress"`
	Detail             string                  `json:"detail"`
	Error              string                  `json:"error,omitempty"`
	Config             ItemConfig              `json:"config"`
	AvailableLanguages []AvailableLanguageInfo `json:"available_languages,omitempty"`
	VideoInfo          *media.VideoInfo        `json:"video_info,omitempty"`
	Metrics            *JobMetrics             `json:"metrics,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
}

// Snapshot is the serializable view of the entire queue state returned by GET /api/queue.
type Snapshot struct {
	Status           RunnerStatus `json:"status"`
	ActiveJobID      string       `json:"active_job_id,omitempty"`
	ReuseAudioChunks bool         `json:"reuse_audio_chunks"`
	Items            []JobItem    `json:"items"`
}
