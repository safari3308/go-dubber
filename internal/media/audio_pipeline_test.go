package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/safari3308/go-dubber/internal/subtitle"
)

func TestExtractPCMFromWAV(t *testing.T) {
	dataPayload := []byte("1234567890PCM_DATA")
	wavWithData := append([]byte("RIFF1234WAVEfmt ....data\x12\x00\x00\x00"), dataPayload...)

	pcm := ExtractPCMFromWAV(wavWithData)
	if string(pcm) != string(dataPayload) {
		t.Errorf("ExtractPCMFromWAV = %q; want %q", string(pcm), string(dataPayload))
	}

	fallbackHeader := make([]byte, 50)
	copy(fallbackHeader, []byte("HEADER_NO_DATA_TAG_OVER_44_BYTES_LONG_DUMMY_HEADER"))
	pcmFallback := ExtractPCMFromWAV(fallbackHeader)
	if len(pcmFallback) != 6 {
		t.Errorf("ExtractPCMFromWAV fallback length = %d; want 6", len(pcmFallback))
	}

	if pcmShort := ExtractPCMFromWAV([]byte("short")); pcmShort != nil {
		t.Errorf("ExtractPCMFromWAV short slice = %v; want nil", pcmShort)
	}
}

func TestTrimSilencePCM16(t *testing.T) {
	silentSample := []byte{0, 0}
	nonSilentSample := []byte{0x00, 0x10}

	var pcm []byte
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, nonSilentSample...)
	pcm = append(pcm, silentSample...)

	trimmed := TrimSilencePCM16(pcm, 300)

	if len(trimmed) != 2 {
		t.Fatalf("TrimSilencePCM16 length = %d; want 2", len(trimmed))
	}

	if !bytes.Equal(trimmed, nonSilentSample) {
		t.Errorf("TrimSilencePCM16 content = %v; want %v", trimmed, nonSilentSample)
	}
}

func TestMixPCM16(t *testing.T) {
	canvas := make([]byte, 8)
	canvas[0] = 100
	canvas[1] = 0

	pcm := make([]byte, 4)
	pcm[0] = 200
	pcm[1] = 0

	MixPCM16(canvas, pcm, 0)

	mixedSample := int16(canvas[0]) | (int16(canvas[1]) << 8)
	if mixedSample != 300 {
		t.Errorf("Mixed sample = %d; want 300", mixedSample)
	}
}

func TestCreateWAVHeader(t *testing.T) {
	header := createWAVHeader(100, 24000, 1, 16)

	if len(header) != 44 {
		t.Fatalf("Header length = %d; want 44", len(header))
	}
	if string(header[0:4]) != "RIFF" {
		t.Errorf("Header RIFF tag = %q; want RIFF", string(header[0:4]))
	}
	if string(header[8:12]) != "WAVE" {
		t.Errorf("Header WAVE tag = %q; want WAVE", string(header[8:12]))
	}
	if string(header[36:40]) != "data" {
		t.Errorf("Header data tag = %q; want data", string(header[36:40]))
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

	if err := ConvertToSRT(assFile, outSrtFile); err != nil {
		t.Fatalf("ConvertToSRT failed: %v", err)
	}

	entries, err := subtitle.ParseSRT(outSrtFile, "en")
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
