package subtitle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/safari3308/go-dubber/internal/config"
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
		codes := ExtractEpisodeCodes(tt.baseName)
		if len(codes) == 0 {
			t.Errorf("ExtractEpisodeCodes(%q) returned empty slice, expected at least %v", tt.baseName, tt.expected)
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
				t.Errorf("ExtractEpisodeCodes(%q) = %v; missing expected code %q", tt.baseName, codes, exp)
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
		got := IsSubMatchingLanguage(tt.subLang, tt.subTitle, tt.target)
		if got != tt.expected {
			t.Errorf("IsSubMatchingLanguage(%q, %q, %q) = %v; want %v", tt.subLang, tt.subTitle, tt.target, got, tt.expected)
		}
	}
}

func TestFindExternalSubtitleForLanguage(t *testing.T) {
	tempDir := t.TempDir()

	videoBase := "Though I Am an Inept Villainess - S01E002 - Will I Be Living Here"
	videoPath := filepath.Join(tempDir, videoBase+".mkv")

	cfg := &config.Config{
		SubLanguage: "vi",
	}

	viSrt := filepath.Join(tempDir, "vi.srt")
	if err := os.WriteFile(viSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644); err != nil {
		t.Fatalf("failed to create vi.srt: %v", err)
	}

	foundPath, found := FindExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("FindExternalSubtitleForLanguage failed to find vi.srt")
	} else if filepath.Clean(foundPath) != filepath.Clean(viSrt) {
		t.Errorf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, viSrt)
	}
	os.Remove(viSrt)

	dashViSrt := filepath.Join(tempDir, videoBase+" - vi.srt")
	os.WriteFile(dashViSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = FindExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("FindExternalSubtitleForLanguage failed to find 'baseName - vi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(dashViSrt) {
		t.Errorf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, dashViSrt)
	}
	os.Remove(dashViSrt)

	subsDir := filepath.Join(tempDir, "Subs", "S01E002")
	os.MkdirAll(subsDir, 0755)
	subInSub := filepath.Join(subsDir, "vi.srt")
	os.WriteFile(subInSub, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = FindExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("FindExternalSubtitleForLanguage failed to find 'Subs/S01E002/vi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(subInSub) {
		t.Errorf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, subInSub)
	}
	os.RemoveAll(subsDir)

	hiViSrt := filepath.Join(tempDir, videoBase+".vi.hi.srt")
	os.WriteFile(hiViSrt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	foundPath, found = FindExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("FindExternalSubtitleForLanguage failed to find 'baseName.vi.hi.srt'")
	} else if filepath.Clean(foundPath) != filepath.Clean(hiViSrt) {
		t.Errorf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, hiViSrt)
	}
	os.Remove(hiViSrt)

	assPath := filepath.Join(tempDir, videoBase+" - vi.ass")
	os.WriteFile(assPath, []byte("[Script Info]\nTitle: Test\n"), 0644)

	foundPath, found = FindExternalSubtitleForLanguage(videoPath, "vi", cfg)
	if !found {
		t.Errorf("FindExternalSubtitleForLanguage failed to find 'baseName - vi.ass'")
	} else if filepath.Clean(foundPath) != filepath.Clean(assPath) {
		t.Errorf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, assPath)
	}
}

func TestFindExternalSubtitleEpisodeConflict(t *testing.T) {
	tempDir := t.TempDir()

	ep11Video := filepath.Join(tempDir, "HELL MODE - The Hardcore Gamer Dominates in Another World with Garbage Balancing - S02E011 - TBA.mkv")
	_ = os.WriteFile(ep11Video, []byte("dummy video"), 0644)

	ep04Sub := filepath.Join(tempDir, "HELL MODE - The Hardcore Gamer Dominates in Another World with Garbage Balancing - S02E004 - AN ORDER FROM THE PRINCE.vi.srt")
	_ = os.WriteFile(ep04Sub, []byte("1\n00:00:01,000 --> 00:00:02,000\nEpisode 4\n"), 0644)

	cfg := &config.Config{SubLanguage: "vi"}

	foundPath, found := FindExternalSubtitleForLanguage(ep11Video, "vi", cfg)
	if found {
		t.Fatalf("FindExternalSubtitleForLanguage should NOT find sub for ep11, but got %q (ep4 sub)", foundPath)
	}

	ep11Sub := filepath.Join(tempDir, "HELL MODE - The Hardcore Gamer Dominates in Another World with Garbage Balancing - S02E011 - TBA.vi.srt")
	_ = os.WriteFile(ep11Sub, []byte("1\n00:00:01,000 --> 00:00:02,000\nEpisode 11\n"), 0644)

	foundPath, found = FindExternalSubtitleForLanguage(ep11Video, "vi", cfg)
	if !found {
		t.Fatalf("FindExternalSubtitleForLanguage failed to find ep11 sub")
	} else if filepath.Clean(foundPath) != filepath.Clean(ep11Sub) {
		t.Fatalf("FindExternalSubtitleForLanguage = %q, want %q", foundPath, ep11Sub)
	}
}

func TestEpisodeBoundaryMatching(t *testing.T) {
	tempDir := t.TempDir()

	ep01Video := filepath.Join(tempDir, "Anime - S01E01 - Pilot.mkv")
	_ = os.WriteFile(ep01Video, []byte("dummy"), 0644)

	ep11Sub := filepath.Join(tempDir, "Anime - S01E011 - Climax.vi.srt")
	_ = os.WriteFile(ep11Sub, []byte("dummy sub"), 0644)

	cfg := &config.Config{SubLanguage: "vi"}

	_, found := FindExternalSubtitleForLanguage(ep01Video, "vi", cfg)
	if found {
		t.Fatalf("Ep01 video falsely matched Ep11 subtitle due to substring containment")
	}
}

func TestSelectSubtitleForLanguagePriority(t *testing.T) {
	tempDir := t.TempDir()

	videoPath := filepath.Join(tempDir, "TestVideo.mkv")
	extSubPath := filepath.Join(tempDir, "TestVideo.vi.srt")
	os.WriteFile(extSubPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0644)

	cfg := &config.Config{}

	embedded := []EmbeddedSubInfo{
		{SubIndex: 0, Language: "vi", Title: "VietSub", CodecName: "subrip", IsBitmap: false},
	}
	selected := SelectSubtitleForLanguage(videoPath, embedded, cfg, "vi", nil)
	if !selected.Found || selected.IsExternal || selected.EmbeddedIndex != 0 {
		t.Errorf("Expected embedded sub index 0, got %v", selected)
	}

	bitmapOnly := []EmbeddedSubInfo{
		{SubIndex: 0, Language: "vi", Title: "PGS Viet", CodecName: "hdmv_pgs_subtitle", IsBitmap: true},
	}
	selectedExt := SelectSubtitleForLanguage(videoPath, bitmapOnly, cfg, "vi", nil)
	if !selectedExt.Found || !selectedExt.IsExternal || selectedExt.SubPath != extSubPath {
		t.Errorf("Expected external sub %q, got %v", extSubPath, selectedExt)
	}

	selectedExt2 := SelectSubtitleForLanguage(videoPath, nil, cfg, "vi", nil)
	if !selectedExt2.Found || !selectedExt2.IsExternal || selectedExt2.SubPath != extSubPath {
		t.Errorf("Expected external sub %q, got %v", extSubPath, selectedExt2)
	}
}

func TestIsSupportedSubtitleExtension(t *testing.T) {
	supported := []string{".srt", ".ass", ".ssa", ".vtt", ".sub", ".sbv", ".smi", ".sami", ".ttml", ".dfxp", ".lrc", ".ASS", ".VTT"}
	for _, ext := range supported {
		if !IsSupportedSubtitleExtension(ext) {
			t.Errorf("IsSupportedSubtitleExtension(%q) = false, want true", ext)
		}
	}

	unsupported := []string{".txt", ".mkv", ".mp4", ".doc", ".nfo"}
	for _, ext := range unsupported {
		if IsSupportedSubtitleExtension(ext) {
			t.Errorf("IsSupportedSubtitleExtension(%q) = true, want false", ext)
		}
	}
}
