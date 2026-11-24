package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/queue"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/subtitle"
	"github.com/safari3308/go-dubber/internal/textutil"
	"github.com/safari3308/go-dubber/internal/tts"
	"github.com/safari3308/go-dubber/internal/workspace"
)

// Server wires together the REST API, SSE stream, and embedded React SPA.
type Server struct {
	configPath string
	cfgMu      sync.RWMutex
	cfg        *config.Config

	hw      *hardware.HardwareProfile
	ws      *workspace.Manager
	queue   *queue.Manager
	broker  *sse.Broker
	ttsMon  *tts.HealthMonitor
	webFS   fs.FS
	devMode bool
}

// New creates a configured HTTP Server.
func New(
	configPath string,
	cfg *config.Config,
	hw *hardware.HardwareProfile,
	ws *workspace.Manager,
	qm *queue.Manager,
	broker *sse.Broker,
	ttsMon *tts.HealthMonitor,
	webFS fs.FS,
	devMode bool,
) *Server {
	return &Server{
		configPath: configPath,
		cfg:        cfg,
		hw:         hw,
		ws:         ws,
		queue:      qm,
		broker:     broker,
		ttsMon:     ttsMon,
		webFS:      webFS,
		devMode:    devMode,
	}
}

// GetConfig returns a thread-safe copy of the current global config.
func (s *Server) GetConfig() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	cp := *s.cfg
	return &cp
}

// Handler builds the HTTP handler with all API routes and SPA static hosting.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// SSE stream
	mux.Handle("GET /api/events", s.broker)

	// System & Settings
	mux.HandleFunc("GET /api/system/hardware", s.handleGetHardware)
	mux.HandleFunc("GET /api/system/tts-status", s.handleGetTTSStatus)
	mux.HandleFunc("POST /api/system/tts-check", s.handleCheckTTSNow)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)

	// Server Filesystem Browser & Media Inspector
	mux.HandleFunc("GET /api/fs/browse", s.handleFSBrowse)
	mux.HandleFunc("POST /api/fs/inspect", s.handleFSInspect)

	// Queue Management & Lifecycle Controls
	mux.HandleFunc("GET /api/queue", s.handleGetQueue)
	mux.HandleFunc("POST /api/queue/items", s.handleAddQueueItems)
	mux.HandleFunc("PATCH /api/queue/items/{id}", s.handleUpdateQueueItem)
	mux.HandleFunc("DELETE /api/queue/items/{id}", s.handleDeleteQueueItem)
	mux.HandleFunc("POST /api/queue/reorder", s.handleReorderQueue)
	mux.HandleFunc("POST /api/queue/start", s.handleQueueStart)
	mux.HandleFunc("POST /api/queue/pause", s.handleQueuePause)
	mux.HandleFunc("POST /api/queue/stop", s.handleQueueStop)
	mux.HandleFunc("PATCH /api/queue/options", s.handleQueueOptions)

	// Unfinished Work Recovery
	mux.HandleFunc("GET /api/recovery", s.handleGetRecovery)
	mux.HandleFunc("POST /api/recovery/{id}/resume", s.handleResumeRecovery)
	mux.HandleFunc("DELETE /api/recovery/{id}", s.handleDiscardRecovery)

	// Embedded React SPA
	if s.webFS != nil {
		mux.Handle("/", s.spaHandler())
	}

	if s.devMode {
		return corsMiddleware(mux)
	}
	return mux
}

func (s *Server) handleGetHardware(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hw)
}

func (s *Server) handleGetTTSStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ttsMon.Status())
}

func (s *Server) handleCheckTTSNow(w http.ResponseWriter, r *http.Request) {
	st := s.ttsMon.CheckNow(r.Context())
	s.broker.Publish("tts_status", st)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.GetConfig())
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var next config.Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}
	next.Normalize()

	if err := config.SaveConfig(s.configPath, &next); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	s.cfgMu.Lock()
	s.cfg = &next
	s.cfgMu.Unlock()

	// Immediately re-check TTS health in case api_url or api_token changed
	go s.ttsMon.CheckNow(r.Context())
	s.broker.Notify("info", "Settings Saved", "Global configuration updated.")
	writeJSON(w, http.StatusOK, &next)
}

type fsEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	IsVideo    bool   `json:"is_video"`
	IsSubtitle bool   `json:"is_subtitle"`
}

func (s *Server) handleFSBrowse(w http.ResponseWriter, r *http.Request) {
	targetPath := textutil.SanitizePath(r.URL.Query().Get("path"))
	if targetPath == "" {
		cfg := s.GetConfig()
		if cfg.NasDir != "" {
			if info, err := os.Stat(cfg.NasDir); err == nil && info.IsDir() {
				targetPath = cfg.NasDir
			}
		}
	}
	if targetPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			targetPath = home
		} else {
			targetPath = "/"
		}
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// If user pasted a direct file path into the browser bar, navigate to its parent directory.
	if info, statErr := os.Stat(absPath); statErr == nil && !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read directory: "+err.Error())
		return
	}

	var entries []fsEntry
	for _, de := range dirEntries {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		fullPath := filepath.Join(absPath, name)
		isDir := de.IsDir()
		var size int64
		if info, err := de.Info(); err == nil {
			size = info.Size()
		}
		ext := strings.ToLower(filepath.Ext(name))
		isVid := !isDir && textutil.IsSupportedVideoExtension(ext)
		isSub := !isDir && subtitle.IsSupportedSubtitleExtension(ext)

		if isDir || isVid || isSub {
			entries = append(entries, fsEntry{
				Name:       name,
				Path:       fullPath,
				IsDir:      isDir,
				Size:       size,
				IsVideo:    isVid,
				IsSubtitle: isSub,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return textutil.NaturalLess(entries[i].Name, entries[j].Name)
	})

	parent := filepath.Dir(absPath)
	if parent == absPath {
		parent = ""
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    absPath,
		"parent":  parent,
		"entries": entries,
	})
}

func (s *Server) handleFSInspect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string   `json:"path"`
		Languages []string `json:"languages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	req.Path = textutil.SanitizePath(req.Path)

	info, err := media.InspectVideo(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to inspect video: "+err.Error())
		return
	}

	cfg := s.GetConfig()
	langs := req.Languages
	if len(langs) == 0 {
		langs = cfg.DubLanguages
	}

	candidatesByLang := make(map[string][]subtitle.CandidateSubtitle)
	resolvedSpecs := make([]subtitle.LanguageDubSpec, 0, len(langs))
	for _, lang := range langs {
		candidatesByLang[lang] = subtitle.DiscoverExternalSubtitlesForLanguage(req.Path, lang, cfg)
		if spec := subtitle.ResolveDualSubtitle(req.Path, info.EmbeddedSubStreams, cfg, lang); spec != nil {
			resolvedSpecs = append(resolvedSpecs, *spec)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"video_info":         info,
		"external_subtitles": candidatesByLang,
		"resolved_specs":     resolvedSpecs,
	})
}

func (s *Server) handleGetQueue(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleAddQueueItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths  []string          `json:"paths"`
		Config *queue.ItemConfig `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths array is required")
		return
	}

	added, err := s.queue.AddPaths(req.Paths, req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"added": added,
		"count": len(added),
	})
}

func (s *Server) handleUpdateQueueItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var cfg queue.ItemConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid item config: "+err.Error())
		return
	}
	updated, err := s.queue.UpdateItemConfig(id, cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteQueueItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.queue.RemoveItem(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"removed": id})
}

func (s *Server) handleReorderQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	_ = s.queue.Reorder(req.IDs)
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleQueueStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReuseAudioChunks *bool `json:"reuse_audio_chunks,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.queue.Start(req.ReuseAudioChunks); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Pause(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleQueueStop(w http.ResponseWriter, r *http.Request) {
	if err := s.queue.Stop(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleQueueOptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReuseAudioChunks *bool `json:"reuse_audio_chunks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if req.ReuseAudioChunks != nil {
		s.queue.SetReuseAudioChunks(*req.ReuseAudioChunks)
	}
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleGetRecovery(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.ws.ScanUnfinished()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []workspace.RecoverableJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

func (s *Server) handleResumeRecovery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.queue.ResumeRecoverable(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDiscardRecovery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.ws.DiscardJob(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.broker.Notify("info", "Workdir Cleaned", "Discarded unfinished temporary files for "+id)
	writeJSON(w, http.StatusOK, map[string]string{"discarded": id})
}

func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.webFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}
		if f, err := s.webFS.Open(cleanPath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fallback to index.html for client-side routing
		indexBytes, err := fs.ReadFile(s.webFS, "index.html")
		if err != nil {
			http.Error(w, "UI build not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexBytes)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
