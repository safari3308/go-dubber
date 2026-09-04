package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/safari3308/go-dubber/config"
	"github.com/safari3308/go-dubber/media"
)

func TestExtractEpisodeCodes(t *testing.T) {
	tests := []struct {
		baseName string
		expected []string
	}{
		{
			baseName: "Though I Am an Inept Villainess - S01E002 - Will I Be Living Here",
			expected: []string{"S01E002", "S01E02", "S1E2", "1x02", "E02", "E002"},
		},
		{
			baseName: "Anime Title 1x05 Episode Name",
			expected: []string{"1x05", "S01E05", "E05"},
		},
		{
			baseName: "Show Name EP03",
			expected: []string{"EP03", "E03", "E003"},
		},
	}

	for _, tt := range tests {
		codes := extractEpisodeCodes(tt.baseName)
		if len(codes) == 0 {
			t.Errorf("extractEpisodeCodes(%q) returned empty slice, expected at least %v", tt.baseName, tt.expected)
		}
		for _, exp := range tt.expected {
			found := false
			for _, c := range codes {
				if c == exp {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("extractEpisodeCodes(%q) = %v; missing expected code %q", tt.baseName, codes, exp)
			}
		}
	}
}

func TestIsSubMatchingLanguage(t *testing.T) {
	tests := []struct {
		subLang  string
		subTitle string
		target   string
		expected bool
	}{
		{"vi", "", "vi", true},
		{"vie", "", "vi", true},
		{"unknown", "VietSub by Fansub", "vi", true},
		{"unknown", "Tiếng Việt", "vi", true},
		{"eng", "", "vi", false},
		{"en", "English SDH", "en", true},
		{"ja", "Japanese Audio", "ja", true},
	}

	for _, tt := range tests {
		got := isSubMatchingLanguage(tt.subLang, tt.subTitle, tt.target)
		if got != tt.expected {
			t.Errorf("isSubMatchingLanguage(%q, %q, %q) = %v; want %v", tt.subLang, tt.subTitle, tt.target, got, tt.expected)
		}
	}
}

func TestFindExternalSubtitleForLanguage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	videoBase := "Though I Am an Inept Villainess - S01E002 - Will I Be Living Here"
	videoPath := filepath.Join(tempDir, videoBase+".mkv")

	cfg := &config.Config{
		SubLanguage: "vi",
	}

	// Test Case 1: Standalone vi.srt in video directory
	viSrt := filepath.Join(tempDir, "vi.srt")
	if err := os.WriteFile(viSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644); err != nil {
		t.Fatalf("failed to create vi.srt: %v", err)
	}

	foundPath, found := findExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("findExternalSubtitleForLanguage failed to find vi.srt")
	} else if filepath.Clean(foundPath) != filepath.Clean(viSrt) {
		t.Errorf("findExternalSubtitleForLanguage = %q, want %q", foundPath, viSrt)
	}

	// Remove vi.srt for next test case
	os.Remove(viSrt)

	// Test Case 2: baseName - vi.srt
	dashViSrt := filepath.Join(tempDir, videoBase+" - vi.srt")
	os.WriteFile(dashViSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = findExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("findExternalSubtitleForLanguage failed to find 'baseName - vi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(dashViSrt) {
		t.Errorf("findExternalSubtitleForLanguage = %q, want %q", foundPath, dashViSrt)
	}

	os.Remove(dashViSrt)

	// Test Case 3: Subs/S01E002/vi.srt
	subsDir := filepath.Join(tempDir, "Subs", "S01E002")
	os.MkdirAll(subsDir, 0755)
	subInSub := filepath.Join(subsDir, "vi.srt")
	os.WriteFile(subInSub, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = findExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("findExternalSubtitleForLanguage failed to find 'Subs/S01E002/vi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(subInSub) {
		t.Errorf("findExternalSubtitleForLanguage = %q, want %q", foundPath, subInSub)
	}

	os.RemoveAll(subsDir)

	// Test Case 4: baseName.vi.hi.srt (Hearing Impaired / SDH modifier)
	hiViSrt := filepath.Join(tempDir, videoBase+".vi.hi.srt")
	os.WriteFile(hiViSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = findExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("findExternalSubtitleForLanguage failed to find 'baseName.vi.hi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(hiViSrt) {
		t.Errorf("findExternalSubtitleForLanguage = %q, want %q", foundPath, hiViSrt)
	}

	os.Remove(hiViSrt)

	// Test Case 5: baseName - vi.ass (External ASS Subtitle)
	assPath := filepath.Join(tempDir, videoBase+" - vi.ass")
	os.WriteFile(assPath, []byte("[Script Info]\nTitle: Test\n"), 0644)

	foundPath, found = findExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("findExternalSubtitleForLanguage failed to find 'baseName - vi.ass'")
	} else if filepath.Clean(foundPath) != filepath.Clean(assPath) {
		t.Errorf("findExternalSubtitleForLanguage = %q, want %q", foundPath, assPath)
	}
}

func TestSelectSubtitleForLanguagePriority(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub_select_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	videoPath := filepath.Join(tempDir, "TestVideo.mkv")
	extSubPath := filepath.Join(tempDir, "TestVideo.vi.srt")
	os.WriteFile(extSubPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	cfg := &config.Config{}

	// Scenario A: Has embedded Vietnamese subtitle -> should select embedded sub (Priority 1)
	infoWithEmbedded := &media.VideoInfo{
		EmbeddedSubStreams: []media.EmbeddedSubInfo{
			{SubIndex: 0, Language: "vi", Title: "VietSub", CodecName: "subrip", IsBitmap: false},
		},
	}
	selected := selectSubtitleForLanguage(videoPath, infoWithEmbedded, cfg, "vi")
	if !selected.Found || selected.IsExternal || selected.EmbeddedIndex != 0 {
		t.Errorf("Expected embedded sub index 0, got %v", selected)
	}

	// Scenario B: Embedded subtitle is bitmap (PGS) -> should skip bitmap embedded sub and select external sub (Priority 2)
	infoWithBitmap := &media.VideoInfo{
		EmbeddedSubStreams: []media.EmbeddedSubInfo{
			{SubIndex: 0, Language: "vi", Title: "PGS Viet", CodecName: "hdmv_pgs_subtitle", IsBitmap: true},
		},
	}
	selectedExt := selectSubtitleForLanguage(videoPath, infoWithBitmap, cfg, "vi")
	if !selectedExt.Found || !selectedExt.IsExternal || selectedExt.SubPath != extSubPath {
		t.Errorf("Expected external sub %q, got %v", extSubPath, selectedExt)
	}

	// Scenario C: No embedded subtitle -> should select external sub (Priority 2)
	infoNoEmbedded := &media.VideoInfo{
		EmbeddedSubStreams: []media.EmbeddedSubInfo{},
	}
	selectedExt2 := selectSubtitleForLanguage(videoPath, infoNoEmbedded, cfg, "vi")
	if !selectedExt2.Found || !selectedExt2.IsExternal || selectedExt2.SubPath != extSubPath {
		t.Errorf("Expected external sub %q, got %v", extSubPath, selectedExt2)
	}
}

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
		"temp_Show - S01E03.mkv", // temp file to ignore
		".upload_Show - S01E04.mkv", // upload temp to ignore
		"readme.txt", // non-video file to ignore
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

func TestIsSupportedSubtitleExtension(t *testing.T) {
	supported := []string{".srt", ".ass", ".ssa", ".vtt", ".sub", ".sbv", ".smi", ".sami", ".ttml", ".dfxp", ".lrc", ".ASS", ".VTT"}
	for _, ext := range supported {
		if !isSupportedSubtitleExtension(ext) {
			t.Errorf("isSupportedSubtitleExtension(%q) = false, want true", ext)
		}
	}

	unsupported := []string{".txt", ".mkv", ".mp4", ".doc", ".nfo"}
	for _, ext := range unsupported {
		if isSupportedSubtitleExtension(ext) {
			t.Errorf("isSupportedSubtitleExtension(%q) = true, want false", ext)
		}
	}
}

func TestConvertToSRT(t *testing.T) {
	tempDir := t.TempDir()
	assFile := filepath.Join(tempDir, "sample.ass")
	outSrtFile := filepath.Join(tempDir, "converted.srt")

	assContent := `[Script Info]
Title: Sample ASS
ScriptType: v4.00+
Collisions: Normal
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\an8}Hello from external ASS subtitle!
`
	if err := os.WriteFile(assFile, []byte(assContent), 0644); err != nil {
		t.Fatalf("failed to write sample.ass: %v", err)
	}

	if err := media.ConvertToSRT(assFile, outSrtFile); err != nil {
		t.Fatalf("media.ConvertToSRT failed: %v", err)
	}

	// Verify that the converted file exists and can be parsed by ParseSRT
	entries, err := media.ParseSRT(outSrtFile, "en")
	if err != nil {
		t.Fatalf("ParseSRT failed on converted SRT: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry from converted SRT, got %d", len(entries))
	}

	if entries[0].Text != "Hello from external ass subtitle!" {
		t.Errorf("expected clean text 'Hello from external ass subtitle!', got %q", entries[0].Text)
	}
}




