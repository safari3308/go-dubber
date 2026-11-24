package subtitle

import (
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

func TestSRTTimeToSeconds(t *testing.T) {
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
			result := SRTTimeToSeconds(tt.timeStr)
			if result != tt.expected {
				t.Errorf("SRTTimeToSeconds(%q) = %v; want %v", tt.timeStr, result, tt.expected)
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

func TestIsSongOrKaraokeLine(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		startSec float64
		endSec   float64
		want     bool
	}{
		{"Normal dialogue", "Hello world!", 1.0, 3.0, false},
		{"Top dialogue with an8 tag", "<font face=\"Arial\">{\\an8}TẬP 3: ELNA TẬP KÍCH</font>", 1.0, 3.0, false},
		{"Music note symbol", "♪ Lalala song ♪", 1.0, 3.0, true},
		{"Song bracket", "[Music]", 1.0, 3.0, true},
		{"ASS karaoke tag", "{\\k15}ka{\\k20}ra{\\k10}o{\\k30}ke", 1.0, 3.0, true},
		{"Song font", "<font face=\"ClubTypeMercurius-Medium\">Text</font>", 1.0, 3.0, true},
		{"Ultra-short duration", "Quick", 1.00, 1.05, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSongOrKaraokeLine(tt.text, tt.startSec, tt.endSec)
			if got != tt.want {
				t.Errorf("IsSongOrKaraokeLine(%q, %v, %v) = %v; want %v", tt.text, tt.startSec, tt.endSec, got, tt.want)
			}
		})
	}
}

func TestFilterSongAndKaraokeSubtitles(t *testing.T) {
	tempDir := t.TempDir()
	inPath := filepath.Join(tempDir, "input.srt")
	outPath := filepath.Join(tempDir, "output.srt")

	srtContent := `1
00:00:01,000 --> 00:00:03,000
Normal dialogue 1

2
00:00:04,000 --> 00:00:04,100
Short syllable

3
00:00:05,000 --> 00:00:07,000
<font face="ClubTypeMercurius-Medium">♪ Song lyric ♪</font>

4
00:00:08,000 --> 00:00:10,000
Normal dialogue 2
`
	if err := os.WriteFile(inPath, []byte(srtContent), 0644); err != nil {
		t.Fatalf("Failed to write input SRT: %v", err)
	}

	total, kept, err := FilterSongAndKaraokeSubtitles(inPath, outPath)
	if err != nil {
		t.Fatalf("FilterSongAndKaraokeSubtitles failed: %v", err)
	}

	if total != 4 || kept != 2 {
		t.Errorf("FilterSongAndKaraokeSubtitles total=%d, kept=%d; want total=4, kept=2", total, kept)
	}

	cleanedEntries, err := ParseSRT(outPath, "en")
	if err != nil {
		t.Fatalf("ParseSRT on cleaned file failed: %v", err)
	}

	if len(cleanedEntries) != 2 {
		t.Errorf("Parsed entries count = %d; want 2", len(cleanedEntries))
	}
}
