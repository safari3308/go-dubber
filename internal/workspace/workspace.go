package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JobState is persisted inside workdir/jobs/<job-id>/state.json so interrupted
// jobs can resume without re-copying large NAS video files or losing TTS chunks.
type JobState struct {
	JobID           string    `json:"job_id"`
	SourceVideoPath string    `json:"source_video_path"`
	SourceSize      int64     `json:"source_size"`
	SourceModTime   int64     `json:"source_mod_time"`
	LocalVideoFile  string    `json:"local_video_file"`
	LocalCopyReady  bool      `json:"local_copy_ready"`
	Stage           string    `json:"stage"`
	CompletedLangs  []string  `json:"completed_langs"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// HasCompletedLang reports whether lang was already dubbed and muxed into the local asset.
func (s *JobState) HasCompletedLang(lang string) bool {
	target := strings.ToLower(strings.TrimSpace(lang))
	for _, l := range s.CompletedLangs {
		if strings.ToLower(strings.TrimSpace(l)) == target {
			return true
		}
	}
	return false
}

// MarkLangCompleted records lang as completed if not already present.
func (s *JobState) MarkLangCompleted(lang string) {
	if !s.HasCompletedLang(lang) {
		s.CompletedLangs = append(s.CompletedLangs, strings.ToLower(strings.TrimSpace(lang)))
	}
}

// RecoverableJob summarizes an unfinished job discovered in workdir on startup.
type RecoverableJob struct {
	State            JobState       `json:"state"`
	JobDir           string         `json:"job_dir"`
	LocalVideoExists bool           `json:"local_video_exists"`
	LocalVideoValid  bool           `json:"local_video_valid"`
	LocalVideoSize   int64          `json:"local_video_size"`
	ChunkCounts      map[string]int `json:"chunk_counts"`
	TotalChunks      int            `json:"total_chunks"`
}

// Manager manages the persistent local SSD working directory for all dubbing jobs.
type Manager struct {
	rootDir string
	jobsDir string
}

// NewManager initializes the workdir directory layout.
func NewManager(workDir string) (*Manager, error) {
	if workDir == "" {
		workDir = "./workdir"
	}
	absRoot, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	jobsDir := filepath.Join(absRoot, "jobs")
	if err := os.MkdirAll(jobsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workdir jobs folder: %w", err)
	}
	return &Manager{
		rootDir: absRoot,
		jobsDir: jobsDir,
	}, nil
}

func (m *Manager) RootDir() string { return m.rootDir }
func (m *Manager) JobsDir() string { return m.jobsDir }

func (m *Manager) QueueFilePath() string {
	return filepath.Join(m.rootDir, "queue.json")
}

func (m *Manager) JobDir(jobID string) string {
	return filepath.Join(m.jobsDir, jobID)
}

func (m *Manager) StateFilePath(jobID string) string {
	return filepath.Join(m.JobDir(jobID), "state.json")
}

func (m *Manager) SubsDir(jobID string) string {
	return filepath.Join(m.JobDir(jobID), "subs")
}

func (m *Manager) ChunksDir(jobID, lang string) string {
	return filepath.Join(m.JobDir(jobID), "chunks", strings.ToLower(strings.TrimSpace(lang)))
}

func (m *Manager) LocalVideoPath(jobID, sourceVideoPath string) string {
	ext := filepath.Ext(sourceVideoPath)
	if ext == "" {
		ext = ".mkv"
	}
	return filepath.Join(m.JobDir(jobID), "source"+ext)
}

// EnsureJobDirs creates the working directories for jobID.
func (m *Manager) EnsureJobDirs(jobID string) error {
	for _, dir := range []string{m.JobDir(jobID), m.SubsDir(jobID), filepath.Join(m.JobDir(jobID), "chunks")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// SaveState atomically persists state.json for a job.
func (m *Manager) SaveState(state *JobState) error {
	if err := m.EnsureJobDirs(state.JobID); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dst := m.StateFilePath(state.JobID)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// LoadState reads state.json for jobID.
func (m *Manager) LoadState(jobID string) (*JobState, error) {
	data, err := os.ReadFile(m.StateFilePath(jobID))
	if err != nil {
		return nil, err
	}
	var state JobState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// HasValidLocalCopy checks if a verified local SSD copy of sourceVideoPath already exists for jobID,
// allowing the pipeline to skip re-copying large files over the network.
func (m *Manager) HasValidLocalCopy(jobID, sourceVideoPath string) (string, bool) {
	state, err := m.LoadState(jobID)
	if err != nil || !state.LocalCopyReady {
		return "", false
	}

	localPath := m.LocalVideoPath(jobID, sourceVideoPath)
	if state.LocalVideoFile != "" {
		localPath = filepath.Join(m.JobDir(jobID), state.LocalVideoFile)
	}

	localInfo, err := os.Stat(localPath)
	if err != nil || localInfo.IsDir() || localInfo.Size() == 0 {
		return "", false
	}

	// If at least one language has already been remuxed in-place, the local file size may differ
	// from the original NAS file; trust LocalCopyReady as long as the file is non-empty.
	if len(state.CompletedLangs) > 0 {
		return localPath, true
	}

	// Otherwise verify size matches the recorded source size (and current NAS file if reachable).
	if state.SourceSize > 0 && localInfo.Size() != state.SourceSize {
		return "", false
	}
	if srcInfo, err := os.Stat(sourceVideoPath); err == nil {
		if srcInfo.Size() != localInfo.Size() {
			return "", false
		}
	}

	return localPath, true
}

// ClearChunks removes all cached TTS audio chunks for a job language (used when ReuseAudioChunks is false).
func (m *Manager) ClearChunks(jobID, lang string) error {
	return os.RemoveAll(m.ChunksDir(jobID, lang))
}

// CountValidChunks counts non-empty (>44 bytes) WAV chunk files for a job language.
func (m *Manager) CountValidChunks(jobID, lang string) int {
	entries, err := os.ReadDir(m.ChunksDir(jobID, lang))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "chunk_") && strings.HasSuffix(name, ".wav") {
			if info, err := entry.Info(); err == nil && info.Size() > 44 {
				count++
			}
		}
	}
	return count
}

// ScanUnfinished scans workdir/jobs/* and returns all incomplete jobs with their cached artifacts.
func (m *Manager) ScanUnfinished() ([]RecoverableJob, error) {
	entries, err := os.ReadDir(m.jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var recovered []RecoverableJob
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		jobID := entry.Name()
		state, err := m.LoadState(jobID)
		if err != nil {
			continue
		}
		if state.Stage == "completed" {
			continue
		}

		rj := RecoverableJob{
			State:       *state,
			JobDir:      m.JobDir(jobID),
			ChunkCounts: make(map[string]int),
		}

		localPath := m.LocalVideoPath(jobID, state.SourceVideoPath)
		if state.LocalVideoFile != "" {
			localPath = filepath.Join(m.JobDir(jobID), state.LocalVideoFile)
		}
		if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
			rj.LocalVideoExists = true
			rj.LocalVideoSize = info.Size()
			_, valid := m.HasValidLocalCopy(jobID, state.SourceVideoPath)
			rj.LocalVideoValid = valid
		}

		chunksRoot := filepath.Join(m.JobDir(jobID), "chunks")
		if langDirs, err := os.ReadDir(chunksRoot); err == nil {
			for _, ld := range langDirs {
				if ld.IsDir() {
					cnt := m.CountValidChunks(jobID, ld.Name())
					if cnt > 0 {
						rj.ChunkCounts[ld.Name()] = cnt
						rj.TotalChunks += cnt
					}
				}
			}
		}

		recovered = append(recovered, rj)
	}

	return recovered, nil
}

// DiscardJob deletes all temporary files and state for jobID.
func (m *Manager) DiscardJob(jobID string) error {
	return os.RemoveAll(m.JobDir(jobID))
}

// MarkCompletedAndCleanup removes the job's local workdir after NAS replacement succeeds.
func (m *Manager) MarkCompletedAndCleanup(jobID string) error {
	return os.RemoveAll(m.JobDir(jobID))
}
