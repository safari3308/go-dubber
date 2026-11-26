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

func TestProcessSRTFile_TranslateAndClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, DefaultModel+":generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("unexpected x-goog-api-key header: %s", r.Header.Get("x-goog-api-key"))
		}

		outCues := []cuePayload{
			{ID: 1, Text: "Xin chào!"},
			{ID: 2, Text: ""}, // Dropped on-screen description/SFX
			{ID: 3, Text: "Chúng ta đi thôi."},
		}
		outJSON, _ := json.Marshal(outCues)

		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]string{
							{"text": string(outJSON)},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tempDir := t.TempDir()
	inSRT := filepath.Join(tempDir, "in.srt")
	outSRT := filepath.Join(tempDir, "out.srt")

	content := `1
00:00:01,000 --> 00:00:03,000
Hello!

2
00:00:04,000 --> 00:00:05,500
[SIGN: Academy Gate]

3
00:00:06,000 --> 00:00:08,000
Let's go.
`
	if err := os.WriteFile(inSRT, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write input srt: %v", err)
	}

	client := NewClient("test-key")
	client.baseURL = srv.URL

	total, kept, err := client.ProcessSRTFile(context.Background(), inSRT, outSRT, ProcessOptions{
		TargetLang:  "vi",
		Translate:   true,
		CleanForTTS: true,
	}, nil)
	if err != nil {
		t.Fatalf("ProcessSRTFile failed: %v", err)
	}
	if total != 3 || kept != 2 {
		t.Fatalf("expected total=3 kept=2, got total=%d kept=%d", total, kept)
	}

	gotBytes, err := os.ReadFile(outSRT)
	if err != nil {
		t.Fatalf("failed to read output srt: %v", err)
	}
	got := string(gotBytes)
	if !strings.Contains(got, "00:00:01,000 --> 00:00:03,000\nXin chào!") {
		t.Errorf("missing cue 1 in output:\n%s", got)
	}
	if strings.Contains(got, "Academy Gate") {
		t.Errorf("dropped cue 2 should not appear in output:\n%s", got)
	}
	if !strings.Contains(got, "2\n00:00:06,000 --> 00:00:08,000\nChúng ta đi thôi.") {
		t.Errorf("cue 3 should be renumbered to 2 with original timestamp:\n%s", got)
	}
}

func TestBuildPrompt_DonghuaStyle(t *testing.T) {
	p := buildPrompt(ProcessOptions{
		TargetLang:  "vi",
		Translate:   true,
		CleanForTTS: true,
		Style:       StyleDonghua,
	}, []cuePayload{{ID: 1, Text: "Đạo hữu xin dừng bước."}})

	for _, want := range []string{"Hán Việt", "Xianxia", "Sư Tôn", "TN:", "Previous batch context", "Đạo hữu xin dừng bước."} {
		if !strings.Contains(p, want) {
			t.Errorf("expected Donghua prompt to contain %q", want)
		}
	}
}
