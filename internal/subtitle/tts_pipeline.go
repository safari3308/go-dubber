package subtitle

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/asticode/go-astisub"
)

// ParseOptions configures subtitle parsing, filtering, deduplication, and optional debug observability.
type ParseOptions struct {
	Lang   string
	Debug  bool
	Logger func(reason string, startSec, endSec float64, detail string)
}

func (o ParseOptions) log(reason string, startSec, endSec float64, detail string) {
	if !o.Debug {
		return
	}
	if o.Logger != nil {
		o.Logger(reason, startSec, endSec, detail)
		return
	}
	fmt.Printf("[subtitle:%s] %.2fs-%.2fs %s\n", reason, startSec, endSec, detail)
}

var (
	// Strips inline ASS vector drawing blocks between {\p1..9} and {\p0} (or end of line).
	reASSDrawingBlock = regexp.MustCompile(`(?i)\{[^}]*\\p[1-9][^}]*\}.*?(\{[^}]*\\p0[^}]*\}|$)`)
	// Matches non-dialogue ASS Style or Name keywords (case-insensitive, token-bounded).
	reNonDialogueMeta = regexp.MustCompile(`(?i)(^|[^a-z0-9])(sign|signs|title|titles|op|ed|opening|ending|song|songs|lyric|lyrics|sfx|fx|note|notes|tl|tn|credit|credits|karaoke|kfx|romaji|kanji|watermark|caption|captions|hud|eyecatch|staff)([^a-z0-9]|$)`)
	// Matches bracketed/parenthesized sound or action descriptions like [door opens], (music), [tiếng bước chân].
	reBracketDesc = regexp.MustCompile(`\[[^\]]*\]|\([^\)]*\)`)
	// Preserves the 3 VieNeu Vietnamese inline emotion tags when stripping [...] descriptions.
	reVieNeuEmotion = regexp.MustCompile(`(?i)^\[\s*(cười|cuoi|thở dài|tho dai|hắng giọng|hang giong)\s*\]$`)
)

// StripVectorDrawings removes ASS vector drawing sequences between \p1..9 and \p0
// while preserving any normal dialogue text outside the drawing block.
func StripVectorDrawings(text string) string {
	return reASSDrawingBlock.ReplaceAllString(text, "")
}

// StripBracketDescriptions removes sound/action descriptions enclosed in [...] and (...).
// For Vietnamese TTS, preserves VieNeu's 3 inline emotion tags ([cười], [thở dài], [hắng giọng]).
func StripBracketDescriptions(text string, lang string) string {
	normLang := strings.ToLower(strings.TrimSpace(lang))
	isVI := normLang == "vi" || normLang == "vie" || normLang == "vn" || normLang == "vietnamese"

	return reBracketDesc.ReplaceAllStringFunc(text, func(match string) string {
		if isVI && reVieNeuEmotion.MatchString(match) {
			return match
		}
		return " "
	})
}

func isNonDialogueMetadata(val string) bool {
	s := strings.TrimSpace(val)
	if s == "" {
		return false
	}
	if reNonDialogueMeta.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "thẻ") || strings.Contains(lower, "chú thích") || strings.Contains(lower, "nhạc")
}

// ParseSubtitleForTTS loads a subtitle file (.ass/.ssa directly, or .srt/.vtt via go-astisub),
// applies noise filtering, text cleaning, deduplication, and anti-overlap timeline resolution.
func ParseSubtitleForTTS(path string, opts ParseOptions) ([]SubEntry, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".ass" || ext == ".ssa" {
		return ParseASSForTTS(path, opts)
	}
	return ParseSRTWithOptions(path, opts)
}

// ParseASSForTTS parses an ASS/SSA file's [Events] section directly so Layer, Style, Name,
// and exact timestamps are preserved without lossy .ass -> .srt conversion.
func ParseASSForTTS(assPath string, opts ParseOptions) ([]SubEntry, error) {
	raw, err := os.ReadFile(assPath)
	if err != nil {
		return nil, err
	}
	content := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	content = strings.ReplaceAll(content, "\r\n", "\n")

	inEvents := false
	numFields := 10
	startIdx, endIdx, styleIdx, nameIdx := 1, 2, 3, 4

	var entries []SubEntry
	seq := 0

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inEvents = strings.EqualFold(trimmed, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}

		if strings.HasPrefix(trimmed, "Format:") {
			cols := strings.Split(strings.TrimSpace(strings.TrimPrefix(trimmed, "Format:")), ",")
			if len(cols) >= 5 {
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

		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "Dialogue:"))
		parts := strings.SplitN(body, ",", numFields)
		if len(parts) < numFields {
			continue
		}

		startSec := assTimeToSeconds(parts[startIdx])
		endSec := assTimeToSeconds(parts[endIdx])
		style := strings.TrimSpace(parts[styleIdx])
		name := ""
		if nameIdx < len(parts) {
			name = strings.TrimSpace(parts[nameIdx])
		}
		rawText := parts[numFields-1]

		if isNonDialogueMetadata(style) {
			opts.log("filtered_style", startSec, endSec, fmt.Sprintf("style=%q text=%q", style, rawText))
			continue
		}
		if isNonDialogueMetadata(name) {
			opts.log("filtered_name", startSec, endSec, fmt.Sprintf("name=%q text=%q", name, rawText))
			continue
		}
		if IsSongOrKaraokeLine(rawText, startSec, endSec) {
			opts.log("filtered_song", startSec, endSec, rawText)
			continue
		}

		cleaned := cleanRawSubtitleText(rawText, opts.Lang)
		if cleaned == "" {
			opts.log("empty_after_clean", startSec, endSec, rawText)
			continue
		}

		seq++
		entries = append(entries, SubEntry{
			Index:    seq,
			StartSec: startSec,
			EndSec:   endSec,
			Text:     cleaned,
		})
	}

	return DeduplicateAndResolveOverlaps(entries, opts), nil
}

// ParseSRTWithOptions parses an SRT/VTT file using go-astisub (with regex fallback for non-standard SRTs)
// and applies noise filtering, deduplication, and anti-overlap timeline resolution.
func ParseSRTWithOptions(srtPath string, opts ParseOptions) ([]SubEntry, error) {
	type rawCue struct {
		startSec float64
		endSec   float64
		rawText  string
	}
	var rawCues []rawCue

	// Prefer go-astisub for standard SRT/VTT parsing
	if subs, err := astisub.OpenFile(srtPath); err == nil && subs != nil && len(subs.Items) > 0 {
		for _, item := range subs.Items {
			var lines []string
			for _, l := range item.Lines {
				if s := strings.TrimSpace(l.String()); s != "" {
					lines = append(lines, s)
				}
			}
			rawCues = append(rawCues, rawCue{
				startSec: item.StartAt.Seconds(),
				endSec:   item.EndAt.Seconds(),
				rawText:  strings.Join(lines, "\n"),
			})
		}
	} else {
		// Fallback to regex SRT parser for malformed files that astisub rejects
		content, readErr := os.ReadFile(srtPath)
		if readErr != nil {
			return nil, readErr
		}
		matches := reSRTBlock.FindAllStringSubmatch(string(content), -1)
		for _, m := range matches {
			rawCues = append(rawCues, rawCue{
				startSec: SRTTimeToSeconds(m[2]),
				endSec:   SRTTimeToSeconds(m[3]),
				rawText:  m[4],
			})
		}
	}

	var entries []SubEntry
	seq := 0
	for _, rc := range rawCues {
		if IsSongOrKaraokeLine(rc.rawText, rc.startSec, rc.endSec) {
			opts.log("filtered_song", rc.startSec, rc.endSec, rc.rawText)
			continue
		}

		cleaned := cleanRawSubtitleText(rc.rawText, opts.Lang)
		if cleaned == "" {
			opts.log("empty_after_clean", rc.startSec, rc.endSec, rc.rawText)
			continue
		}

		seq++
		entries = append(entries, SubEntry{
			Index:    seq,
			StartSec: rc.startSec,
			EndSec:   rc.endSec,
			Text:     cleaned,
		})
	}

	return DeduplicateAndResolveOverlaps(entries, opts), nil
}

func cleanRawSubtitleText(rawText, lang string) string {
	// Normalize ASS line-break escapes and CRLF before line splitting
	s := StripVectorDrawings(rawText)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, `\N`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\h`, " ")

	var keptLines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || isStatLine(l) {
			continue
		}
		keptLines = append(keptLines, l)
	}
	if len(keptLines) == 0 {
		return ""
	}
	finalText := CleanDialogueLine(strings.Join(keptLines, " "), lang)
	finalText = strings.TrimSpace(reSpace.ReplaceAllString(finalText, " "))
	for _, r := range finalText {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return finalText
		}
	}
	return ""
}

func normalizeForDedup(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DeduplicateAndResolveOverlaps sorts cues chronologically, removes duplicate/contained
// cues within 500ms, merges very close short dialogue fragments (< 250ms gap), and adjusts
// remaining overlapping boundaries so TTS audio clips never collide on the timeline.
func DeduplicateAndResolveOverlaps(entries []SubEntry, opts ParseOptions) []SubEntry {
	if len(entries) <= 1 {
		if len(entries) == 1 {
			entries[0].Index = 1
		}
		return entries
	}

	sorted := make([]SubEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StartSec != sorted[j].StartSec {
			return sorted[i].StartSec < sorted[j].StartSec
		}
		return sorted[i].EndSec < sorted[j].EndSec
	})

	const (
		dedupWindowSec  = 0.50 // 500ms
		closeGapSec     = 0.25 // 250ms
		minCueDurSec    = 0.20
		maxMergeChars   = 120
		maxMergeDurSec  = 6.0
	)

	var out []SubEntry
	for _, curr := range sorted {
		if curr.EndSec < curr.StartSec {
			curr.EndSec = curr.StartSec + minCueDurSec
		}
		if len(out) == 0 {
			out = append(out, curr)
			continue
		}

		prev := &out[len(out)-1]
		normPrev := normalizeForDedup(prev.Text)
		normCurr := normalizeForDedup(curr.Text)

		// 1. Deduplicate identical or substring-contained cues within 500ms
		if curr.StartSec <= prev.EndSec+dedupWindowSec && normPrev != "" && normCurr != "" {
			if normPrev == normCurr || strings.Contains(normPrev, normCurr) || strings.Contains(normCurr, normPrev) {
				if len(normCurr) > len(normPrev) {
					opts.log("dedup_overlap", curr.StartSec, curr.EndSec, fmt.Sprintf("replaced %q with fuller %q", prev.Text, curr.Text))
					prev.Text = curr.Text
				} else {
					opts.log("dedup_overlap", curr.StartSec, curr.EndSec, fmt.Sprintf("dropped duplicate %q (kept %q)", curr.Text, prev.Text))
				}
				if curr.EndSec > prev.EndSec {
					prev.EndSec = curr.EndSec
				}
				continue
			}
		}

		// 2. Anti-overlap / close-gap handling (< 250ms gap)
		gap := curr.StartSec - prev.EndSec
		if gap < closeGapSec {
			combinedLen := len(prev.Text) + 1 + len(curr.Text)
			combinedDur := math.Max(prev.EndSec, curr.EndSec) - prev.StartSec
			if combinedLen <= maxMergeChars && combinedDur <= maxMergeDurSec {
				opts.log("merged_close_gap", curr.StartSec, curr.EndSec, fmt.Sprintf("gap=%.3fs merged %q + %q", gap, prev.Text, curr.Text))
				prev.Text = strings.TrimSpace(prev.Text + " " + curr.Text)
				if curr.EndSec > prev.EndSec {
					prev.EndSec = curr.EndSec
				}
				continue
			}

			// Distinct longer cues that actually overlap -> clamp boundaries so they never collide
			if curr.StartSec < prev.EndSec {
				if curr.StartSec-prev.StartSec >= minCueDurSec {
					opts.log("adjusted_overlap", curr.StartSec, curr.EndSec, fmt.Sprintf("clamped prev.End %.2f->%.2f", prev.EndSec, curr.StartSec))
					prev.EndSec = curr.StartSec
				} else {
					newStart := prev.EndSec
					opts.log("adjusted_overlap", curr.StartSec, curr.EndSec, fmt.Sprintf("shifted curr.Start %.2f->%.2f", curr.StartSec, newStart))
					curr.StartSec = newStart
					if curr.EndSec < curr.StartSec+minCueDurSec {
						curr.EndSec = curr.StartSec + minCueDurSec
					}
				}
			}
		}

		out = append(out, curr)
	}

	for i := range out {
		out[i].Index = i + 1
	}
	return out
}

func assTimeToSeconds(assTime string) float64 {
	assTime = strings.TrimSpace(assTime)
	parts := strings.Split(assTime, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(parts[0], 64)
	m, _ := strconv.ParseFloat(parts[1], 64)
	s, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + s
}
