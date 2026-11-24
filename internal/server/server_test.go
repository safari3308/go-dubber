package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/queue"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/tts"
	"github.com/safari3308/go-dubber/internal/workspace"
)

func TestServer_APIAndSPA(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	mediaDir := filepath.Join(tempDir, "media")
	_ = os.MkdirAll(mediaDir, 0755)
	vidPath := filepath.Join(mediaDir, "Movie.mkv")
	_ = os.WriteFile(vidPath, []byte("dummy"), 0644)

	cfg := config.DefaultConfig()
	cfg.NasDir = mediaDir
	cfg.WorkDir = filepath.Join(tempDir, "workdir")
	_ = config.SaveConfig(cfgPath, cfg)

	ws, err := workspace.NewManager(cfg.WorkDir)
	if err != nil {
		t.Fatalf("workspace.NewManager failed: %v", err)
	}
	broker := sse.NewBroker()
	hw := &hardware.HardwareProfile{OS: "darwin", SelectedEncoder: "hevc_videotoolbox", HasGPU: true}
	ttsMon := tts.NewHealthMonitor(func() *config.Config { return cfg }, time.Minute, nil)
	qm := queue.NewManager(func() *config.Config { return cfg }, hw, ws, broker, ttsMon)

	mockWebFS := fstest.MapFS{
		"index.html": {Data: []byte("<html><body>Go-Dubber SPA</body></html>")},
	}

	srv := New(cfgPath, cfg, hw, ws, qm, broker, ttsMon, mockWebFS, true)
	h := srv.Handler()

	// 1. GET / serves SPA
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Go-Dubber SPA")) {
		t.Errorf("SPA root returned %d: %s", rec.Code, rec.Body.String())
	}

	// 2. GET /api/system/hardware
	req = httptest.NewRequest(http.MethodGet, "/api/system/hardware", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/system/hardware returned %d", rec.Code)
	}

	// 3. GET /api/fs/browse
	req = httptest.NewRequest(http.MethodGet, "/api/fs/browse?path="+mediaDir, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Movie.mkv")) {
		t.Errorf("GET /api/fs/browse failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. POST /api/queue/items & GET /api/queue
	body, _ := json.Marshal(map[string]any{"paths": []string{mediaDir}})
	req = httptest.NewRequest(http.MethodPost, "/api/queue/items", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/queue/items returned %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var snap queue.Snapshot
	_ = json.Unmarshal(rec.Body.Bytes(), &snap)
	if len(snap.Items) != 1 || snap.Items[0].FileName != "Movie.mkv" {
		t.Errorf("unexpected queue snapshot: %+v", snap)
	}

	// 5. POST /api/queue/items/{id}/subtitles (upload external subtitle)
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	_ = mw.WriteField("language", "vi")
	_ = mw.WriteField("role", "both")
	fw, _ := mw.CreateFormFile("file", "Movie.vi.ass")
	_, _ = fw.Write([]byte("[Script Info]\nTitle: Uploaded ASS\n"))
	_ = mw.Close()

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/queue/items/"+snap.Items[0].ID+"/subtitles", &mpBuf)
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, uploadReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/queue/items/{id}/subtitles returned %d: %s", rec.Code, rec.Body.String())
	}
	var updatedJob queue.JobItem
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedJob)
	if len(updatedJob.Config.DubLanguages) != 1 || updatedJob.Config.DubLanguages[0] != "vi" {
		t.Errorf("expected vi to be enabled after upload, got %v", updatedJob.Config.DubLanguages)
	}

	// 6. GET /api/recovery
	req = httptest.NewRequest(http.MethodGet, "/api/recovery", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/recovery returned %d", rec.Code)
	}
}
