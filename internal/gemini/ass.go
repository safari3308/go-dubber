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
	"strconv"
	"strings"
)

// ASSEvent represents a single Dialogue line parsed from an ASS/SSA file.
type ASSEvent struct {
	ID          int    // 1-based sequential ID for AI batching
	LineIndex   int    // Index inside ASSDocument.Lines
	Prefix      string // Exact "Dialogue: Layer,Start,End,Style,...,Effect," prefix
	StartTime   string // ASS format H:MM:SS.cc
	EndTime     string // ASS format H:MM:SS.cc
	Style       string
	LeadingTags string // Leading {...} override blocks preserved verbatim
	PlainText   string // Stripped text sent to Gemini
	IsDrawing   bool   // True when line contains vector drawing {\p1}..{\p9}
}

// ASSDocument holds a losslessly parsed ASS/SSA file.
type ASSDocument struct {
	Lines  []string
	Events []ASSEvent
}

// ASSProcessResult summarizes the dual-output ASS + SRT processing result.
type ASSProcessResult struct {
	TotalEvents    int
	DialogueCount  int
	SignCount      int
	SkippedCount   int
	FilterOnlyMode bool
}

type assInputCue struct {
	ID    int    `json:"id"`
	Style string `json:"style,omitempty"`
	Text  string `json:"text"`
}

type assOutputCue struct {
	ID   int    `json:"id"`
	Type string `json:"t"`              // "d" = dialogue, "s" = sign/description, "m" = music/credits/TN
	Text string `json:"text,omitempty"` // omitted in filter-only mode to save ~85-90% output tokens
}

var (
	reASSLeadingTags = regexp.MustCompile(`^(\{[^}]*\})+`)
	reASSTagBlock    = regexp.MustCompile(`\{[^}]*\}`)
	reASSDrawingTag  = regexp.MustCompile(`\{[^}]*\\p[1-9][^}]*\}`)
)

// ParseASS parses an ASS/SSA document while retaining every non-event line, header, style, and override tag.
func ParseASS(content string) *ASSDocument {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	rawLines := strings.Split(content, "\n")

	doc := &ASSDocument{
		Lines: rawLines,
	}

	inEvents := false
	numFields := 10
	startIdx, endIdx, styleIdx := 1, 2, 3
	nextID := 1

	for lineIdx, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inEvents = strings.EqualFold(trimmed, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}

		if strings.HasPrefix(trimmed, "Format:") {
			fmtSpec := strings.TrimSpace(strings.TrimPrefix(trimmed, "Format:"))
			cols := strings.Split(fmtSpec, ",")
			if len(cols) >= 4 {
				numFields = len(cols)
				for i, c := range cols {
					switch strings.ToLower(strings.TrimSpace(c)) {
					case "start":
						startIdx = i
					case "end":
						endIdx = i
					case "style":
						styleIdx = i
					}
				}
			}
			continue
		}

		if !strings.HasPrefix(trimmed, "Dialogue:") {
			continue
		}

		// Split on the first (numFields - 1) commas so commas inside Text are preserved
		body := strings.TrimPrefix(line, "Dialogue:")
		leadSpace := ""
		if strings.HasPrefix(body, " ") {
			leadSpace = " "
			body = body[1:]
		}

		parts := strings.SplitN(body, ",", numFields)
		if len(parts) < numFields {
			continue
		}

		textField := parts[numFields-1]
		prefix := "Dialogue:" + leadSpace + strings.Join(parts[:numFields-1], ",") + ","

		isDrawing := reASSDrawingTag.MatchString(textField)
		leadingTags := ""
		if m := reASSLeadingTags.FindString(textField); m != "" {
			leadingTags = m
		}

		plain := reASSTagBlock.ReplaceAllString(textField, "")
		plain = strings.ReplaceAll(plain, `\N`, " ")
		plain = strings.ReplaceAll(plain, `\n`, " ")
		plain = strings.ReplaceAll(plain, `\h`, " ")
		plain = strings.TrimSpace(strings.Join(strings.Fields(plain), " "))

		doc.Events = append(doc.Events, ASSEvent{
			ID:          nextID,
			LineIndex:   lineIdx,
			Prefix:      prefix,
			StartTime:   strings.TrimSpace(parts[startIdx]),
			EndTime:     strings.TrimSpace(parts[endIdx]),
			Style:       strings.TrimSpace(parts[styleIdx]),
			LeadingTags: leadingTags,
			PlainText:   plain,
			IsDrawing:   isDrawing,
		})
		nextID++
	}

	return doc
}

// ASSTimeToSRT converts an ASS timestamp ("0:02:33.30") into standard SRT format ("00:02:33,300").
func ASSTimeToSRT(assTime string) string {
	assTime = strings.TrimSpace(assTime)
	parts := strings.Split(assTime, ":")
	if len(parts) != 3 {
		return "00:00:00,000"
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])

	secParts := strings.Split(parts[2], ".")
	s, _ := strconv.Atoi(secParts[0])
	ms := 0
	if len(secParts) == 2 {
		frac := secParts[1]
		switch len(frac) {
		case 1:
			ms, _ = strconv.Atoi(frac + "00")
		case 2:
			ms, _ = strconv.Atoi(frac + "0")
		default:
			ms, _ = strconv.Atoi(frac[:3])
		}
	}
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// ProcessASSFile parses an ASS/SSA file natively, calls Gemini ONCE in compact JSON,
// and writes BOTH:
//   - outASSPath: styled ASS preserving headers, styles, positions, and translated signs/dialogue
//   - outSRTPath: clean TTS-only SRT containing strictly spoken dialogue (t == "d")
//
// When opts.Translate is false and opts.CleanForTTS is true, runs in ultra-low-token
// filter-only mode where Gemini returns only non-dialogue IDs (saving ~85-90% output tokens).
func (c *Client) ProcessASSFile(
	ctx context.Context,
	inASSPath, outASSPath, outSRTPath string,
	opts ProcessOptions,
	onProgress func(done, total int),
) (ASSProcessResult, error) {
	if c.apiKey == "" {
		return ASSProcessResult{}, fmt.Errorf("Gemini API key is not configured")
	}

	raw, err := os.ReadFile(inASSPath)
	if err != nil {
		return ASSProcessResult{}, fmt.Errorf("failed to read ASS file: %w", err)
	}

	doc := ParseASS(string(raw))
	if len(doc.Events) == 0 {
		return ASSProcessResult{}, fmt.Errorf("no dialogue events found in ASS file %s", inASSPath)
	}

	filterOnly := !opts.Translate && opts.CleanForTTS

	// Collect only non-empty, non-drawing events for AI processing (0 tokens spent on vector drawings!)
	var activeEvents []ASSEvent
	for _, ev := range doc.Events {
		if ev.IsDrawing || ev.PlainText == "" {
			continue
		}
		activeEvents = append(activeEvents, ev)
	}

	classifications := make(map[int]assOutputCue, len(activeEvents))
	const batchSize = 40
	total := len(activeEvents)
	var prevTail []cuePayload

	for start := 0; start < total; start += batchSize {
		if err := ctx.Err(); err != nil {
			return ASSProcessResult{}, err
		}
		end := start + batchSize
		if end > total {
			end = total
		}

		batch := make([]assInputCue, 0, end-start)
		for i := start; i < end; i++ {
			batch = append(batch, assInputCue{
				ID:    activeEvents[i].ID,
				Style: activeEvents[i].Style,
				Text:  activeEvents[i].PlainText,
			})
		}

		outCues, err := c.processASSBatch(ctx, batch, prevTail, opts, filterOnly)
		if err != nil {
			return ASSProcessResult{}, err
		}

		var nextTail []cuePayload
		for _, oc := range outCues {
			oc.Type = strings.ToLower(strings.TrimSpace(oc.Type))
			classifications[oc.ID] = oc
			if !filterOnly && oc.Type == "d" && strings.TrimSpace(oc.Text) != "" {
				nextTail = append(nextTail, cuePayload{ID: oc.ID, Text: oc.Text})
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

	// Reconstruct BOTH outASSPath (styled with signs + dialogue) and outSRTPath (clean dialogue-only for TTS)
	outASSLines := make([]string, len(doc.Lines))
	copy(outASSLines, doc.Lines)

	var srtCues []SRTCue
	res := ASSProcessResult{
		TotalEvents:    len(doc.Events),
		FilterOnlyMode: filterOnly,
	}

	for _, ev := range doc.Events {
		if ev.IsDrawing || ev.PlainText == "" {
			res.SkippedCount++
			continue
		}

		oc, found := classifications[ev.ID]
		cat := "d"
		finalText := ev.PlainText

		if filterOnly {
			// In filter-only mode, Gemini only returns non-dialogue items ("s" or "m");
			// anything not flagged is spoken dialogue ("d")!
			if found && (oc.Type == "s" || oc.Type == "m") {
				cat = oc.Type
			}
		} else if found {
			if oc.Type == "s" || oc.Type == "m" || oc.Type == "d" {
				cat = oc.Type
			}
			finalText = strings.TrimSpace(oc.Text)
		}

		switch cat {
		case "d":
			if finalText != "" {
				res.DialogueCount++
				srtCues = append(srtCues, SRTCue{
					Index:    len(srtCues) + 1,
					TimeLine: fmt.Sprintf("%s --> %s", ASSTimeToSRT(ev.StartTime), ASSTimeToSRT(ev.EndTime)),
					Text:     finalText,
				})
				if !filterOnly {
					outASSLines[ev.LineIndex] = ev.Prefix + ev.LeadingTags + finalText
				}
			} else {
				res.SkippedCount++
			}
		case "s":
			res.SignCount++
			if !filterOnly && finalText != "" {
				// Preserve signs/descriptions in the embedded .ass with their original styles & positions!
				outASSLines[ev.LineIndex] = ev.Prefix + ev.LeadingTags + finalText
			}
		default: // "m" (music, lyrics, credits, TN)
			res.SkippedCount++
			if !filterOnly && finalText != "" {
				outASSLines[ev.LineIndex] = ev.Prefix + ev.LeadingTags + finalText
			}
		}
	}

	if outASSPath != "" {
		if err := os.WriteFile(outASSPath, []byte(strings.Join(outASSLines, "\n")), 0644); err != nil {
			return res, fmt.Errorf("failed to write output ASS: %w", err)
		}
	}
	if outSRTPath != "" {
		if err := os.WriteFile(outSRTPath, []byte(FormatSRTCues(srtCues)), 0644); err != nil {
			return res, fmt.Errorf("failed to write output TTS SRT: %w", err)
		}
	}

	return res, nil
}

func (c *Client) processASSBatch(
	ctx context.Context,
	batch []assInputCue,
	prevTail []cuePayload,
	opts ProcessOptions,
	filterOnly bool,
) ([]assOutputCue, error) {
	inputJSON, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}

	prompt := buildASSPrompt(opts, prevTail, filterOnly) + "\n\nInput JSON:\n" + string(inputJSON)

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
		return nil, fmt.Errorf("gemini ASS request failed: %w", err)
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

	var out []assOutputCue
	if err := json.Unmarshal([]byte(rawText), &out); err != nil {
		return nil, fmt.Errorf("failed to parse gemini ASS JSON cues: %w", err)
	}
	return out, nil
}

func buildASSPrompt(opts ProcessOptions, prevTail []cuePayload, filterOnly bool) string {
	normLang := strings.ToLower(strings.TrimSpace(opts.TargetLang))
	langName := "Vietnamese"
	if normLang == "en" {
		langName = "English"
	}
	isDonghua := strings.ToLower(strings.TrimSpace(opts.Style)) == StyleDonghua

	if filterOnly {
		return strings.Join([]string{
			"You are classifying ASS subtitle events so a TTS voiceover engine ONLY reads spoken dialogue.",
			"Input is a JSON array: [{\"id\": number, \"style\": string, \"text\": string}].",
			"Classify non-dialogue cues using:",
			"- \"s\" = on-screen sign, title card, location/character nameplate, UI/stat description",
			"- \"m\" = song/OP/ED lyrics, translator note (TN:, Note:), credits, watermark, disclaimer, pure sound effects ([door slams])",
			"To minimize output tokens, return ONLY a JSON array of NON-DIALOGUE items: [{\"id\": number, \"t\": \"s\"|\"m\"}].",
			"Do NOT include spoken dialogue items and do NOT repeat the text. If all items are spoken dialogue, return [].",
		}, "\n")
	}

	var rules []string
	rules = append(rules,
		"You are translating and classifying ASS subtitle events for a dual-output dubbing pipeline (styled ASS video subtitles + clean TTS voiceover).",
		"Input is a JSON array: [{\"id\": number, \"style\": string, \"text\": string}].",
		"Return ONLY a JSON array: [{\"id\": number, \"t\": \"d\"|\"s\"|\"m\", \"text\": string}] with the exact same \"id\" values.",
		"Category \"t\":",
		"- \"d\" = spoken dialogue (will be read aloud by TTS AND shown in ASS)",
		"- \"s\" = on-screen sign, title card, location/character nameplate, or visual description (will be translated and kept in ASS, but NOT read by TTS)",
		"- \"m\" = song/OP/ED lyrics, translator notes (TN:, Note:), credits, watermarks, disclaimers, or pure sound effects (NOT read by TTS; set text to \"\" for TN/credits/watermarks/disclaimers).",
	)

	if isDonghua && normLang != "en" {
		rules = append(rules,
			"",
			"Donghua / Xianxia (Hán Việt) Translation Style:",
			"- Translate into classical Vietnamese rich in authentic Sino-Vietnamese (Hán Việt) cultivation terminology (Bán Thần, Thiên Tôn, Đạo Hữu, Sư Tôn, Bản Tọa, Lão Phu, Đan Dược, Pháp Thân, Nguyên Anh, Kim Đan, Trúc Cơ).",
			"- Keep character names, sect names, and pronouns (ta - ngươi, sư tôn - đồ nhi, tiền bối - vãn bối, bản tọa, tại hạ) consistent.",
			"- For \"d\" (dialogue): write smooth, natural spoken Vietnamese without awkward ellipses (...), brackets [], (), or asterisks *.",
		)
	} else {
		rules = append(rules,
			fmt.Sprintf("Translate all \"d\" (dialogue) and \"s\" (signs/descriptions) naturally into %s.", langName),
			"For \"d\" (dialogue): strip speaker prefixes, brackets [], (), and awkward ellipses (...) so TTS reads smoothly.",
		)
	}

	if len(prevTail) > 0 {
		if tailJSON, err := json.Marshal(prevTail); err == nil {
			rules = append(rules, "", "Previous batch context (DO NOT include in output — use only for consistent names/pronouns):", string(tailJSON))
		}
	}

	return strings.Join(rules, "\n")
}
