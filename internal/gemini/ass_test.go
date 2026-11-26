package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleASS = `[Script Info]
Title: Donghua Sample
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1
Style: Sign,Arial,36,&H0000FFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,8,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.20,0:00:03.50,Default,,0,0,0,,Fellow Daoist, please stop!
Dialogue: 0,0:00:04.00,0:00:06.25,Sign,,0,0,0,,{\an8\pos(960,120)}Celestial Sect Gate
Dialogue: 0,0:00:07.00,0:00:09.00,Default,,0,0,0,,{\p1}m 0 0 l 100 0 100 100 0 100
Dialogue: 0,0:00:10.00,0:00:12.00,Default,,0,0,0,,TN: Celestial Sect is a top clan.
`

func TestASSTimeToSRT(t *testing.T) {
	tests := map[string]string{
		"0:00:01.20": "00:00:01,200",
		"0:02:33.30": "00:02:33,300",
		"1:12:05.05": "01:12:05,050",
	}
	for in, want := range tests {
		if got := ASSTimeToSRT(in); got != want {
			t.Errorf("ASSTimeToSRT(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestProcessASSFile_DualOutputTranslate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outCues := []assOutputCue{
			{ID: 1, Type: "d", Text: "Đạo hữu, xin dừng bước!"},
			{ID: 2, Type: "s", Text: "Cổng Thiên Tông"},
			{ID: 4, Type: "m", Text: ""},
		}
		outJSON, _ := json.Marshal(outCues)
		resp := map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]string{{"text": string(outJSON)}}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tempDir := t.TempDir()
	inASS := filepath.Join(tempDir, "in.ass")
	outASS := filepath.Join(tempDir, "out.ass")
	outSRT := filepath.Join(tempDir, "out.srt")
	_ = os.WriteFile(inASS, []byte(sampleASS), 0644)

	client := NewClient("test-key")
	client.baseURL = srv.URL

	res, err := client.ProcessASSFile(context.Background(), inASS, outASS, outSRT, ProcessOptions{
		TargetLang:  "vi",
		Translate:   true,
		CleanForTTS: true,
		Style:       StyleDonghua,
	}, nil)
	if err != nil {
		t.Fatalf("ProcessASSFile failed: %v", err)
	}
	if res.DialogueCount != 1 || res.SignCount != 1 {
		t.Fatalf("expected DialogueCount=1 SignCount=1, got %+v", res)
	}

	assOutBytes, _ := os.ReadFile(outASS)
	assOut := string(assOutBytes)
	if !strings.Contains(assOut, "{\\an8\\pos(960,120)}Cổng Thiên Tông") {
		t.Errorf("expected translated sign with preserved ASS tags in out.ass:\n%s", assOut)
	}
	if !strings.Contains(assOut, "Đạo hữu, xin dừng bước!") {
		t.Errorf("expected translated dialogue in out.ass:\n%s", assOut)
	}

	srtOutBytes, _ := os.ReadFile(outSRT)
	srtOut := string(srtOutBytes)
	if !strings.Contains(srtOut, "00:00:01,200 --> 00:00:03,500\nĐạo hữu, xin dừng bước!") {
		t.Errorf("expected clean dialogue in out.srt:\n%s", srtOut)
	}
	if strings.Contains(srtOut, "Cổng Thiên Tông") {
		t.Errorf("sign should NOT appear in TTS out.srt:\n%s", srtOut)
	}
}

func TestProcessASSFile_FilterOnlyModeSavesTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In filter-only mode, Gemini returns ONLY non-dialogue IDs without repeating text
		outCues := []assOutputCue{
			{ID: 2, Type: "s"},
			{ID: 4, Type: "m"},
		}
		outJSON, _ := json.Marshal(outCues)
		resp := map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]string{{"text": string(outJSON)}}}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tempDir := t.TempDir()
	inASS := filepath.Join(tempDir, "in.ass")
	outASS := filepath.Join(tempDir, "out.ass")
	outSRT := filepath.Join(tempDir, "out.srt")
	_ = os.WriteFile(inASS, []byte(sampleASS), 0644)

	client := NewClient("test-key")
	client.baseURL = srv.URL

	res, err := client.ProcessASSFile(context.Background(), inASS, outASS, outSRT, ProcessOptions{
		TargetLang:  "vi",
		Translate:   false,
		CleanForTTS: true,
	}, nil)
	if err != nil {
		t.Fatalf("ProcessASSFile filter-only failed: %v", err)
	}
	if !res.FilterOnlyMode || res.DialogueCount != 1 || res.SignCount != 1 {
		t.Fatalf("unexpected filter-only result: %+v", res)
	}

	srtOutBytes, _ := os.ReadFile(outSRT)
	srtOut := string(srtOutBytes)
	if !strings.Contains(srtOut, "Fellow Daoist, please stop!") {
		t.Errorf("expected dialogue kept in out.srt:\n%s", srtOut)
	}
	if strings.Contains(srtOut, "Celestial Sect") {
		t.Errorf("expected sign and TN excluded from out.srt:\n%s", srtOut)
	}
}
