package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
