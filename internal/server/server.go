package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/gemini"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/queue"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/store"
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
	db      *store.DB
	queue   *queue.Manager
	broker  *sse.Broker
	ttsMon  *tts.HealthMonitor
	webFS   fs.FS
	devMode bool
}

// SetStore attaches the SQLite store for persisted logs and unread notifications.
func (s *Server) SetStore(db *store.DB) {
	s.db = db
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
	if len(s.cfg.TTSVoices) > 0 {
		cp.TTSVoices = make(map[string]string, len(s.cfg.TTSVoices))
		for k, v := range s.cfg.TTSVoices {
			cp.TTSVoices[k] = v
		}
	}
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
	mux.HandleFunc("GET /api/system/tts-voices", s.handleGetTTSVoices)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)

	// Server Filesystem Browser & Media Inspector
	mux.HandleFunc("GET /api/fs/browse", s.handleFSBrowse)
	mux.HandleFunc("POST /api/fs/inspect", s.handleFSInspect)
	mux.HandleFunc("POST /api/fs/extract-subtitle", s.handleFSExtractSubtitle)

	// Queue Management & Lifecycle Controls
	mux.HandleFunc("GET /api/queue", s.handleGetQueue)
	mux.HandleFunc("POST /api/queue/items", s.handleAddQueueItems)
	mux.HandleFunc("PATCH /api/queue/items/{id}", s.handleUpdateQueueItem)
	mux.HandleFunc("POST /api/queue/items/{id}/subtitles", s.handleUploadQueueSubtitle)
	mux.HandleFunc("POST /api/queue/items/{id}/gemini-subtitle", s.handleGeminiQueueSubtitle)
	mux.HandleFunc("POST /api/queue/items/{id}/start", s.handleQueueStartSingle)
	mux.HandleFunc("DELETE /api/queue/items/{id}", s.handleDeleteQueueItem)
	mux.HandleFunc("POST /api/queue/clear-completed", s.handleQueueClearCompleted)
	mux.HandleFunc("POST /api/queue/reorder", s.handleReorderQueue)
	mux.HandleFunc("POST /api/queue/start", s.handleQueueStart)
	mux.HandleFunc("POST /api/queue/pause", s.handleQueuePause)
	mux.HandleFunc("POST /api/queue/stop", s.handleQueueStop)
	mux.HandleFunc("PATCH /api/queue/options", s.handleQueueOptions)

	// Unfinished Work Recovery
	mux.HandleFunc("GET /api/recovery", s.handleGetRecovery)
	mux.HandleFunc("POST /api/recovery/{id}/resume", s.handleResumeRecovery)
	mux.HandleFunc("DELETE /api/recovery/{id}", s.handleDiscardRecovery)

	// Persisted Logs & Missed Notifications (SQLite)
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("DELETE /api/logs", s.handleClearLogs)
	mux.HandleFunc("GET /api/notifications/unread", s.handleGetUnreadNotifications)
	mux.HandleFunc("POST /api/notifications/read", s.handleMarkNotificationsRead)

	// In-Browser KokoroJS English TTS Fallback Bridge
	mux.HandleFunc("GET /api/browser-tts/status", s.handleGetBrowserTTSStatus)
	mux.HandleFunc("POST /api/browser-tts/status", s.handleSetBrowserTTSStatus)
	mux.HandleFunc("POST /api/browser-tts/result/{id}", s.handleBrowserTTSResult)

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

func (s *Server) handleGetTTSVoices(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	voices, err := tts.ListVoices(r.Context(), s.GetConfig(), lang)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, voices)
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

	if s.db != nil {
		raw, err := json.Marshal(&next)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode settings: "+err.Error())
			return
		}
		if err := s.db.SaveSettingsJSON(string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save settings to sqlite: "+err.Error())
			return
		}
	} else if s.configPath != "" {
		if err := config.SaveConfig(s.configPath, &next); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
			return
		}
	}

	s.cfgMu.Lock()
	s.cfg = &next
	s.cfgMu.Unlock()

	// Immediately re-check TTS health in case api_url, grpc_url, or api_token changed
	tts.ResetGRPCFallbackCache()
	go s.ttsMon.CheckNow(context.Background())
	s.broker.Notify("info", "Settings Saved", "Global settings updated.")
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
	langs := queue.SupportedTTSLanguages
	if len(req.Languages) > 0 {
		// Always ensure en and vi are included for dual-subtitle discovery
		seen := make(map[string]bool)
		var merged []string
		for _, l := range append(append([]string(nil), req.Languages...), queue.SupportedTTSLanguages...) {
			norm := strings.ToLower(strings.TrimSpace(l))
			if norm != "" && !seen[norm] {
				seen[norm] = true
				merged = append(merged, norm)
			}
		}
		langs = merged
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

func (s *Server) handleFSExtractSubtitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		SubIndex int    `json:"sub_index"`
		Format   string `json:"format"` // "original" or "srt"
		AsJSON   bool   `json:"as_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	videoPath := textutil.SanitizePath(req.Path)

	info, err := media.InspectVideo(videoPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to inspect video: "+err.Error())
		return
	}

	var matched *media.EmbeddedSubInfo
	for i := range info.EmbeddedSubStreams {
		if info.EmbeddedSubStreams[i].SubIndex == req.SubIndex {
			matched = &info.EmbeddedSubStreams[i]
			break
		}
	}
	if matched == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("embedded subtitle track #%d not found", req.SubIndex))
		return
	}
	if matched.IsBitmap {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("subtitle track #%d is a bitmap format (%s) and cannot be extracted as text", req.SubIndex, matched.CodecName))
		return
	}

	format := strings.ToLower(strings.TrimSpace(req.Format))
	ext := ".srt"
	useCopy := false
	if format == "original" {
		switch strings.ToLower(matched.CodecName) {
		case "ass":
			ext = ".ass"
			useCopy = true
		case "ssa":
			ext = ".ssa"
			useCopy = true
		case "webvtt", "vtt":
			ext = ".vtt"
			useCopy = true
		case "subrip", "srt":
			ext = ".srt"
			useCopy = true
		}
	}

	tmpDir, err := os.MkdirTemp("", "godubber-extract-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	langTag := matched.Language
	if langTag == "" || langTag == "unknown" {
		langTag = fmt.Sprintf("track%d", matched.SubIndex)
	}
	outName := fmt.Sprintf("%s.%s%s", base, langTag, ext)
	outPath := filepath.Join(tmpDir, "extracted"+ext)

	if useCopy {
		err = media.ExtractEmbeddedSubtitleCopyContext(r.Context(), videoPath, outPath, req.SubIndex)
	} else {
		err = media.ExtractEmbeddedSubtitleContext(r.Context(), videoPath, outPath, req.SubIndex)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if req.AsJSON {
		writeJSON(w, http.StatusOK, map[string]any{
			"filename": outName,
			"format":   strings.TrimPrefix(ext, "."),
			"content":  string(data),
		})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, outName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleUploadQueueSubtitle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart upload: "+err.Error())
		return
	}

	lang := strings.ToLower(strings.TrimSpace(r.FormValue("language")))
	role := strings.ToLower(strings.TrimSpace(r.FormValue("role")))
	if lang == "" {
		lang = "vi"
	}
	if role == "" {
		role = "both"
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "subtitle file is required")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !subtitle.IsSupportedSubtitleExtension(ext) {
		writeError(w, http.StatusBadRequest, "unsupported subtitle file extension: "+ext)
		return
	}

	if err := s.ws.EnsureJobDirs(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare job subs dir: "+err.Error())
		return
	}

	safeName := filepath.Base(header.Filename)
	destPath := filepath.Join(s.ws.SubsDir(id), fmt.Sprintf("uploaded_%s_%s", lang, safeName))
	out, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save subtitle: "+err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeError(w, http.StatusInternalServerError, "failed to write subtitle: "+err.Error())
		return
	}
	out.Close()

	updated, err := s.queue.AttachUploadedSubtitle(id, lang, role, destPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.broker.Notify("success", "Subtitle Attached", fmt.Sprintf("Attached %s (%s) to %s", safeName, strings.ToUpper(lang), updated.FileName))
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleGeminiQueueSubtitle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := s.GetConfig()
	if !cfg.GeminiEnabled {
		writeError(w, http.StatusBadRequest, "Google AI (Gemini) is disabled in Settings")
		return
	}
	if strings.TrimSpace(cfg.GeminiAPIKey) == "" {
		writeError(w, http.StatusBadRequest, "Gemini API key is not configured in Settings")
		return
	}

	var req struct {
		SourceType    string `json:"source_type"`    // "embedded" or "external"
		EmbeddedIndex int    `json:"embedded_index"` // when source_type == "embedded"
		ExternalPath  string `json:"external_path"`  // when source_type == "external"
		TargetLang    string `json:"target_lang"`    // "en" or "vi"
		Translate     bool   `json:"translate"`
		CleanForTTS   bool   `json:"clean_for_tts"`
		Style         string `json:"style"` // "standard" or "donghua"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}

	lang := strings.ToLower(strings.TrimSpace(req.TargetLang))
	if lang != "en" && lang != "vi" {
		writeError(w, http.StatusBadRequest, "target_lang must be 'en' or 'vi'")
		return
	}
	if !req.Translate && !req.CleanForTTS {
		req.CleanForTTS = true
	}

	snap := s.queue.Snapshot()
	var targetJob *queue.JobItem
	for i := range snap.Items {
		if snap.Items[i].ID == id {
			targetJob = &snap.Items[i]
			break
		}
	}
	if targetJob == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	if err := s.ws.EnsureJobDirs(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	subsDir := s.ws.SubsDir(id)
	style := strings.TrimSpace(req.Style)
	if style == "" {
		style = targetJob.Config.GeminiStyle
	}
	if style == "" {
		style = cfg.GeminiStyle
	}
	gClient := gemini.NewClient(cfg.GeminiAPIKey)
	gOpts := gemini.ProcessOptions{
		TargetLang:  lang,
		Translate:   req.Translate,
		CleanForTTS: req.CleanForTTS,
		Style:       style,
	}

	// Detect if source subtitle is ASS/SSA so we can preserve styles, signs & descriptions in a dual output!
	sourceIsASS := false
	if req.SourceType == "embedded" && targetJob.VideoInfo != nil {
		for _, sub := range targetJob.VideoInfo.EmbeddedSubStreams {
			if sub.SubIndex == req.EmbeddedIndex {
				c := strings.ToLower(sub.CodecName)
				if c == "ass" || c == "ssa" {
					sourceIsASS = true
				}
				break
			}
		}
	} else if req.SourceType != "embedded" {
		ext := strings.ToLower(filepath.Ext(textutil.SanitizePath(req.ExternalPath)))
		if ext == ".ass" || ext == ".ssa" {
			sourceIsASS = true
		}
	}

	outSrt := filepath.Join(subsDir, fmt.Sprintf("gemini_%s.srt", lang))
	outAss := filepath.Join(subsDir, fmt.Sprintf("gemini_%s.ass", lang))

	var totalCues, keptCues int
	var generatedASS bool

	if sourceIsASS {
		rawASS := filepath.Join(subsDir, fmt.Sprintf("gemini_src_%s.ass", lang))
		if req.SourceType == "embedded" {
			if err := media.ExtractEmbeddedSubtitleCopyContext(r.Context(), targetJob.SourcePath, rawASS, req.EmbeddedIndex); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			defer os.Remove(rawASS)
		} else {
			rawASS = textutil.SanitizePath(req.ExternalPath)
		}

		res, err := gClient.ProcessASSFile(r.Context(), rawASS, outAss, outSrt, gOpts, nil)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		totalCues = res.TotalEvents
		keptCues = res.DialogueCount
		generatedASS = true
	} else {
		rawSrt := filepath.Join(subsDir, fmt.Sprintf("gemini_src_%s.srt", lang))
		if req.SourceType == "embedded" {
			if err := media.ExtractEmbeddedSubtitleContext(r.Context(), targetJob.SourcePath, rawSrt, req.EmbeddedIndex); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		} else {
			extPath := textutil.SanitizePath(req.ExternalPath)
			if extPath == "" {
				writeError(w, http.StatusBadRequest, "external_path is required")
				return
			}
			if strings.ToLower(filepath.Ext(extPath)) == ".srt" {
				if err := textutil.CopyFileContext(r.Context(), extPath, rawSrt, nil); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			} else {
				if err := media.ConvertToSRTContext(r.Context(), extPath, rawSrt); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}
		var err error
		totalCues, keptCues, err = gClient.ProcessSRTFile(r.Context(), rawSrt, outSrt, gOpts, nil)
		_ = os.Remove(rawSrt)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}

	if generatedASS {
		// Dual output: clean dialogue-only SRT for TTS + styled translated ASS (with signs & descriptions) for Embed!
		if _, err := s.queue.AttachUploadedSubtitle(id, lang, "tts", outSrt); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, err := s.queue.AttachUploadedSubtitle(id, lang, "embed", outAss)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.broker.Notify("success", "Google AI Dual Subtitles Ready", fmt.Sprintf("Generated clean TTS SRT (%d dialogue) + styled ASS with signs/descriptions (%s).", keptCues, strings.ToUpper(lang)))
		writeJSON(w, http.StatusOK, map[string]any{
			"job":        updated,
			"total_cues": totalCues,
			"kept_cues":  keptCues,
			"path":       outSrt,
			"ass_path":   outAss,
		})
		return
	}

	// Non-ASS source: attach for both TTS & Embed if no native/embed sub exists for lang
	attachRole := "tts"
	hasEmbedSub := false
	for _, sp := range targetJob.Config.LanguageSpecs {
		if strings.EqualFold(sp.Language, lang) && sp.EmbedSub != nil && sp.EmbedSub.IsValid() {
			hasEmbedSub = true
			break
		}
	}
	hasNativeEmbedded := false
	if targetJob.VideoInfo != nil {
		for _, sub := range targetJob.VideoInfo.EmbeddedSubStreams {
			if subtitle.IsSubMatchingLanguage(sub.Language, sub.Title, lang) {
				hasNativeEmbedded = true
				break
			}
		}
	}
	if req.Translate && !hasEmbedSub && !hasNativeEmbedded {
		attachRole = "both"
	}

	updated, err := s.queue.AttachUploadedSubtitle(id, lang, attachRole, outSrt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	roleDesc := "TTS subtitle"
	if attachRole == "both" {
		roleDesc = "TTS & Embed subtitle"
	}
	s.broker.Notify("success", "Google AI Subtitle Ready", fmt.Sprintf("Processed %d -> %d cues (%s) and attached as %s.", totalCues, keptCues, strings.ToUpper(lang), roleDesc))
	writeJSON(w, http.StatusOK, map[string]any{
		"job":        updated,
		"total_cues": totalCues,
		"kept_cues":  keptCues,
		"path":       outSrt,
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

func (s *Server) handleQueueStartSingle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ReuseAudioChunks *bool `json:"reuse_audio_chunks,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.queue.StartSingle(id, req.ReuseAudioChunks); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.queue.Snapshot())
}

func (s *Server) handleQueueClearCompleted(w http.ResponseWriter, r *http.Request) {
	removed := s.queue.ClearCompleted()
	writeJSON(w, http.StatusOK, map[string]any{
		"removed_count": removed,
		"queue":         s.queue.Snapshot(),
	})
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
	// Exclude the currently active running job so it doesn't appear as "unfinished work" while running
	snap := s.queue.Snapshot()
	filtered := make([]workspace.RecoverableJob, 0, len(jobs))
	for _, j := range jobs {
		if snap.ActiveJobID != "" && j.State.JobID == snap.ActiveJobID {
			continue
		}
		filtered = append(filtered, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":  filtered,
		"count": len(filtered),
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

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []store.LogRecord{})
		return
	}
	logs, err := s.db.ListLogs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		_ = s.db.ClearLogs()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (s *Server) handleGetUnreadNotifications(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, []store.NotificationRecord{})
		return
	}
	notifs, err := s.db.ListUnreadNotifications()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notifs)
}

func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	var req struct {
		UpToID int64 `json:"up_to_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.db.MarkNotificationsRead(req.UpToID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetBrowserTTSStatus(w http.ResponseWriter, r *http.Request) {
	bridge := s.queue.BrowserTTS()
	if bridge == nil {
		writeJSON(w, http.StatusOK, tts.BrowserTTSStatus{Ready: false})
		return
	}
	writeJSON(w, http.StatusOK, bridge.Status())
}

func (s *Server) handleSetBrowserTTSStatus(w http.ResponseWriter, r *http.Request) {
	bridge := s.queue.BrowserTTS()
	if bridge == nil {
		writeError(w, http.StatusServiceUnavailable, "browser TTS bridge not configured")
		return
	}
	var st tts.BrowserTTSStatus
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeError(w, http.StatusBadRequest, "invalid browser TTS status payload")
		return
	}
	bridge.SetStatus(st)
	s.broker.Publish("browser_tts_status", st)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBrowserTTSResult(w http.ResponseWriter, r *http.Request) {
	bridge := s.queue.BrowserTTS()
	if bridge == nil {
		writeError(w, http.StatusServiceUnavailable, "browser TTS bridge not configured")
		return
	}
	reqID := r.PathValue("id")
	errMsg := r.Header.Get("X-TTS-Error")
	if errMsg != "" {
		bridge.Complete(reqID, nil, errMsg)
		writeJSON(w, http.StatusOK, map[string]string{"status": "error_recorded"})
		return
	}
	wavBytes, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read WAV body")
		return
	}
	bridge.Complete(reqID, wavBytes, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
