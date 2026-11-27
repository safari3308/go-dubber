package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ASSStyle represents a parsed style entry from [V4+ Styles].
type ASSStyle struct {
	Name        string
	Fontname    string
	Fontsize    float64
	Alignment   int
	LineIndex   int
	FontsizeIdx int
	Parts       []string
	LeadSpace   string
}

// ASSEvent represents a single Dialogue line parsed from an ASS/SSA file.
type ASSEvent struct {
	ID          int    // 1-based sequential ID for AI batching
	LineIndex   int    // Index inside ASSDocument.Lines
	Prefix      string // Exact "Dialogue: Layer,Start,End,Style,...,Effect," prefix
	StartTime   string // ASS format H:MM:SS.cc
	EndTime     string // ASS format H:MM:SS.cc
	Style       string
	Name        string // Actor/Name field from [Events]
	LeadingTags string // Leading {...} override blocks preserved verbatim
	RawText     string // Raw text field including override tags
	PlainText   string // Stripped text sent to Gemini
	IsDrawing   bool   // True when line contains vector drawing {\p1}..{\p9}
}

// ASSDocument holds a losslessly parsed ASS/SSA file.
type ASSDocument struct {
	Lines    []string
	Events   []ASSEvent
	Styles   map[string]ASSStyle
	PlayResX int
	PlayResY int
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
	ID       int    `json:"id"`
	Style    string `json:"style,omitempty"`
	SignHint bool   `json:"sign,omitempty"`
	Text     string `json:"text"`
}

type assOutputCue struct {
	ID   int    `json:"id"`
	Type string `json:"t"`              // "d" = dialogue, "s" = sign/description, "m" = music/credits/TN
	Text string `json:"text,omitempty"` // omitted in filter-only mode to save ~85-90% output tokens
}

var (
	reASSLeadingTags  = regexp.MustCompile(`^(\{[^}]*\})+`)
	reASSTagBlock     = regexp.MustCompile(`\{[^}]*\}`)
	reASSDrawingTag   = regexp.MustCompile(`\{[^}]*\\p[1-9][^}]*\}`)
	reASSDrawingBlock = regexp.MustCompile(`(?i)\{[^}]*\\p[1-9][^}]*\}.*?(\{[^}]*\\p0[^}]*\}|$)`)
	reASSKaraokeTag  = regexp.MustCompile(`\{[^}]*\\[kK][fo]?\d+[^}]*\}`)
	reASSAlignTag    = regexp.MustCompile(`\\an([1-9])`)
	reASSSignTag     = regexp.MustCompile(`\{[^}]*\\(pos|move|fad|fade|frz|org)\b[^}]*\}`)
	reMusicStyleWord = regexp.MustCompile(`(?i)(^|[^a-z0-9])(op|ed|opening|ending|song|songs|lyric|lyrics|karaoke|kfx|romaji|kanji|credit|credits|watermark|staff|nhac)([^a-z0-9]|$)`)
	reSignStyleWord  = regexp.MustCompile(`(?i)(^|[^a-z0-9])(sign|signs|title|titles|caption|captions|note|notes|tn|hud|screen|eyecatch|next|card|label|4-normal|5-normal|6-normal)([^a-z0-9]|$)`)
	reTNOrCreditText = regexp.MustCompile(`(?i)^(\s*(t/?n|note|translator'?s?\s+note)\s*(\([^)]*\))?\s*:|subtitled\b|translated\s+by\b|uploaded\s+by\b|encoded\s+by\b|\[telegram\]|@\w*donghua\w*)`)
)

func isTNOrCreditLine(plain string) bool {
	s := strings.TrimSpace(plain)
	if s == "" {
		return false
	}
	if reTNOrCreditText.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "[telegram]") || strings.Contains(lower, "subtitled by") || strings.Contains(lower, "subtitled & uploaded")
}

// ParseASS parses an ASS/SSA document while retaining every non-event line, header, style, and override tag.
func ParseASS(content string) *ASSDocument {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	rawLines := strings.Split(content, "\n")

	doc := &ASSDocument{
		Lines:  rawLines,
		Styles: make(map[string]ASSStyle),
	}

	section := ""
	numFields := 10
	startIdx, endIdx, styleIdx, nameIdx := 1, 2, 3, 4
	styleNameIdx, styleFontIdx, styleSizeIdx, styleAlignIdx := 0, 1, 2, 18
	nextID := 1

	for lineIdx, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(trimmed)
			continue
		}

		switch section {
		case "[script info]":
			if strings.HasPrefix(strings.ToLower(trimmed), "playresx:") {
				val := strings.TrimSpace(trimmed[len("playresx:"):])
				doc.PlayResX, _ = strconv.Atoi(val)
			} else if strings.HasPrefix(strings.ToLower(trimmed), "playresy:") {
				val := strings.TrimSpace(trimmed[len("playresy:"):])
				doc.PlayResY, _ = strconv.Atoi(val)
			}

		case "[v4+ styles]", "[v4 styles]":
			if strings.HasPrefix(trimmed, "Format:") {
				cols := strings.Split(strings.TrimSpace(strings.TrimPrefix(trimmed, "Format:")), ",")
				for i, c := range cols {
					switch strings.ToLower(strings.TrimSpace(c)) {
					case "name":
						styleNameIdx = i
					case "fontname":
						styleFontIdx = i
					case "fontsize":
						styleSizeIdx = i
					case "alignment":
						styleAlignIdx = i
					}
				}
				continue
			}
			if !strings.HasPrefix(trimmed, "Style:") {
				continue
			}
			body := strings.TrimPrefix(line, "Style:")
			leadSpace := ""
			if strings.HasPrefix(body, " ") {
				leadSpace = " "
				body = body[1:]
			}
			parts := strings.Split(body, ",")
			if len(parts) <= styleSizeIdx || len(parts) <= styleNameIdx {
				continue
			}
			name := strings.TrimSpace(parts[styleNameIdx])
			font := ""
			if styleFontIdx < len(parts) {
				font = strings.TrimSpace(parts[styleFontIdx])
			}
			fsize, _ := strconv.ParseFloat(strings.TrimSpace(parts[styleSizeIdx]), 64)
			align := 2
			if styleAlignIdx < len(parts) {
				if a, err := strconv.Atoi(strings.TrimSpace(parts[styleAlignIdx])); err == nil {
					align = a
				}
			}
			doc.Styles[name] = ASSStyle{
				Name:        name,
				Fontname:    font,
				Fontsize:    fsize,
				Alignment:   align,
				LineIndex:   lineIdx,
				FontsizeIdx: styleSizeIdx,
				Parts:       parts,
				LeadSpace:   leadSpace,
			}

		case "[events]":
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
						case "name", "actor":
							nameIdx = i
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

			plain := reASSDrawingBlock.ReplaceAllString(textField, "")
			plain = reASSTagBlock.ReplaceAllString(plain, "")
			plain = strings.ReplaceAll(plain, `\N`, " ")
			plain = strings.ReplaceAll(plain, `\n`, " ")
			plain = strings.ReplaceAll(plain, `\h`, " ")
			plain = strings.TrimSpace(strings.Join(strings.Fields(plain), " "))
			if isDrawing && plain != "" {
				// Inline drawing mixed with real text: keep the cleaned text!
				isDrawing = false
			}

			actorName := ""
			if nameIdx < len(parts) {
				actorName = strings.TrimSpace(parts[nameIdx])
			}

			doc.Events = append(doc.Events, ASSEvent{
				ID:          nextID,
				LineIndex:   lineIdx,
				Prefix:      prefix,
				StartTime:   strings.TrimSpace(parts[startIdx]),
				EndTime:     strings.TrimSpace(parts[endIdx]),
				Style:       strings.TrimSpace(parts[styleIdx]),
				Name:        actorName,
				LeadingTags: leadingTags,
				RawText:     textField,
				PlainText:   plain,
				IsDrawing:   isDrawing,
			})
			nextID++
		}
	}

	return doc
}

// ASSTimeToCentiseconds converts an ASS timestamp ("0:02:33.30") into centiseconds for chronological sorting.
func ASSTimeToCentiseconds(assTime string) int {
	assTime = strings.TrimSpace(assTime)
	parts := strings.Split(assTime, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	secParts := strings.Split(parts[2], ".")
	s, _ := strconv.Atoi(secParts[0])
	cs := 0
	if len(secParts) == 2 {
		frac := secParts[1]
		switch len(frac) {
		case 1:
			cs, _ = strconv.Atoi(frac + "0")
		default:
			cs, _ = strconv.Atoi(frac[:2])
		}
	}
	return ((h*60+m)*60+s)*100 + cs
}

// looksUntranslatedToVI returns true when translating into Vietnamese produced text
// that is identical to the English source (or still pure ASCII English with >= 2 words and no Vietnamese diacritics).
func looksUntranslatedToVI(srcText, outText string) bool {
	src := strings.TrimSpace(srcText)
	out := strings.TrimSpace(outText)
	if out == "" {
		return true
	}
	// Count ASCII letters/words and check for any non-ASCII Vietnamese diacritics
	hasNonASCII := false
	for _, r := range out {
		if r > 127 {
			hasNonASCII = true
			break
		}
	}
	if hasNonASCII {
		return false
	}
	// If output is exact case-insensitive match to multi-word ASCII source, it was echoed untranslated
	words := strings.Fields(out)
	if len(words) >= 2 && strings.EqualFold(src, out) {
		return true
	}
	// Pure ASCII multi-word output (>= 3 words) with zero Vietnamese diacritics is almost certainly untranslated English
	if len(words) >= 3 {
		alphaWords := 0
		for _, w := range words {
			for _, r := range w {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					alphaWords++
					break
				}
			}
		}
		if alphaWords >= 3 {
			return true
		}
	}
	return false
}
// isPureMusicLine returns true when a subtitle line is clearly song/OP/ED lyrics wrapped in music notes (♪...♪ or ♫...♫).
func isPureMusicLine(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	runes := []rune(s)
	first, last := runes[0], runes[len(runes)-1]
	isNote := func(r rune) bool {
		return r == '♪' || r == '♫' || r == '♬' || r == '♩'
	}
	return isNote(first) && (len(runes) == 1 || isNote(last))
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

// isMusicStyle returns true when an ASS style name indicates OP/ED/song/karaoke/credits.
func isMusicStyle(styleName string) bool {
	s := strings.TrimSpace(styleName)
	if s == "" {
		return false
	}
	if reMusicStyleWord.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "nhạc") || strings.Contains(lower, "bài hát")
}

// isSignStyle returns true when an ASS style name indicates an on-screen sign, nameplate, or HUD card.
func isSignStyle(styleName string) bool {
	s := strings.TrimSpace(styleName)
	if s == "" {
		return false
	}
	if reSignStyleWord.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "thẻ") || strings.Contains(lower, "chú thích") || strings.Contains(lower, "biển")
}

func hasKaraokeTag(rawText string) bool {
	return reASSKaraokeTag.MatchString(rawText)
}

func effectiveAlignment(ev ASSEvent, styles map[string]ASSStyle) int {
	if m := reASSAlignTag.FindStringSubmatch(ev.RawText); len(m) == 2 {
		if a, err := strconv.Atoi(m[1]); err == nil {
			return a
		}
	}
	if st, ok := styles[ev.Style]; ok && st.Alignment > 0 {
		return st.Alignment
	}
	return 2 // ASS default bottom-center
}

// isLikelySignEvent returns true when style/name or non-bottom alignment + positioning/animation tags indicate an on-screen sign.
func isLikelySignEvent(ev ASSEvent, styles map[string]ASSStyle) bool {
	if isSignStyle(ev.Style) || isSignStyle(ev.Name) {
		return true
	}
	align := effectiveAlignment(ev, styles)
	// Alignments 4..9 are middle/top of screen; combined with \pos/\move/\fad/\frz they are almost always signs/cards.
	if align >= 4 && reASSSignTag.MatchString(ev.RawText) {
		return true
	}
	return false
}

func isStandardSubtitleFont(font string) bool {
	switch strings.ToLower(strings.TrimSpace(font)) {
	case "arial", "noto sans", "roboto", "helvetica", "sans-serif", "segoe ui", "trebuchet ms", "verdana", "tahoma", "open sans", "inter":
		return true
	default:
		return false
	}
}

// normalizeASSStyleLines caps oversized font sizes in [V4+ Styles] so missing custom fonts
// (e.g. narrow calligraphic fonts like UVN La Xanh at 89–111px on 1080p) don't render giant fallback text.
func normalizeASSStyleLines(doc *ASSDocument, outLines []string) {
	playResY := doc.PlayResY
	if playResY <= 0 {
		playResY = 1080
	}

	// Detect if any non-standard font style is oversized (> 8% of PlayResY, e.g. > 86px on 1080p)
	// and compute a uniform scale factor so relative hierarchy (Default vs Sign vs Song) is preserved.
	maxCustomSize := 0.0
	for _, st := range doc.Styles {
		if !isStandardSubtitleFont(st.Fontname) && st.Fontsize > maxCustomSize {
			maxCustomSize = st.Fontsize
		}
	}
	customScale := 1.0
	maxAllowedCustom := float64(playResY) * 0.068 // ~73px on 1080p
	if maxCustomSize > float64(playResY)*0.08 {
		customScale = maxAllowedCustom / maxCustomSize
	}

	maxSongSize := float64(playResY) * 0.045 // ~48px on 1080p

	for _, st := range doc.Styles {
		if st.LineIndex < 0 || st.LineIndex >= len(outLines) || st.Fontsize <= 0 {
			continue
		}
		newSize := st.Fontsize
		if !isStandardSubtitleFont(st.Fontname) && customScale < 1.0 {
			newSize = math.Round(newSize * customScale)
		}
		if isMusicStyle(st.Name) && newSize > maxSongSize {
			newSize = math.Round(maxSongSize)
		}
		if math.Abs(newSize-st.Fontsize) < 1.0 {
			continue
		}

		partsCopy := make([]string, len(st.Parts))
		copy(partsCopy, st.Parts)
		partsCopy[st.FontsizeIdx] = strconv.Itoa(int(newSize))
		outLines[st.LineIndex] = "Style:" + st.LeadSpace + strings.Join(partsCopy, ",")
	}
}

// ProcessASSLocal processes an ASS/SSA file purely locally (0 API tokens) using style, karaoke,
// music-note, and alignment/position heuristics to produce:
//   - outASSPath: fixed ASS (oversized styles normalized, song/OP/ED lines commented out when dropSongSubtitles is true)
//   - outSRTPath: clean TTS-only SRT with songs, drawings, and obvious signs excluded
func ProcessASSLocal(inASSPath, outASSPath, outSRTPath string, dropSongSubtitles bool) (ASSProcessResult, error) {
	raw, err := os.ReadFile(inASSPath)
	if err != nil {
		return ASSProcessResult{}, fmt.Errorf("failed to read ASS file: %w", err)
	}
	doc := ParseASS(string(raw))
	if len(doc.Events) == 0 {
		return ASSProcessResult{}, fmt.Errorf("no dialogue events found in ASS file %s", inASSPath)
	}

	classifications := make(map[int]assOutputCue, len(doc.Events))
	for _, ev := range doc.Events {
		if ev.IsDrawing || ev.PlainText == "" {
			continue
		}
		if isPureMusicLine(ev.PlainText) || hasKaraokeTag(ev.RawText) || isMusicStyle(ev.Style) || isMusicStyle(ev.Name) || isTNOrCreditLine(ev.PlainText) {
			classifications[ev.ID] = assOutputCue{ID: ev.ID, Type: "m", Text: ""}
			continue
		}
		if isLikelySignEvent(ev, doc.Styles) {
			classifications[ev.ID] = assOutputCue{ID: ev.ID, Type: "s", Text: ev.PlainText}
		}
	}

	return writeASSAndSRTOutputs(doc, classifications, outASSPath, outSRTPath, true, dropSongSubtitles)
}

// ProcessASSFile parses an ASS/SSA file natively, pre-filters obvious music/karaoke/signs locally,
// calls Gemini in compact JSON, and writes BOTH:
//   - outASSPath: styled ASS preserving headers, styles, positions, and translated signs/dialogue (with oversized styles normalized and optional song hiding)
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

	classifications := make(map[int]assOutputCue, len(doc.Events))

	// Collect only events that actually need AI processing (0 tokens spent on vector drawings,
	// pure ♪...♪ lyrics, karaoke-tagged lines, OP/ED/song/credits styles, or obvious signs in filter-only mode!)
	var activeEvents []ASSEvent
	for _, ev := range doc.Events {
		if ev.IsDrawing || ev.PlainText == "" {
			continue
		}
		if opts.CleanForTTS && (isPureMusicLine(ev.PlainText) || hasKaraokeTag(ev.RawText) || isMusicStyle(ev.Style) || isMusicStyle(ev.Name) || isTNOrCreditLine(ev.PlainText)) {
			classifications[ev.ID] = assOutputCue{
				ID:   ev.ID,
				Type: "m",
				Text: "",
			}
			continue
		}
		if filterOnly && isLikelySignEvent(ev, doc.Styles) {
			classifications[ev.ID] = assOutputCue{
				ID:   ev.ID,
				Type: "s",
				Text: ev.PlainText,
			}
			continue
		}
		activeEvents = append(activeEvents, ev)
	}

	// Sort active events chronologically before batching so out-of-order signs at the top of [Events]
	// don't scramble story context across batches.
	sort.SliceStable(activeEvents, func(i, j int) bool {
		return ASSTimeToCentiseconds(activeEvents[i].StartTime) < ASSTimeToCentiseconds(activeEvents[j].StartTime)
	})

	const batchSize = 30
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
				ID:       activeEvents[i].ID,
				Style:    activeEvents[i].Style,
				SignHint: isLikelySignEvent(activeEvents[i], doc.Styles),
				Text:     activeEvents[i].PlainText,
			})
		}

		outCues, err := c.processASSBatch(ctx, batch, prevTail, opts, filterOnly)
		if err != nil {
			return ASSProcessResult{}, err
		}

		for _, oc := range outCues {
			oc.Type = strings.ToLower(strings.TrimSpace(oc.Type))
			classifications[oc.ID] = oc
		}

		// In translation mode, verify that Gemini returned every requested ID with actual translated text
		// (catching omitted IDs, empty "d"/"s" text, AND untranslated English echoes when translating to VI).
		// Retry any missing/untranslated IDs up to 2 times so no sentences are silently skipped!
		if !filterOnly {
			targetIsVI := strings.ToLower(strings.TrimSpace(opts.TargetLang)) != "en"
			for retry := 0; retry < 2; retry++ {
				var missing []assInputCue
				for _, inp := range batch {
					oc, ok := classifications[inp.ID]
					if !ok {
						missing = append(missing, inp)
						continue
					}
					if oc.Type == "m" {
						continue
					}
					trimmed := strings.TrimSpace(oc.Text)
					if trimmed == "" || (opts.Translate && targetIsVI && looksUntranslatedToVI(inp.Text, trimmed)) {
						missing = append(missing, inp)
					}
				}
				if len(missing) == 0 {
					break
				}
				retryOut, rErr := c.processASSBatch(ctx, missing, prevTail, opts, false)
				if rErr != nil {
					if ctx.Err() != nil {
						return ASSProcessResult{}, ctx.Err()
					}
					break
				}
				for _, oc := range retryOut {
					oc.Type = strings.ToLower(strings.TrimSpace(oc.Type))
					if oc.Type == "m" || strings.TrimSpace(oc.Text) != "" {
						classifications[oc.ID] = oc
					}
				}
			}
		}

		var nextTail []cuePayload
		for _, inp := range batch {
			if oc, ok := classifications[inp.ID]; ok && !filterOnly && oc.Type == "d" && strings.TrimSpace(oc.Text) != "" {
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

	return writeASSAndSRTOutputs(doc, classifications, outASSPath, outSRTPath, filterOnly, opts.DropSongSubtitles)
}

func writeASSAndSRTOutputs(
	doc *ASSDocument,
	classifications map[int]assOutputCue,
	outASSPath string,
	outSRTPath string,
	filterOnly bool,
	dropSongSubtitles bool,
) (ASSProcessResult, error) {
	// Reconstruct BOTH outASSPath (styled with signs + dialogue) and outSRTPath (clean dialogue-only for TTS)
	outASSLines := make([]string, len(doc.Lines))
	copy(outASSLines, doc.Lines)

	// Normalize oversized custom-font & song styles so fallback fonts never render giant text.
	normalizeASSStyleLines(doc, outASSLines)

	type timedSRTCue struct {
		startCS int
		order   int
		cue     SRTCue
	}
	var timedCues []timedSRTCue
	res := ASSProcessResult{
		TotalEvents:    len(doc.Events),
		FilterOnlyMode: filterOnly,
	}

	for evIdx, ev := range doc.Events {
		if ev.IsDrawing || ev.PlainText == "" {
			res.SkippedCount++
			continue
		}

		oc, found := classifications[ev.ID]
		cat := "d"
		finalText := ev.PlainText

		if filterOnly {
			// In filter-only mode, Gemini/local rules only populate non-dialogue items ("s" or "m");
			// anything not flagged is spoken dialogue ("d")!
			if found && (oc.Type == "s" || oc.Type == "m") {
				cat = oc.Type
				if oc.Type == "m" && oc.Text == "" {
					finalText = ""
				}
			}
		} else if found {
			if oc.Type == "s" || oc.Type == "m" || oc.Type == "d" {
				cat = oc.Type
			}
			if trimmed := strings.TrimSpace(oc.Text); trimmed != "" {
				finalText = trimmed
			} else if cat == "m" {
				finalText = ""
			}
		}

		switch cat {
		case "d":
			if finalText != "" {
				res.DialogueCount++
				timedCues = append(timedCues, timedSRTCue{
					startCS: ASSTimeToCentiseconds(ev.StartTime),
					order:   evIdx,
					cue: SRTCue{
						TimeLine: fmt.Sprintf("%s --> %s", ASSTimeToSRT(ev.StartTime), ASSTimeToSRT(ev.EndTime)),
						Text:     finalText,
					},
				})
				if !filterOnly {
					// Strip VieNeu TTS emotion tags from the visual embedded ASS subtitle.
					visualText := StripEmotionTags(finalText)
					if visualText == "" {
						visualText = finalText
					}
					outASSLines[ev.LineIndex] = ev.Prefix + ev.LeadingTags + visualText
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
			if dropSongSubtitles || finalText == "" {
				// Comment out song/music/credits/TN lines in the embedded ASS so they don't clutter the screen.
				commentPrefix := strings.Replace(ev.Prefix, "Dialogue:", "Comment:", 1)
				if !filterOnly && finalText != "" {
					outASSLines[ev.LineIndex] = commentPrefix + ev.LeadingTags + finalText
				} else {
					outASSLines[ev.LineIndex] = strings.Replace(outASSLines[ev.LineIndex], "Dialogue:", "Comment:", 1)
				}
			} else if !filterOnly && finalText != "" {
				outASSLines[ev.LineIndex] = ev.Prefix + ev.LeadingTags + finalText
			}
		}
	}

	// Sort TTS SRT cues chronologically so out-of-order [Events] never break timeline overlap calculations.
	sort.SliceStable(timedCues, func(i, j int) bool {
		if timedCues[i].startCS != timedCues[j].startCS {
			return timedCues[i].startCS < timedCues[j].startCS
		}
		return timedCues[i].order < timedCues[j].order
	})
	srtCues := make([]SRTCue, len(timedCues))
	for i, tc := range timedCues {
		tc.cue.Index = i + 1
		srtCues[i] = tc.cue
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
		"Input is a JSON array: [{\"id\": number, \"style\": string, \"sign\"?: true, \"text\": string}]. \"sign\": true is a layout hint from non-bottom alignment/positioning tags.",
		"Return ONLY a JSON array: [{\"id\": number, \"t\": \"d\"|\"s\"|\"m\", \"text\": string}] with the exact same \"id\" values.",
		"CRITICAL: Return EVERY input id. Never omit an id, never leave source-language text untranslated, and never merge two consecutive ids into one (translate each cue individually, including continuation fragments, so timestamps stay aligned).",
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
			"- For \"d\" (dialogue): write smooth, natural spoken Vietnamese without awkward ellipses (...), parentheses (), asterisks *, or brackets [] EXCEPT the 3 allowed VieNeu emotion tags below.",
			"- VieNeu Emotion Tags (ONLY these 3 when dialogue genuinely expresses them): [cười] (chuckle/laugh), [thở dài] (sigh), [hắng giọng] (clear throat). Keep \"hmm\" / \"hừm\" as plain text, NOT in brackets.",
		)
	} else {
		rules = append(rules,
			fmt.Sprintf("Translate all \"d\" (dialogue) and \"s\" (signs/descriptions) naturally into %s.", langName),
			"For \"d\" (dialogue): strip speaker prefixes, parentheses (), and awkward ellipses (...) so TTS reads smoothly.",
		)
		if normLang != "en" {
			rules = append(rules,
				"For \"d\" (dialogue), you may use ONLY these 3 VieNeu emotion tags when genuinely expressed (no other brackets []): [cười] (chuckle/laugh), [thở dài] (sigh), [hắng giọng] (clear throat). Keep \"hmm\" / \"hừm\" as plain text.",
			)
		} else {
			rules = append(rules,
				"For \"d\" (dialogue), do NOT use any bracketed tags [] (Kokoro English reads bracketed words literally).",
			)
		}
	}

	if len(prevTail) > 0 {
		if tailJSON, err := json.Marshal(prevTail); err == nil {
			rules = append(rules, "", "Previous batch context (DO NOT include in output — use only for consistent names/pronouns):", string(tailJSON))
		}
	}

	return strings.Join(rules, "\n")
}
