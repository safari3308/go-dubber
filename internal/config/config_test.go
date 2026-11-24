package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Success(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	jsonContent := `{
		"nas_dir": "/volume1/Media",
		"api_url": "http://localhost:8000",
		"api_token": "secret-token",
		"interactive_mode": true,
		"sub_language": "vi",
		"use_gpu": true,
		"workers": 4,
		"tts_speed": 1.25,
		"ffmpeg": {
			"gpu_codec": "hevc_nvenc",
			"gpu_preset": "p4",
			"audio_bitrate": "192k"
		}
	}`

	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.NasDir != "/volume1/Media" {
		t.Errorf("cfg.NasDir = %q; want %q", cfg.NasDir, "/volume1/Media")
	}
	if cfg.ApiUrl != "http://localhost:8000" {
		t.Errorf("cfg.ApiUrl = %q; want %q", cfg.ApiUrl, "http://localhost:8000")
	}
	if cfg.ApiToken != "secret-token" {
		t.Errorf("cfg.ApiToken = %q; want %q", cfg.ApiToken, "secret-token")
	}
	if !cfg.InteractiveMode {
		t.Errorf("cfg.InteractiveMode = false; want true")
	}
	if cfg.Workers != 4 {
		t.Errorf("cfg.Workers = %d; want 4", cfg.Workers)
	}
	if cfg.TTSSpeed != 1.25 {
		t.Errorf("cfg.TTSSpeed = %v; want 1.25", cfg.TTSSpeed)
	}
	if cfg.FFmpeg.GPUCodec != "hevc_nvenc" {
		t.Errorf("cfg.FFmpeg.GPUCodec = %q; want %q", cfg.FFmpeg.GPUCodec, "hevc_nvenc")
	}
}

func TestLoadConfig_DefaultTTSSpeed(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config_zero_speed.json")
	jsonContent := `{
		"api_url": "http://localhost:8000",
		"tts_speed": 0
	}`

	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.TTSSpeed != 1.1 {
		t.Errorf("cfg.TTSSpeed = %v; want default 1.1 when speed is <= 0", cfg.TTSSpeed)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("non_existent_config.json")
	if err == nil {
		t.Errorf("LoadConfig expected error for non-existent file, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid.json")
	if err := os.WriteFile(configPath, []byte("{ invalid json "), 0644); err != nil {
		t.Fatalf("Failed to write invalid config file: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Errorf("LoadConfig expected error for invalid JSON, got nil")
	}
}

func TestLoadConfig_DubLanguages(t *testing.T) {
	tempDir := t.TempDir()

	cfg1Path := filepath.Join(tempDir, "cfg1.json")
	_ = os.WriteFile(cfg1Path, []byte(`{"dub_languages": ["EN", " vi ", "en"]}`), 0644)
	cfg1, err := LoadConfig(cfg1Path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg1.DubLanguages) != 2 || cfg1.DubLanguages[0] != "en" || cfg1.DubLanguages[1] != "vi" {
		t.Errorf("cfg1.DubLanguages = %v; want [en vi]", cfg1.DubLanguages)
	}

	cfg2Path := filepath.Join(tempDir, "cfg2.json")
	_ = os.WriteFile(cfg2Path, []byte(`{"sub_language": "EN"}`), 0644)
	cfg2, err := LoadConfig(cfg2Path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg2.DubLanguages) != 1 || cfg2.DubLanguages[0] != "en" {
		t.Errorf("cfg2.DubLanguages = %v; want [en]", cfg2.DubLanguages)
	}

	cfg3Path := filepath.Join(tempDir, "cfg3.json")
	_ = os.WriteFile(cfg3Path, []byte(`{}`), 0644)
	cfg3, err := LoadConfig(cfg3Path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg3.DubLanguages) != 1 || cfg3.DubLanguages[0] != "vi" {
		t.Errorf("cfg3.DubLanguages = %v; want [vi]", cfg3.DubLanguages)
	}
}

func TestSaveConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "saved.json")

	cfg := DefaultConfig()
	cfg.NasDir = "/mnt/nas"
	cfg.ServerPort = 9090

	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig failed: %v", err)
	}
	if loaded.NasDir != "/mnt/nas" || loaded.ServerPort != 9090 {
		t.Errorf("loaded config mismatch: %+v", loaded)
	}
}

func TestGetConfigFileChecksum(t *testing.T) {
	tempDir := t.TempDir()
	testPath := filepath.Join(tempDir, "test_config.json")

	content1 := []byte(`{"nas_dir": "dir1"}`)
	if err := os.WriteFile(testPath, content1, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash1, err := GetConfigFileChecksum(testPath)
	if err != nil {
		t.Fatalf("GetConfigFileChecksum failed: %v", err)
	}
	if hash1 == "" {
		t.Errorf("expected non-empty hash")
	}

	hash1Again, _ := GetConfigFileChecksum(testPath)
	if hash1 != hash1Again {
		t.Errorf("hash mismatch for identical content: %s vs %s", hash1, hash1Again)
	}

	content2 := []byte(`{"nas_dir": "dir2"}`)
	if err := os.WriteFile(testPath, content2, 0644); err != nil {
		t.Fatalf("failed to overwrite test file: %v", err)
	}

	hash2, err := GetConfigFileChecksum(testPath)
	if err != nil {
		t.Fatalf("GetConfigFileChecksum failed after edit: %v", err)
	}
	if hash1 == hash2 {
		t.Errorf("expected different hash after content edit, got same %s", hash1)
	}
}

func TestResolveConfigPath(t *testing.T) {
	tempDir := t.TempDir()
	existingFile := filepath.Join(tempDir, "custom.json")
	_ = os.WriteFile(existingFile, []byte(`{}`), 0644)

	got := ResolveConfigPath(existingFile)
	if got != existingFile {
		t.Errorf("ResolveConfigPath(%q) = %q; want %q", existingFile, got, existingFile)
	}

	fallback := ResolveConfigPath("non_existent.json")
	if fallback == "" {
		t.Errorf("ResolveConfigPath returned empty string")
	}
}
