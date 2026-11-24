package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/subtitle"
	"github.com/safari3308/go-dubber/internal/textutil"
	"github.com/safari3308/go-dubber/internal/tts"
	"github.com/safari3308/go-dubber/internal/workspace"
)

// ExecutorFunc runs a single JobItem to completion or until ctx is cancelled.
type ExecutorFunc func(ctx context.Context, mgr *Manager, job *JobItem, reuseChunks bool) error

// Manager coordinates the live-mutable queue, persistence, and Start/Pause/Stop lifecycle.
type Manager struct {
	mu               sync.Mutex
	items            []*JobItem
	status           RunnerStatus
	activeJobID      string
	activeJobCancel  context.CancelFunc
	activeRemoved    bool
	pauseRequested   bool
	stopRequested    bool
	reuseAudioChunks bool

	getCfg   func() *config.Config
	hw       *hardware.HardwareProfile
	ws       *workspace.Manager
	broker   *sse.Broker
	ttsMon   *tts.HealthMonitor
	executor ExecutorFunc
}

// NewManager creates a Queue Manager and loads any persisted queue.json from workdir.
func NewManager(
	getCfg func() *config.Config,
	hw *hardware.HardwareProfile,
	ws *workspace.Manager,
	broker *sse.Broker,
	ttsMon *tts.HealthMonitor,
) *Manager {
	cfg := getCfg()
	m := &Manager{
		status:           RunnerIdle,
		reuseAudioChunks: cfg.ReuseAudioChunks,
		getCfg:           getCfg,
		hw:               hw,
		ws:               ws,
		broker:           broker,
		ttsMon:           ttsMon,
		executor:         ExecuteJob,
	}
	m.loadPersistedQueue()
	return m
}

// SetExecutor overrides the job executor (useful for testing).
func (m *Manager) SetExecutor(fn ExecutorFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executor = fn
}

func (m *Manager) GetConfig() *config.Config            { return m.getCfg() }
func (m *Manager) Hardware() *hardware.HardwareProfile  { return m.hw }
func (m *Manager) Workspace() *workspace.Manager        { return m.ws }
func (m *Manager) Broker() *sse.Broker                  { return m.broker }
func (m *Manager) TTSMonitor() *tts.HealthMonitor       { return m.ttsMon }

// Snapshot returns a thread-safe copy of the current queue state.
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := make([]JobItem, len(m.items))
	for i, it := range m.items {
		items[i] = *it
	}
	return Snapshot{
		Status:           m.status,
		ActiveJobID:      m.activeJobID,
		ReuseAudioChunks: m.reuseAudioChunks,
		Items:            items,
	}
}

// SetReuseAudioChunks updates the toggle for reusing cached TTS audio chunks.
func (m *Manager) SetReuseAudioChunks(reuse bool) {
	m.mu.Lock()
	m.reuseAudioChunks = reuse
	m.mu.Unlock()
	m.publishQueueState()
}

// AddPaths accepts one or more file or folder paths and appends discovered videos to the queue.
// Safe to call while the queue is actively running.
func (m *Manager) AddPaths(paths []string, overrideCfg *ItemConfig) ([]JobItem, error) {
	var videoPaths []string
	for _, raw := range paths {
		p := textutil.SanitizePath(raw)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			discovered, err := textutil.DiscoverVideoFiles(p)
			if err == nil {
				videoPaths = append(videoPaths, discovered...)
			}
		} else if textutil.IsSupportedVideoExtension(filepath.Ext(p)) {
			videoPaths = append(videoPaths, p)
		}
	}

	if len(videoPaths) == 0 {
		return nil, fmt.Errorf("no supported video files found in the provided path(s)")
	}

	globalCfg := m.getCfg()
	now := time.Now().UTC()
	var added []JobItem

	for _, vp := range videoPaths {
		absPath, err := filepath.Abs(vp)
		if err == nil {
			vp = absPath
		}

		itemCfg := ItemConfigFromGlobal(globalCfg)
		if overrideCfg != nil {
			itemCfg = *overrideCfg
			if len(itemCfg.DubLanguages) == 0 {
				itemCfg.DubLanguages = append([]string(nil), globalCfg.DubLanguages...)
			}
			if itemCfg.TTSSpeed <= 0 {
				itemCfg.TTSSpeed = globalCfg.TTSSpeed
			}
			if itemCfg.Workers <= 0 {
				itemCfg.Workers = globalCfg.Workers
			}
		}

		// Fast metadata probe & automatic dual-subtitle resolution
		var vInfo *media.VideoInfo
		if info, err := media.InspectVideo(vp); err == nil {
			vInfo = info
		}

		if len(itemCfg.LanguageSpecs) == 0 {
			var embedded []subtitle.EmbeddedSubInfo
			if vInfo != nil {
				embedded = vInfo.EmbeddedSubStreams
			}
			effectiveCfg := *globalCfg
			effectiveCfg.SkipSubSync = itemCfg.SkipSubSync
			effectiveCfg.DropSongSubtitles = itemCfg.DropSongSubtitles
			for _, lang := range itemCfg.DubLanguages {
				if spec := subtitle.ResolveDualSubtitle(vp, embedded, &effectiveCfg, lang); spec != nil {
					itemCfg.LanguageSpecs = append(itemCfg.LanguageSpecs, *spec)
				}
			}
		}

		item := &JobItem{
			ID:         newJobID(),
			SourcePath: vp,
			FileName:   filepath.Base(vp),
			Status:     JobPending,
			Stage:      "pending",
			Config:     itemCfg,
			VideoInfo:  vInfo,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		m.mu.Lock()
		m.items = append(m.items, item)
		m.savePersistedQueueLocked()
		m.mu.Unlock()

		added = append(added, *item)
	}

	m.publishListChanged("added", added)
	return added, nil
}

// ResumeRecoverable adds an unfinished job from workdir back into the queue with its original JobID
// so its verified local SSD video copy and cached audio chunks are automatically reused.
func (m *Manager) ResumeRecoverable(jobID string) (*JobItem, error) {
	state, err := m.ws.LoadState(jobID)
	if err != nil {
		return nil, fmt.Errorf("recoverable job %s not found: %w", jobID, err)
	}

	m.mu.Lock()
	for _, existing := range m.items {
		if existing.ID == jobID {
			if existing.Status == JobPaused || existing.Status == JobFailed || existing.Status == JobCancelled {
				existing.Status = JobPending
				existing.Error = ""
				existing.UpdatedAt = time.Now().UTC()
				m.savePersistedQueueLocked()
			}
			cp := *existing
			m.mu.Unlock()
			m.publishJobUpdated(cp)
			return &cp, nil
		}
	}
	m.mu.Unlock()

	globalCfg := m.getCfg()
	itemCfg := ItemConfigFromGlobal(globalCfg)
	var vInfo *media.VideoInfo
	if info, err := media.InspectVideo(state.SourceVideoPath); err == nil {
		vInfo = info
	}
	var embedded []subtitle.EmbeddedSubInfo
	if vInfo != nil {
		embedded = vInfo.EmbeddedSubStreams
	}
	for _, lang := range itemCfg.DubLanguages {
		if spec := subtitle.ResolveDualSubtitle(state.SourceVideoPath, embedded, globalCfg, lang); spec != nil {
			itemCfg.LanguageSpecs = append(itemCfg.LanguageSpecs, *spec)
		}
	}

	now := time.Now().UTC()
	item := &JobItem{
		ID:         jobID,
		SourcePath: state.SourceVideoPath,
		FileName:   filepath.Base(state.SourceVideoPath),
		Status:     JobPending,
		Stage:      "recovered",
		Detail:     "Recovered from unfinished workdir",
		Config:     itemCfg,
		VideoInfo:  vInfo,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	m.mu.Lock()
	m.items = append([]*JobItem{item}, m.items...)
	m.savePersistedQueueLocked()
	m.mu.Unlock()

	m.publishListChanged("recovered", []JobItem{*item})
	cp := *item
	return &cp, nil
}

// UpdateItemConfig modifies the dubbing configuration (including dual-subtitles) of a non-running item.
func (m *Manager) UpdateItemConfig(id string, cfg ItemConfig) (*JobItem, error) {
	// Sanitize any user-pasted external subtitle paths (e.g. macOS quoted pathnames)
	for i := range cfg.LanguageSpecs {
		if cfg.LanguageSpecs[i].TTSSub.Type == "external" && cfg.LanguageSpecs[i].TTSSub.Path != "" {
			cfg.LanguageSpecs[i].TTSSub.Path = textutil.SanitizePath(cfg.LanguageSpecs[i].TTSSub.Path)
		}
		if cfg.LanguageSpecs[i].EmbedSub != nil && cfg.LanguageSpecs[i].EmbedSub.Type == "external" && cfg.LanguageSpecs[i].EmbedSub.Path != "" {
			cfg.LanguageSpecs[i].EmbedSub.Path = textutil.SanitizePath(cfg.LanguageSpecs[i].EmbedSub.Path)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, it := range m.items {
		if it.ID == id {
			if m.activeJobID == id && m.status == RunnerRunning {
				return nil, fmt.Errorf("cannot edit config of currently running job")
			}
			it.Config = cfg
			it.UpdatedAt = time.Now().UTC()
			if it.Status == JobFailed || it.Status == JobCancelled {
				it.Status = JobPending
				it.Error = ""
			}
			m.savePersistedQueueLocked()
			cp := *it
			go m.publishJobUpdated(cp)
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("job %s not found", id)
}

// RemoveItem removes an item from the queue. If it is currently executing, cancels it cleanly.
func (m *Manager) RemoveItem(id string) error {
	m.mu.Lock()
	idx := -1
	for i, it := range m.items {
		if it.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("job %s not found", id)
	}

	isActive := m.activeJobID == id && m.activeJobCancel != nil
	if isActive {
		m.activeRemoved = true
		m.activeJobCancel()
	}

	m.items = append(m.items[:idx], m.items[idx+1:]...)
	m.savePersistedQueueLocked()
	m.mu.Unlock()

	m.publishListChanged("removed", nil)
	return nil
}

// Reorder rearranges the queue items according to ids. Any items not in ids are preserved at the end.
func (m *Manager) Reorder(ids []string) error {
	m.mu.Lock()
	byID := make(map[string]*JobItem, len(m.items))
	for _, it := range m.items {
		byID[it.ID] = it
	}
	var reordered []*JobItem
	seen := make(map[string]bool)
	for _, id := range ids {
		if it, ok := byID[id]; ok && !seen[id] {
			reordered = append(reordered, it)
			seen[id] = true
		}
	}
	for _, it := range m.items {
		if !seen[it.ID] {
			reordered = append(reordered, it)
		}
	}
	m.items = reordered
	m.savePersistedQueueLocked()
	m.mu.Unlock()

	m.publishListChanged("reordered", nil)
	return nil
}

// Start launches or resumes queue processing.
func (m *Manager) Start(reuseAudioChunks *bool) error {
	m.mu.Lock()
	if reuseAudioChunks != nil {
		m.reuseAudioChunks = *reuseAudioChunks
	}
	if m.status == RunnerRunning {
		m.mu.Unlock()
		return nil
	}
	if m.status == RunnerPausing || m.status == RunnerStopping {
		m.mu.Unlock()
		return fmt.Errorf("queue is currently transitioning (%s), please wait", m.status)
	}

	// Re-queue any paused items so they resume cleanly
	for _, it := range m.items {
		if it.Status == JobPaused {
			it.Status = JobPending
			it.UpdatedAt = time.Now().UTC()
		}
	}

	m.status = RunnerRunning
	m.pauseRequested = false
	m.stopRequested = false
	m.savePersistedQueueLocked()
	m.mu.Unlock()

	m.publishQueueState()
	go m.runLoop()
	return nil
}

// Pause gracefully pauses the active job (preserving local copy & audio chunks) and halts the queue.
func (m *Manager) Pause() error {
	m.mu.Lock()
	if m.status != RunnerRunning {
		m.mu.Unlock()
		return nil
	}
	m.status = RunnerPausing
	m.pauseRequested = true
	if m.activeJobCancel != nil {
		m.activeJobCancel()
	}
	m.mu.Unlock()

	m.publishQueueState()
	return nil
}

// Stop stops the active job immediately (preserving workdir for recovery) and returns queue to idle.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.status == RunnerIdle {
		m.mu.Unlock()
		return nil
	}
	if m.status == RunnerPaused {
		m.status = RunnerIdle
		m.pauseRequested = false
		m.stopRequested = false
		m.mu.Unlock()
		m.publishQueueState()
		return nil
	}
	m.status = RunnerStopping
	m.stopRequested = true
	if m.activeJobCancel != nil {
		m.activeJobCancel()
	}
	m.mu.Unlock()

	m.publishQueueState()
	return nil
}

// UpdateJobProgress updates a job's status/stage/progress and broadcasts it over SSE.
func (m *Manager) UpdateJobProgress(jobID string, status JobStatus, stage string, progress float64, detail string) {
	m.mu.Lock()
	var updated *JobItem
	for _, it := range m.items {
		if it.ID == jobID {
			if status != "" {
				it.Status = status
			}
			if stage != "" {
				it.Stage = stage
			}
			if progress >= 0 {
				it.Progress = progress
			}
			if detail != "" {
				it.Detail = detail
			}
			it.UpdatedAt = time.Now().UTC()
			cp := *it
			updated = &cp
			break
		}
	}
	m.mu.Unlock()

	if updated != nil {
		m.publishJobUpdated(*updated)
	}
}

func (m *Manager) runLoop() {
	for {
		m.mu.Lock()
		if m.pauseRequested {
			m.status = RunnerPaused
			m.activeJobID = ""
			m.activeJobCancel = nil
			m.mu.Unlock()
			m.publishQueueState()
			return
		}
		if m.stopRequested {
			m.status = RunnerIdle
			m.activeJobID = ""
			m.activeJobCancel = nil
			m.mu.Unlock()
			m.publishQueueState()
			return
		}

		// Pick next pending job
		var nextJob *JobItem
		for _, it := range m.items {
			if it.Status == JobPending {
				nextJob = it
				break
			}
		}

		if nextJob == nil {
			m.status = RunnerIdle
			m.activeJobID = ""
			m.activeJobCancel = nil
			m.mu.Unlock()
			m.publishQueueState()
			if m.broker != nil {
				m.broker.Notify("success", "Queue Completed", "All queued videos have been processed.")
			}
			return
		}

		jobCtx, cancel := context.WithCancel(context.Background())
		m.activeJobID = nextJob.ID
		m.activeJobCancel = cancel
		m.activeRemoved = false
		reuseChunks := m.reuseAudioChunks
		execFn := m.executor
		jobCopy := *nextJob
		m.mu.Unlock()

		m.publishQueueState()
		err := execFn(jobCtx, m, &jobCopy, reuseChunks)
		cancel()

		m.mu.Lock()
		removed := m.activeRemoved
		paused := m.pauseRequested
		stopped := m.stopRequested
		m.activeJobID = ""
		m.activeJobCancel = nil
		m.activeRemoved = false

		if !removed {
			for _, it := range m.items {
				if it.ID == jobCopy.ID {
					it.Metrics = jobCopy.Metrics
					it.UpdatedAt = time.Now().UTC()
					switch {
					case err == nil:
						it.Status = JobCompleted
						it.Stage = "completed"
						it.Progress = 100
						it.Error = ""
					case paused || stopped || errors.Is(err, context.Canceled):
						it.Status = JobPaused
						it.Stage = "paused"
						it.Detail = "Paused — local video & audio chunks preserved"
						it.Error = ""
					default:
						it.Status = JobFailed
						it.Stage = "failed"
						it.Error = err.Error()
					}
					cp := *it
					go m.publishJobUpdated(cp)
					break
				}
			}
		}
		m.savePersistedQueueLocked()

		if paused {
			m.status = RunnerPaused
			m.mu.Unlock()
			m.publishQueueState()
			return
		}
		if stopped {
			m.status = RunnerIdle
			m.mu.Unlock()
			m.publishQueueState()
			return
		}
		m.mu.Unlock()
	}
}

func (m *Manager) loadPersistedQueue() {
	if m.ws == nil {
		return
	}
	data, err := os.ReadFile(m.ws.QueueFilePath())
	if err != nil {
		return
	}
	var items []*JobItem
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}
	// Any jobs that were mid-flight when the server died are marked paused so they can be resumed.
	for _, it := range items {
		switch it.Status {
		case JobDownloading, JobSyncing, JobSynthesizing, JobRemuxing, JobUploading:
			it.Status = JobPaused
			it.Stage = "paused"
			it.Detail = "Interrupted — ready to resume"
		}
	}
	m.items = items
}

func (m *Manager) savePersistedQueueLocked() {
	if m.ws == nil {
		return
	}
	data, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return
	}
	dst := m.ws.QueueFilePath()
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err == nil {
		_ = os.Rename(tmp, dst)
	}
}

func (m *Manager) publishQueueState() {
	if m.broker == nil {
		return
	}
	m.mu.Lock()
	payload := map[string]any{
		"status":             m.status,
		"active_job_id":      m.activeJobID,
		"reuse_audio_chunks": m.reuseAudioChunks,
	}
	m.mu.Unlock()
	m.broker.Publish("queue_state", payload)
}

func (m *Manager) publishJobUpdated(job JobItem) {
	if m.broker == nil {
		return
	}
	m.broker.Publish("job_updated", job)
}

func (m *Manager) publishListChanged(action string, added []JobItem) {
	if m.broker == nil {
		return
	}
	m.broker.Publish("job_list_changed", map[string]any{
		"action": action,
		"items":  added,
	})
}

func newJobID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("job-%d-%s", time.Now().Unix(), hex.EncodeToString(b))
}
