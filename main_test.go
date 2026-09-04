package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		less bool
	}{
		{"S01E02.mkv", "S01E10.mkv", true},
		{"S01E10.mkv", "S01E02.mkv", false},
		{"Episode 1.mp4", "Episode 2.mp4", true},
		{"Episode 9.mp4", "Episode 10.mp4", true},
		{"Episode 10.mp4", "Episode 9.mp4", false},
		{"Show - 1.mkv", "Show - 01.mkv", true},
		{"file.mkv", "file.mkv", false},
	}

	for _, tt := range tests {
		got := naturalLess(tt.a, tt.b)
		if got != tt.less {
			t.Errorf("naturalLess(%q, %q) = %v; want %v", tt.a, tt.b, got, tt.less)
		}
	}
}

func TestDiscoverVideoFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create test files in unsorted order
	files := []string{
		"Show - S01E10.mkv",
		"Show - S01E01.mkv",
		"Show - S01E02.mkv",
		"temp_Show - S01E03.mkv",   // temp file to ignore
		".upload_Show - S01E04.mkv", // upload temp to ignore
		"readme.txt",               // non-video file to ignore
		"subfolder/Show - S01E03.mp4",
	}

	for _, f := range files {
		fullPath := filepath.Join(tempDir, f)
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, []byte("dummy video content"), 0644)
	}

	discovered, err := discoverVideoFiles(tempDir)
	if err != nil {
		t.Fatalf("discoverVideoFiles failed: %v", err)
	}

	if len(discovered) != 4 {
		t.Fatalf("discoverVideoFiles found %d files; want 4 (ignored temp, upload, and txt). Got: %v", len(discovered), discovered)
	}

	// Verify order: S01E01, S01E02, S01E03 (in subfolder), S01E10
	expectedBases := []string{
		"Show - S01E01.mkv",
		"Show - S01E02.mkv",
		"Show - S01E03.mp4",
		"Show - S01E10.mkv",
	}

	for i, exp := range expectedBases {
		gotBase := filepath.Base(discovered[i])
		if gotBase != exp {
			t.Errorf("discovered[%d] = %q; want %q", i, gotBase, exp)
		}
	}
}

func TestRunDubbingSession_ConfigUnchanged(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	emptyNasDir := filepath.Join(tempDir, "nas")
	_ = os.MkdirAll(emptyNasDir, 0755)

	cfgContent := fmt.Sprintf(`{"nas_dir": %q, "api_url": "http://127.0.0.1:9999"}`, emptyNasDir)
	_ = os.WriteFile(configPath, []byte(cfgContent), 0644)

	changed, err := runDubbingSession(configPath)
	if err != nil {
		t.Fatalf("runDubbingSession failed: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false when config was not modified")
	}
}

func TestRunDubbingSession_ConfigChanged(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	nasDir := filepath.Join(tempDir, "nas")
	_ = os.MkdirAll(nasDir, 0755)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When health check is executed during the session, mutate config.json
		newCfg := fmt.Sprintf(`{"nas_dir": %q, "api_url": "http://127.0.0.1:9999", "sub_language": "en"}`, nasDir)
		_ = os.WriteFile(configPath, []byte(newCfg), 0644)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfgContent := fmt.Sprintf(`{"nas_dir": %q, "api_url": %q}`, nasDir, server.URL)
	_ = os.WriteFile(configPath, []byte(cfgContent), 0644)

	changed, err := runDubbingSession(configPath)
	if err != nil {
		t.Fatalf("runDubbingSession failed: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true when config was modified during execution")
	}
}
