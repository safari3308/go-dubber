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

func TestProcessASSFile_RetriesMissingIDsAndSortsAndSkipsMusic(t *testing.T) {
	// Out-of-order ASS: sign at 0:04:56 appears BEFORE dialogue at 0:00:11, plus ♪...♪ OP lyrics.
	assContent := `[Script Info]
ScriptType: v4.00+

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:04:56.81,0:05:01.78,6-normal,,0,0,0,,{\pos(1810,880)}Episode 22: Hunter Kelt
Dialogue: 0,0:00:11.22,0:00:13.90,Default,,0,0,0,,Graveyard of Grief, entrance.
Dialogue: 0,0:00:16.55,0:00:19.62,Default,,0,0,0,,Something weird is happening here.
Dialogue: 0,0:03:31.50,0:03:36.50,Default,,0,0,0,,♪Rewrite the Answer♪
`
	calls := 0
	var seenBatches [][]assInputCue
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		promptText := req.Contents[0].Parts[0].Text
		idx := strings.Index(promptText, "Input JSON:\n")
		var inCues []assInputCue
		if idx >= 0 {
			_ = json.Unmarshal([]byte(promptText[idx+len("Input JSON:\n"):]), &inCues)
		}
		seenBatches = append(seenBatches, inCues)

		var outCues []assOutputCue
		if calls == 1 {
			// Simulate Gemini dropping the 2nd dialogue cue (ID=3) on the first pass!
			outCues = []assOutputCue{
				{ID: 2, Type: "d", Text: "Lối vào Nghĩa Địa Đau Khổ."},
				{ID: 1, Type: "s", Text: "Tập 22: Thợ Săn Kelt"},
			}
		} else {
			// Retry pass returns the missing ID=3
			outCues = []assOutputCue{
				{ID: 3, Type: "d", Text: "Có chuyện kỳ lạ đang xảy ra ở đây."},
			}
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
	_ = os.WriteFile(inASS, []byte(assContent), 0644)

	client := NewClient("test-key")
	client.baseURL = srv.URL

	res, err := client.ProcessASSFile(context.Background(), inASS, outASS, outSRT, ProcessOptions{
		TargetLang:  "vi",
		Translate:   true,
		CleanForTTS: true,
	}, nil)
	if err != nil {
		t.Fatalf("ProcessASSFile failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 Gemini calls (initial + 1 retry for missing ID), got %d", calls)
	}
	// Pure ♪...♪ line (ID=4) should NOT have been sent to Gemini at all
	if len(seenBatches[0]) != 3 {
		t.Fatalf("expected 3 cues sent to Gemini (music pre-filtered), got %d", len(seenBatches[0]))
	}
	// First batch should be sorted chronologically (ID 2 at 0:00:11 before ID 1 at 0:04:56)
	if seenBatches[0][0].ID != 2 || seenBatches[0][2].ID != 1 {
		t.Errorf("expected chronological batch order [2,3,1], got %+v", seenBatches[0])
	}
	// Retry batch should contain ONLY the missing ID=3
	if len(seenBatches[1]) != 1 || seenBatches[1][0].ID != 3 {
		t.Errorf("expected retry batch with only ID=3, got %+v", seenBatches[1])
	}
	if res.DialogueCount != 2 || res.SignCount != 1 || res.SkippedCount != 1 {
		t.Errorf("unexpected counts: %+v", res)
	}

	srtOutBytes, _ := os.ReadFile(outSRT)
	srtOut := string(srtOutBytes)
	if !strings.Contains(srtOut, "Lối vào Nghĩa Địa Đau Khổ.") || !strings.Contains(srtOut, "Có chuyện kỳ lạ đang xảy ra ở đây.") {
		t.Errorf("missing translated dialogue in out.srt:\n%s", srtOut)
	}
	if strings.Contains(srtOut, "Rewrite the Answer") {
		t.Errorf("music lyric should be excluded from out.srt:\n%s", srtOut)
	}
}

func TestProcessASSLocal_NormalizesOversizedStylesAndHidesSongs(t *testing.T) {
	dptkASS := `[Script Info]
Title: Đấu Phá Thương Khung
ScriptType: v4.00+
PlayResX: 2580
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,UVN La Xanh,111,&H00FFFFFF,&H0000FFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,4,4,2,8,8,154,1
Style: ED ĐPTK,UVN La Xanh,89,&H00FFFFFF,&H0000FFFF,&H00000000,&H00000000,-1,-1,0,0,100,100,0,0,1,3,2,8,8,8,40,1
Style: Thẻ nhân vật,UVN La Xanh,84,&H0000E1FF,&H0000FFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,3,3,4,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:31.43,0:00:34.90,Default,,0,0,0,,Vạn năm trước, Đà Xá Cổ Đế
Dialogue: 0,0:02:30.07,0:02:34.27,ED ĐPTK,,0,0,130,,♪ Trời xa hửng sáng, tiếng gió vọng vang ♪
Dialogue: 0,0:02:35.00,0:02:38.00,Default,,0,0,0,,{\k40}Karaoke {\k50}line
Dialogue: 1,0:06:07.00,0:06:09.20,Thẻ nhân vật,,0,0,0,,{\an4\pos(645,513)\fad(150,150)}Hồn U\N{\fs62\c&H00FFFFFF&}Nhất Tinh Đấu Thánh
`
	tempDir := t.TempDir()
	inASS := filepath.Join(tempDir, "dptk.ass")
	outASS := filepath.Join(tempDir, "dptk_fixed.ass")
	outSRT := filepath.Join(tempDir, "dptk_tts.srt")
	_ = os.WriteFile(inASS, []byte(dptkASS), 0644)

	res, err := ProcessASSLocal(inASS, outASS, outSRT, true)
	if err != nil {
		t.Fatalf("ProcessASSLocal failed: %v", err)
	}
	if res.DialogueCount != 1 || res.SignCount != 1 || res.SkippedCount != 2 {
		t.Fatalf("unexpected local ASS counts: %+v (want dialogue=1, sign=1, skipped=2)", res)
	}

	assBytes, _ := os.ReadFile(outASS)
	assOut := string(assBytes)

	// Oversized UVN La Xanh styles should be scaled down proportionally, and ED style capped <= 48px
	if strings.Contains(assOut, "UVN La Xanh,111,") || strings.Contains(assOut, "UVN La Xanh,89,") {
		t.Errorf("expected oversized UVN La Xanh font sizes to be normalized:\n%s", assOut)
	}
	if !strings.Contains(assOut, "Style: ED ĐPTK,UVN La Xanh,49,") && !strings.Contains(assOut, "Style: ED ĐPTK,UVN La Xanh,48,") {
		t.Errorf("expected ED ĐPTK font size capped to ~48-49:\n%s", assOut)
	}

	// Song & karaoke lines should be commented out in embedded ASS
	if !strings.Contains(assOut, "Comment: 0,0:02:30.07,0:02:34.27,ED ĐPTK,") {
		t.Errorf("expected ED ĐPTK song line converted to Comment: in out.ass:\n%s", assOut)
	}
	if !strings.Contains(assOut, "Comment: 0,0:02:35.00,0:02:38.00,Default,") {
		t.Errorf("expected karaoke line converted to Comment: in out.ass:\n%s", assOut)
	}

	// Character card sign should remain visible Dialogue: in embedded ASS
	if !strings.Contains(assOut, "Dialogue: 1,0:06:07.00,0:06:09.20,Thẻ nhân vật,") {
		t.Errorf("expected Thẻ nhân vật sign kept as Dialogue: in out.ass:\n%s", assOut)
	}

	// TTS SRT should contain ONLY the spoken dialogue
	srtBytes, _ := os.ReadFile(outSRT)
	srtOut := string(srtBytes)
	if !strings.Contains(srtOut, "Vạn năm trước, Đà Xá Cổ Đế") {
		t.Errorf("expected spoken dialogue in out.srt:\n%s", srtOut)
	}
	if strings.Contains(srtOut, "Trời xa hửng sáng") || strings.Contains(srtOut, "Hồn U") || strings.Contains(srtOut, "Karaoke") {
		t.Errorf("expected songs, karaoke, and signs excluded from out.srt:\n%s", srtOut)
	}
}

func TestProcessASSFile_RetriesUntranslatedEnglishEcho(t *testing.T) {
	assContent := `[Script Info]
ScriptType: v4.00+

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:09:07.50,0:09:08.47,Default,,0,0,0,,Li Qing
Dialogue: 0,0:09:08.97,0:09:09.80,Default,,0,0,0,,pays homage to the Preceptor.
`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var outCues []assOutputCue
		if calls == 1 {
			// Simulate Gemini echoing line 2 back in English untranslated!
			outCues = []assOutputCue{
				{ID: 1, Type: "d", Text: "Thuộc hạ Lý Thanh,"},
				{ID: 2, Type: "d", Text: "pays homage to the Preceptor."},
			}
		} else {
			// Retry translates line 2 properly into Vietnamese
			outCues = []assOutputCue{
				{ID: 2, Type: "d", Text: "bái kiến Quốc Sư."},
			}
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

	tmp := t.TempDir()
	inASS := filepath.Join(tmp, "in.ass")
	outASS := filepath.Join(tmp, "out.ass")
	outSRT := filepath.Join(tmp, "out.srt")
	_ = os.WriteFile(inASS, []byte(assContent), 0644)

	client := NewClient("test-key")
	client.baseURL = srv.URL

	_, err := client.ProcessASSFile(context.Background(), inASS, outASS, outSRT, ProcessOptions{
		TargetLang:  "vi",
		Translate:   true,
		CleanForTTS: true,
		Style:       StyleDonghua,
	}, nil)
	if err != nil {
		t.Fatalf("ProcessASSFile failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 Gemini calls (initial + retry for untranslated English echo), got %d", calls)
	}
	assBytes, _ := os.ReadFile(outASS)
	if !strings.Contains(string(assBytes), "bái kiến Quốc Sư.") {
		t.Errorf("expected retried Vietnamese translation in out.ass:\n%s", string(assBytes))
	}
}
