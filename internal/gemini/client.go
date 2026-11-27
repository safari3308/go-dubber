package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// DefaultModel is the default Google AI Gemini model alias used for subtitle translation and TTS cleanup.
const DefaultModel = "gemini-flash-latest"

// Supported subtitle processing / translation styles.
const (
	StyleStandard = "standard"
	StyleDonghua  = "donghua"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

var (
	emotionTagRe    = regexp.MustCompile(`(?i)\[\s*(cười|cuoi|chuckle|thở dài|tho dai|sigh|hắng giọng|hang giong|clear throat)\s*\]`)
	multiSpaceRe    = regexp.MustCompile(`\s{2,}`)
	spaceBeforePunc = regexp.MustCompile(`\s+([.,!?;:…])`)
)

// StripEmotionTags removes VieNeu inline emotion tags (e.g. [cười], [thở dài], [hắng giọng])
// for visual subtitles or engines that do not support them.
func StripEmotionTags(text string) string {
	cleaned := emotionTagRe.ReplaceAllString(text, " ")
	cleaned = spaceBeforePunc.ReplaceAllString(cleaned, "$1")
	cleaned = multiSpaceRe.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

// ProcessOptions configures how Gemini processes an SRT subtitle.
type ProcessOptions struct {
	TargetLang  string // "en" or "vi"
	Translate   bool   // Translate subtitles into TargetLang
	CleanForTTS bool   // Remove on-screen descriptions/signs/SFX and format naturally for TTS
	Style       string // "standard" (default) or "donghua" (Xianxia / Hán Việt)
}

// SRTCue represents a single parsed SRT subtitle entry with its original timestamp line.
type SRTCue struct {
	Index    int
	TimeLine string
	Text     string
}

type cuePayload struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// Client is a lightweight pure-Go client for the Google AI Gemini generateContent REST API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Gemini API client using the provided API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

var reSRTCue = regexp.MustCompile(`(?m)^(\d+)\r?\n(\d{2}:\d{2}:\d{2}[,\.]\d{3}\s*-->\s*\d{2}:\d{2}:\d{2}[,\.]\d{3}[^\r\n]*)\r?\n([\s\S]*?)(?:\r?\n\r?\n|\z)`)

// ParseSRTCues parses raw SRT text into ordered cues while preserving exact timestamp lines.
func ParseSRTCues(srtContent string) []SRTCue {
	// Strip UTF-8 BOM if present
	srtContent = strings.TrimPrefix(srtContent, "\xef\xbb\xbf")
	matches := reSRTCue.FindAllStringSubmatch(srtContent, -1)
	cues := make([]SRTCue, 0, len(matches))
	for i, m := range matches {
		txt := strings.TrimSpace(strings.ReplaceAll(m[3], "\r\n", "\n"))
		if txt == "" {
			continue
		}
		cues = append(cues, SRTCue{
			Index:    i + 1,
			TimeLine: strings.TrimSpace(m[2]),
			Text:     txt,
		})
	}
	return cues
}

// FormatSRTCues serializes cues back into valid SRT format, dropping empty cues and re-numbering sequentially.
func FormatSRTCues(cues []SRTCue) string {
	var blocks []string
	num := 1
	for _, c := range cues {
		txt := strings.TrimSpace(c.Text)
		if txt == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("%d\n%s\n%s\n", num, c.TimeLine, txt))
		num++
	}
	return strings.Join(blocks, "\n")
}

// ProcessSRTFile reads inPath, processes it with Gemini in batches, and writes the result to outPath.
func (c *Client) ProcessSRTFile(ctx context.Context, inPath, outPath string, opts ProcessOptions, onProgress func(done, total int)) (int, int, error) {
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read SRT file: %w", err)
	}

	cues := ParseSRTCues(string(raw))
	if len(cues) == 0 {
		return 0, 0, fmt.Errorf("no subtitle cues found in %s", inPath)
	}

	processed, err := c.ProcessCues(ctx, cues, opts, onProgress)
	if err != nil {
		return len(cues), 0, err
	}

	kept := 0
	for _, pc := range processed {
		if strings.TrimSpace(pc.Text) != "" {
			kept++
		}
	}

	outStr := FormatSRTCues(processed)
	if err := os.WriteFile(outPath, []byte(outStr), 0644); err != nil {
		return len(cues), 0, fmt.Errorf("failed to write processed SRT: %w", err)
	}
	return len(cues), kept, nil
}

// ProcessCues sends cues to Gemini in batches of 60 and returns updated cues with original timestamps preserved.
func (c *Client) ProcessCues(ctx context.Context, cues []SRTCue, opts ProcessOptions, onProgress func(done, total int)) ([]SRTCue, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("Gemini API key is not configured")
	}
	if !opts.Translate && !opts.CleanForTTS {
		return cues, nil
	}

	const batchSize = 35
	result := make([]SRTCue, len(cues))
	copy(result, cues)

	total := len(cues)
	var prevTail []cuePayload

	for start := 0; start < total; start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + batchSize
		if end > total {
			end = total
		}

		batch := make([]cuePayload, 0, end-start)
		for i := start; i < end; i++ {
			batch = append(batch, cuePayload{
				ID:   cues[i].Index,
				Text: cues[i].Text,
			})
		}

		updated, err := c.processBatch(ctx, batch, prevTail, opts)
		if err != nil {
			return nil, err
		}

		byID := make(map[int]string, len(updated))
		for _, u := range updated {
			byID[u.ID] = u.Text
		}
		var nextTail []cuePayload
		for i := start; i < end; i++ {
			if newText, ok := byID[cues[i].Index]; ok {
				result[i].Text = newText
				if strings.TrimSpace(newText) != "" {
					nextTail = append(nextTail, cuePayload{ID: cues[i].Index, Text: newText})
				}
			}
		}
		if len(nextTail) > 5 {
			prevTail = nextTail[len(nextTail)-5:]
		} else if len(nextTail) > 0 {
			prevTail = nextTail
		}

		if onProgress != nil {
			onProgress(end, total)
		}
	}

	return result, nil
}

func (c *Client) processBatch(ctx context.Context, batch []cuePayload, prevTail []cuePayload, opts ProcessOptions) ([]cuePayload, error) {
	inputJSON, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}

	prompt := buildPrompt(opts, prevTail) + "\n\nInput JSON:\n" + string(inputJSON)

	reqBody := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"temperature":      0.2,
			"thinkingConfig": map[string]any{
				"thinkingBudget": 0,
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimRight(c.baseURL, "/"), DefaultModel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini response: %w", err)
	}
	if len(apiResp.Candidates) == 0 || len(apiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini returned empty candidates")
	}

	rawText := strings.TrimSpace(apiResp.Candidates[0].Content.Parts[0].Text)
	rawText = strings.TrimPrefix(rawText, "```json")
	rawText = strings.TrimPrefix(rawText, "```")
	rawText = strings.TrimSuffix(rawText, "```")
	rawText = strings.TrimSpace(rawText)

	var out []cuePayload
	if err := json.Unmarshal([]byte(rawText), &out); err != nil {
		return nil, fmt.Errorf("failed to parse gemini JSON cues: %w", err)
	}
	return out, nil
}

func buildPrompt(opts ProcessOptions, prevTail []cuePayload) string {
	normLang := strings.ToLower(strings.TrimSpace(opts.TargetLang))
	langName := "Vietnamese"
	if normLang == "en" {
		langName = "English"
	}
	isDonghua := strings.ToLower(strings.TrimSpace(opts.Style)) == StyleDonghua

	var rules []string
	if isDonghua && normLang != "en" {
		rules = append(rules,
			"You are a Donghua (Chinese animation / Xianxia / Wuxia) subtitle translator and editor optimizing Vietnamese SRT cues specifically for Text-to-Speech (TTS) dubbing.",
			"You will receive a JSON array of subtitle cues: [{\"id\": number, \"text\": string}].",
			"Return ONLY a valid JSON array of objects with the exact same \"id\" values and updated \"text\" strings.",
			"",
			"Strict Filtering Rules (so TTS does NOT read metadata aloud):",
			"- Strip all translator notes (TN:, Note:, lore explanations), subtitler credits, Telegram/Discord handles, watermarks, group names, and disclaimer notices (e.g. 'Minors please watch with guardian...').",
			"- Strip all Aegisub/ASS override tags ({\\pos(...)}, {\\fn...}, {\\fad(...)}, karaoke \\k tags) and replace \\N line breaks with spaces or natural punctuation.",
			"- Exclude pure opening/ending credits, watermarks, and overlapping on-screen sign/TN blocks by setting their \"text\" to \"\" (empty string).",
		)
		if opts.Translate {
			rules = append(rules,
				"",
				"Classical Xianxia / Hán Việt Translation Style:",
				"- Translate spoken dialogue into classical Vietnamese rich in authentic Sino-Vietnamese (Hán Việt) cultivation terminology for realms, sects, titles, techniques, and artifacts (e.g., Bán Thần, Thiên Tôn, Đạo Hữu, Sư Tôn, Bản Tọa, Lão Phu, Đan Dược, Pháp Thân, Nguyên Anh, Kim Đan, Trúc Cơ).",
				"- Maintain consistent character names, sect names, and series-specific lore terminology across the entire episode.",
				"- Use appropriate classical pronouns based on character status and relationship (ta - ngươi, sư tôn - đồ nhi, tiền bối - vãn bối, bản tọa, tại hạ).",
			)
		}
		if opts.CleanForTTS {
			rules = append(rules,
				"",
				"TTS Flow Optimization:",
				"- Write complete, natural, flowing Vietnamese sentences that read smoothly aloud.",
				"- Avoid awkward ellipses (...) at the start or end of split lines that cause robotic TTS pauses; use clean commas or periods instead.",
				"- Avoid symbols, parentheses (), asterisks *, or brackets [] EXCEPT the 3 allowed VieNeu emotion tags below.",
				"- Spell out ambiguous abbreviations for clear pronunciation.",
			)
		}
		rules = append(rules,
			"",
			"VieNeu Emotion Tags (use ONLY these 3 exact tags when dialogue genuinely expresses them; do NOT invent other bracketed tags):",
			"- [cười] for chuckles/laughs (e.g. (laughs), haha, hehe, hì hì)",
			"- [thở dài] for sighs (e.g. (sighs), haizz)",
			"- [hắng giọng] for clearing throat (e.g. (clears throat), ahem, khụ khụ)",
			"- Keep thinking sounds like \"hmm\" / \"hừm\" as plain spoken text (e.g. \"Hừm,\"), NOT in brackets.",
		)
	} else {
		rules = append(rules,
			"You are a subtitle processor for an AI text-to-speech (TTS) dubbing pipeline.",
			"You will receive a JSON array of subtitle cues: [{\"id\": number, \"text\": string}].",
			"Return ONLY a valid JSON array of objects with the exact same \"id\" values and updated \"text\" strings.",
		)
		if opts.CleanForTTS {
			if normLang != "en" {
				rules = append(rules, "For TTS cleanup: remove on-screen sign translations, title cards, narrator/UI descriptions, non-vocal sound effects (e.g. [door slams]), music lyrics, translator notes, and speaker name prefixes. Keep only spoken dialogue, formatted cleanly for natural speech without awkward ellipses (...). If a cue contains ONLY non-spoken descriptions/signs/SFX/credits, set its \"text\" to \"\" (empty string).")
			} else {
				rules = append(rules, "For TTS cleanup: remove on-screen sign translations, title cards, narrator/UI descriptions, sound effects (e.g. [door slams], *sighs*), music lyrics, translator notes, and speaker name prefixes. Keep only spoken dialogue, formatted cleanly for natural speech without awkward ellipses (...) or brackets []. If a cue contains ONLY non-spoken descriptions/signs/SFX/credits, set its \"text\" to \"\" (empty string).")
			}
		}
		if opts.Translate {
			rules = append(rules, fmt.Sprintf("Translate all spoken dialogue naturally and conversationally into %s (keep proper names consistent).", langName))
		}
		if normLang != "en" {
			rules = append(rules,
				"",
				"VieNeu Emotion Tags (use ONLY these 3 exact tags when dialogue genuinely expresses them; do NOT invent other bracketed tags):",
				"- [cười] for chuckles/laughs (e.g. (laughs), haha, hehe, hì hì)",
				"- [thở dài] for sighs (e.g. (sighs), haizz)",
				"- [hắng giọng] for clearing throat (e.g. (clears throat), ahem, khụ khụ)",
				"- Keep thinking sounds like \"hmm\" / \"hừm\" as plain spoken text (e.g. \"Hừm,\"), NOT in brackets.",
			)
		}
	}

	if len(prevTail) > 0 {
		if tailJSON, err := json.Marshal(prevTail); err == nil {
			rules = append(rules, "", "Previous batch context (DO NOT include in output — use only to maintain consistent names, pronouns, and terminology):", string(tailJSON))
		}
	}

	return strings.Join(rules, "\n")
}
