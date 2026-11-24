package subtitle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/safari3308/go-dubber/internal/config"
)

func TestResolveDualSubtitle_SRTForTTSAndASSForEmbed(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "Anime - S01E01.mkv")
	srtPath := filepath.Join(tempDir, "Anime - S01E01.vi.srt")
	assPath := filepath.Join(tempDir, "Anime - S01E01.vi.ass")

	_ = os.WriteFile(videoPath, []byte("video"), 0644)
	_ = os.WriteFile(srtPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nXin chào\n"), 0644)
	_ = os.WriteFile(assPath, []byte("[Script Info]\nTitle: Signs & Descriptions\n"), 0644)

	cfg := config.DefaultConfig()
	spec := ResolveDualSubtitle(videoPath, nil, cfg, "vi")
	if spec == nil {
		t.Fatalf("expected non-nil LanguageDubSpec")
	}

	if spec.TTSSub.Type != "external" || spec.TTSSub.Path != filepath.Clean(srtPath) {
		t.Errorf("expected TTSSub=%q, got %+v", srtPath, spec.TTSSub)
	}
	if spec.EmbedSub == nil || spec.EmbedSub.Path != filepath.Clean(assPath) || !spec.EmbedSub.IsASS() {
		t.Errorf("expected EmbedSub=%q (ASS), got %+v", assPath, spec.EmbedSub)
	}
	if !spec.SyncTTSSub {
		t.Errorf("expected SyncTTSSub=true for external SRT")
	}
	if spec.SyncEmbedSub {
		t.Errorf("expected SyncEmbedSub=false for ASS to preserve styling")
	}
}

func TestResolveDualSubtitle_OnlyASS(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "Anime - S01E02.mkv")
	assPath := filepath.Join(tempDir, "Anime - S01E02.vi.ass")

	_ = os.WriteFile(videoPath, []byte("video"), 0644)
	_ = os.WriteFile(assPath, []byte("[Script Info]\nTitle: ASS Only\n"), 0644)

	cfg := config.DefaultConfig()
	spec := ResolveDualSubtitle(videoPath, nil, cfg, "vi")
	if spec == nil {
		t.Fatalf("expected non-nil LanguageDubSpec")
	}

	if spec.TTSSub.Path != filepath.Clean(assPath) {
		t.Errorf("expected TTSSub=%q, got %+v", assPath, spec.TTSSub)
	}
	if spec.EmbedSub == nil || spec.EmbedSub.Path != filepath.Clean(assPath) {
		t.Errorf("expected EmbedSub=%q, got %+v", assPath, spec.EmbedSub)
	}
}

func TestResolveDualSubtitle_EmbeddedForTTSAndExternalASSForEmbed(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "Movie.mkv")
	assPath := filepath.Join(tempDir, "Movie.vi.ass")
	_ = os.WriteFile(videoPath, []byte("video"), 0644)
	_ = os.WriteFile(assPath, []byte("[Script Info]\n"), 0644)

	embedded := []EmbeddedSubInfo{
		{SubIndex: 0, Language: "vi", Title: "VietSub", CodecName: "subrip", IsBitmap: false},
	}

	cfg := config.DefaultConfig()
	spec := ResolveDualSubtitle(videoPath, embedded, cfg, "vi")
	if spec == nil {
		t.Fatalf("expected non-nil LanguageDubSpec")
	}
	if spec.TTSSub.Type != "embedded" || spec.TTSSub.EmbeddedIndex != 0 {
		t.Errorf("expected embedded TTSSub, got %+v", spec.TTSSub)
	}
	if spec.EmbedSub == nil || spec.EmbedSub.Path != filepath.Clean(assPath) {
		t.Errorf("expected external ASS EmbedSub, got %+v", spec.EmbedSub)
	}
}
