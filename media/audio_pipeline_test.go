package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCollapseRepeatedChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"wwwwwhattttttt", "what"},
		{"nooooo", "no"},
		{"look", "look"},
		{"speed", "speed"},
		{"apple", "apple"},
		{"1000", "1000"},
		{"9999", "9999"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := collapseRepeatedChars(tt.input)
			if result != tt.expected {
				t.Errorf("collapseRepeatedChars(%q) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCleanDialogueLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		lang     string
		expected string
	}{
		{
			name:     "ASS/HTML tags removal",
			input:    "{\\pos(10,20)}<b>Hello</b> world",
			lang:     "en",
			expected: "Hello world",
		},
		{
			name:     "Parentheses preservation",
			input:    "(Tập 53) Hello",
			lang:     "en",
			expected: "Tập 53 Hello",
		},
		{
			name:     "Speaker prefix removal",
			input:    "JOHN: Hello there",
			lang:     "en",
			expected: "Hello there",
		},
		{
			name:     "Music symbols removal",
			input:    "♪ Sing a song ♫",
			lang:     "en",
			expected: "Sing a song",
		},
		{
			name:     "Stuttering collapse",
			input:    "b-but wh-what",
			lang:     "en",
			expected: "But what",
		},
		{
			name:     "Repeated char collapse",
			input:    "nooooo stop",
			lang:     "en",
			expected: "No stop",
		},
		{
			name:     "Vietnamese number conversion",
			input:    "Có 2 cái",
			lang:     "vi",
			expected: "Có hai cái",
		},
		{
			name:     "Preserve numbers 1000 and 9999 (en)",
			input:    "1000 and 9999",
			lang:     "en",
			expected: "1000 and 9999",
		},
		{
			name:     "Convert numbers 1000 and 9999 (vi)",
			input:    "Có 1000 và 9999",
			lang:     "vi",
			expected: "Có một nghìn và chín nghìn chín trăm chín mươi chín",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanDialogueLine(tt.input, tt.lang)
			if result != tt.expected {
				t.Errorf("CleanDialogueLine(%q, %q) = %q; want %q", tt.input, tt.lang, result, tt.expected)
			}
		})
	}
}

func TestSrtTimeToSeconds(t *testing.T) {
	tests := []struct {
		timeStr  string
		expected float64
	}{
		{"00:00:00,000", 0.0},
		{"00:01:23,456", 83.456},
		{"01:00:00.000", 3600.0},
		{"invalid", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.timeStr, func(t *testing.T) {
			result := srtTimeToSeconds(tt.timeStr)
			if result != tt.expected {
				t.Errorf("srtTimeToSeconds(%q) = %v; want %v", tt.timeStr, result, tt.expected)
			}
		})
	}
}

func TestParseSRT(t *testing.T) {
	tempDir := t.TempDir()
	srtFile := filepath.Join(tempDir, "test.srt")

	srtContent := `1
00:00:01,000 --> 00:00:03,500
Hello World!

2
00:00:04,000 --> 00:00:06,000
HP: 100
Level: 99

3
00:00:07,000 --> 00:00:09,000
Good bye!
`
	if err := os.WriteFile(srtFile, []byte(srtContent), 0644); err != nil {
		t.Fatalf("Failed to write srt file: %v", err)
	}

	entries, err := ParseSRT(srtFile, "en")
	if err != nil {
		t.Fatalf("ParseSRT failed: %v", err)
	}

	// Entry 2 should be skipped because it contains stat line (HP:, Level:)
	if len(entries) != 2 {
		t.Fatalf("ParseSRT returned %d entries; want 2", len(entries))
	}

	if entries[0].StartSec != 1.0 || entries[0].EndSec != 3.5 || entries[0].Text != "Hello World!" {
		t.Errorf("Entry 0 unexpected: %+v", entries[0])
	}

	if entries[1].StartSec != 7.0 || entries[1].EndSec != 9.0 || entries[1].Text != "Good bye!" {
		t.Errorf("Entry 1 unexpected: %+v", entries[1])
	}
}

func TestExtractPCMFromWAV(t *testing.T) {
	// 1. WAV header containing "data" tag
	dataPayload := []byte("1234567890PCM_DATA")
	wavWithData := append([]byte("RIFF1234WAVEfmt ....data\x12\x00\x00\x00"), dataPayload...)

	pcm := ExtractPCMFromWAV(wavWithData)
	if string(pcm) != string(dataPayload) {
		t.Errorf("ExtractPCMFromWAV = %q; want %q", string(pcm), string(dataPayload))
	}

	// 2. Short header fallback (>44 bytes)
	fallbackHeader := make([]byte, 50)
	copy(fallbackHeader, []byte("HEADER_NO_DATA_TAG_OVER_44_BYTES_LONG_DUMMY_HEADER"))
	pcmFallback := ExtractPCMFromWAV(fallbackHeader)
	if len(pcmFallback) != 6 {
		t.Errorf("ExtractPCMFromWAV fallback length = %d; want 6", len(pcmFallback))
	}

	// 3. Short slice (<44 bytes)
	if pcmShort := ExtractPCMFromWAV([]byte("short")); pcmShort != nil {
		t.Errorf("ExtractPCMFromWAV short slice = %v; want nil", pcmShort)
	}
}

func TestTrimSilencePCM16(t *testing.T) {
	// 16-bit samples (little endian). 0 is silent, 1000 is non-silent sample.
	silentSample := []byte{0, 0}
	nonSilentSample := []byte{0x00, 0x10} // 4096 in int16

	var pcm []byte
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, nonSilentSample...)
	pcm = append(pcm, silentSample...)

	trimmed := TrimSilencePCM16(pcm, 300)

	// Should trim leading 2 silent samples and trailing 1 silent sample, leaving 1 non-silent sample (2 bytes)
	if len(trimmed) != 2 {
		t.Fatalf("TrimSilencePCM16 length = %d; want 2", len(trimmed))
	}

	if !bytes.Equal(trimmed, nonSilentSample) {
		t.Errorf("TrimSilencePCM16 content = %v; want %v", trimmed, nonSilentSample)
	}
}

func TestMixPCM16(t *testing.T) {
	canvas := make([]byte, 8)
	// Sample 1 at offset 0 = 100
	canvas[0] = 100
	canvas[1] = 0

	pcm := make([]byte, 4)
	// Sample 1 = 200
	pcm[0] = 200
	pcm[1] = 0

	MixPCM16(canvas, pcm, 0)

	mixedSample := int16(canvas[0]) | (int16(canvas[1]) << 8)
	if mixedSample != 300 {
		t.Errorf("Mixed sample = %d; want 300", mixedSample)
	}
}

func TestCreateWAVHeader(t *testing.T) {
	dataLen := 100
	sampleRate := 24000
	numChannels := 1
	bitsPerSample := 16

	header := createWAVHeader(dataLen, sampleRate, numChannels, bitsPerSample)

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
