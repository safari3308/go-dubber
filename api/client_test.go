package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/safari3308/go-dubber/config"
)

func TestRenderSingleLineTTS_Success(t *testing.T) {
	mockWavBytes := []byte("RIFF1234WAVEfmt mock audio data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			http.Error(w, "invalid path", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "invalid content-type", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body error", http.StatusInternalServerError)
			return
		}

		// Text "Xin chào 1" will be normalized to "Xin chào một" for lang "vi"
		if !strings.Contains(string(body), "Xin chào một") {
			t.Errorf("Request body missing normalized text: %s", string(body))
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mockWavBytes)
	}))
	defer server.Close()

	cfg := &config.Config{
		ApiUrl:   server.URL,
		ApiToken: "test-token",
		TTSSpeed: 1.1,
	}

	result, err := RenderSingleLineTTS(cfg, "Xin chào 1", "vi", "A")
	if err != nil {
		t.Fatalf("RenderSingleLineTTS failed: %v", err)
	}

	if string(result) != string(mockWavBytes) {
		t.Errorf("RenderSingleLineTTS bytes = %q; want %q", string(result), string(mockWavBytes))
	}
}

func TestRenderSingleLineTTS_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	cfg := &config.Config{
		ApiUrl:   server.URL,
		ApiToken: "test-token",
	}

	_, err := RenderSingleLineTTS(cfg, "Hello", "en", "A")
	if err == nil {
		t.Errorf("Expected error from server error response, got nil")
	}
}

func TestSyncSubtitleWithServer_Success(t *testing.T) {
	tempDir := t.TempDir()
	targetSubPath := filepath.Join(tempDir, "input.srt")
	refAudioPath := filepath.Join(tempDir, "ref.aac")
	outputSubPath := filepath.Join(tempDir, "synced.srt")

	_ = os.WriteFile(targetSubPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)
	_ = os.WriteFile(refAudioPath, []byte("mock audio stream"), 0644)

	syncedSrtContent := "1\n00:00:01,500 --> 00:00:02,500\nHello Synced\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/utils/subsync" {
			http.Error(w, "invalid path", http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			http.Error(w, "bad form data", http.StatusBadRequest)
			return
		}

		if r.FormValue("ref_type") != "audio" {
			t.Errorf("Form ref_type = %q; want 'audio'", r.FormValue("ref_type"))
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(syncedSrtContent))
	}))
	defer server.Close()

	cfg := &config.Config{
		ApiUrl:   server.URL,
		ApiToken: "test-token",
	}

	err := SyncSubtitleWithServer(cfg, targetSubPath, refAudioPath, outputSubPath)
	if err != nil {
		t.Fatalf("SyncSubtitleWithServer failed: %v", err)
	}

	gotContent, err := os.ReadFile(outputSubPath)
	if err != nil {
		t.Fatalf("Failed to read output srt: %v", err)
	}

	if string(gotContent) != syncedSrtContent {
		t.Errorf("Synced output content = %q; want %q", string(gotContent), syncedSrtContent)
	}
}

func TestRenderTTSFromServer_Success(t *testing.T) {
	tempDir := t.TempDir()
	subPath := filepath.Join(tempDir, "input.srt")
	outputWavPath := filepath.Join(tempDir, "output.wav")

	_ = os.WriteFile(subPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nXin chào\n"), 0644)
	mockWavBytes := []byte("RIFF....WAVEfmt ...")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			http.Error(w, "invalid path", http.StatusNotFound)
			return
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			http.Error(w, "bad form data", http.StatusBadRequest)
			return
		}

		if r.FormValue("lang") != "vi" {
			t.Errorf("Form lang = %q; want 'vi'", r.FormValue("lang"))
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mockWavBytes)
	}))
	defer server.Close()

	cfg := &config.Config{
		ApiUrl:   server.URL,
		ApiToken: "test-token",
		TTSSpeed: 1.1,
	}

	err := RenderTTSFromServer(cfg, subPath, outputWavPath, "vi")
	if err != nil {
		t.Fatalf("RenderTTSFromServer failed: %v", err)
	}

	gotContent, err := os.ReadFile(outputWavPath)
	if err != nil {
		t.Fatalf("Failed to read output wav: %v", err)
	}

	if string(gotContent) != string(mockWavBytes) {
		t.Errorf("Rendered WAV content = %q; want %q", string(gotContent), string(mockWavBytes))
	}
}
