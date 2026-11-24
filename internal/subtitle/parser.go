package subtitle

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/safari3308/go-dubber/internal/textutil"
)

type SubEntry struct {
	Index    int
	StartSec float64
	EndSec   float64
	Text     string
}

// Blacklist for Game/Anime UI stat tables (ignored by AI reader)
var statBlacklist = []string{
	"name:", "sex:", "level:", "hp:", "mp:", "strength:",
	"stamina:", "intelligence:", "spirit:", "speed:", "dexterity:",
	"fire:", "water:", "wind:", "earth:", "light:", "dark:",
}

var (
	reASS            = regexp.MustCompile(`\{[^}]+\}`)
	reHTML           = regexp.MustCompile(`<[^>]+>`)
	rePrefix         = regexp.MustCompile(`^[A-ZÁÀẢÃẠÉÈẺẼẸÓÒỎÕỌÚÙỦŨỤỨỪỬỮỰÍÌỈĨỊÝỲỶỸỴĐ\s\d]+:\s*`)
	reStutter        = regexp.MustCompile(`(?i)\b([a-zA-ZđĐ]+)-([a-zA-ZđĐ]+)`)
	reSpace          = regexp.MustCompile(`\s+`)
	reAlphaNum       = regexp.MustCompile(`[^\w\s]`)
	reMusicSymbols   = regexp.MustCompile(`[♪♫🎵🎶]`)
	reSongBrackets   = regexp.MustCompile(`(?i)\[(Music|Song|Singing|♫|♪)\]|\((Music|Song|Singing|♫|♪)\)`)
	reASSKaraokeTags = regexp.MustCompile(`(?i)\{\\k[fota]?\d*\}`)
	reFontName       = regexp.MustCompile(`(?i)font face="([^"]+)"`)
	reFontTag        = regexp.MustCompile(`(?i)</?font[^>]*>`)
	reFSOverride     = regexp.MustCompile(`(?i)\{\\fs\d+\}`)
	reSRTBlock       = regexp.MustCompile(`(?m)^(\d+)\r?\n(\d{2}:\d{2}:\d{2}[,\.]\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}[,\.]\d{3})\r?\n([\s\S]*?)(?:\r?\n\r?\n|\z)`)
	songFontKeywords = []string{
		"clubtypemercurius", "chewy", "bubblegum", "song", "karaoke", "music",
	}
)

// collapseRepeatedChars shortens repeated characters that appear more than 2 times in a row (wwwwwhattttttt -> what, nooooo -> no).
// Keeps valid 2-letter repeated words (look, speed, apple...).
func collapseRepeatedChars(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}

	var result []rune
	i := 0
	for i < len(runes) {
		j := i
		for j < len(runes) && unicode.ToLower(runes[j]) == unicode.ToLower(runes[i]) {
			j++
		}

		runLength := j - i
		if runLength >= 3 && unicode.IsLetter(runes[i]) {
			result = append(result, runes[i])
		} else {
			result = append(result, runes[i:j]...)
		}
		i = j
	}
	return string(result)
}

// CleanDialogueLine cleans dialogue text lines for TTS synthesis.
func CleanDialogueLine(textLine string, lang string) string {
	// 1. Remove ASS/SSA {...} and HTML <...>
	clean := reASS.ReplaceAllString(textLine, "")
	clean = reHTML.ReplaceAllString(clean, "")

	// Keep content inside (), [], just remove the brackets (e.g., "(Tập 53)" -> "Tập 53")
	clean = strings.ReplaceAll(clean, "(", " ")
	clean = strings.ReplaceAll(clean, ")", " ")
	clean = strings.ReplaceAll(clean, "[", " ")
	clean = strings.ReplaceAll(clean, "]", " ")

	// 2. Remove speaker name prefixes (e.g. "JOHN: Hello", "ANNOUNCER 1: ")
	clean = rePrefix.ReplaceAllString(clean, "")

	// 3. Remove music symbols and slashes
	clean = strings.ReplaceAll(clean, "\\", "")
	clean = strings.ReplaceAll(clean, "♪", "")
	clean = strings.ReplaceAll(clean, "♫", "")

	// 4. Handle stuttering (b-but -> but, wh-what -> what)
	oldClean := ""
	for oldClean != clean {
		oldClean = clean
		clean = reStutter.ReplaceAllStringFunc(clean, func(m string) string {
			parts := strings.Split(m, "-")
			if len(parts) != 2 {
				return m
			}
			p0, p1 := parts[0], parts[1]
			p0Lower, p1Lower := strings.ToLower(p0), strings.ToLower(p1)
			if p0Lower == p1Lower {
				return p0
			}
			if strings.HasPrefix(p1Lower, p0Lower) {
				if len(p0) > 0 && unicode.IsUpper([]rune(p0)[0]) {
					runes := []rune(p1)
					runes[0] = unicode.ToUpper(runes[0])
					return string(runes)
				}
				return p1
			}
			return m
		})
	}

	// 4.5. Collapse repeated characters
	clean = collapseRepeatedChars(clean)

	// 4.6. Convert numbers to Vietnamese text (called after HTML filtering)
	normLang := strings.ToLower(strings.TrimSpace(lang))
	if normLang == "vi" || normLang == "vie" || normLang == "vn" || normLang == "vietnamese" {
		clean = textutil.NormalizeVietnameseTextReplaceNumbers(clean)
	}

	// 5. Normalize whitespace
	clean = strings.TrimSpace(reSpace.ReplaceAllString(clean, " "))
	if clean == "" {
		return ""
	}

	// 6. Handle ALL-CAPS words
	words := strings.Fields(clean)
	var fixedWords []string
	for _, w := range words {
		core := reAlphaNum.ReplaceAllString(w, "")
		if len(core) > 1 && isAllUpper(core) {
			fixedWords = append(fixedWords, strings.ToLower(w))
		} else {
			fixedWords = append(fixedWords, w)
		}
	}
	clean = strings.Join(fixedWords, " ")

	// 7. Capitalize first letter
	if len(clean) > 0 {
		runes := []rune(clean)
		runes[0] = unicode.ToUpper(runes[0])
		clean = string(runes)
	}

	clean = StripFontTags(clean)

	return clean
}

func isAllUpper(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func isStatLine(line string) bool {
	lower := strings.ToLower(line)
	for _, stat := range statBlacklist {
		if strings.Contains(lower, stat) {
			return true
		}
	}
	return false
}

// IsSongOrKaraokeLine checks if an SRT line/block is song lyrics or ASS karaoke timing overlay.
func IsSongOrKaraokeLine(rawText string, startSec, endSec float64) bool {
	if endSec-startSec <= 0.15 {
		return true
	}
	if reMusicSymbols.MatchString(rawText) {
		return true
	}
	if reSongBrackets.MatchString(rawText) {
		return true
	}
	if reASSKaraokeTags.MatchString(rawText) {
		return true
	}
	if fontMatch := reFontName.FindStringSubmatch(rawText); len(fontMatch) > 1 {
		fontLower := strings.ToLower(fontMatch[1])
		for _, kw := range songFontKeywords {
			if strings.Contains(fontLower, kw) {
				return true
			}
		}
	}
	return false
}

// FilterSongAndKaraokeSubtitles parses an SRT file, drops song/karaoke entries, and writes clean SRT.
func FilterSongAndKaraokeSubtitles(srtPath, outSrtPath string) (int, int, error) {
	content, err := os.ReadFile(srtPath)
	if err != nil {
		return 0, 0, err
	}

	matches := reSRTBlock.FindAllStringSubmatch(string(content), -1)

	totalCount := len(matches)
	var keptBlocks []string
	newIdx := 1

	for _, m := range matches {
		startSec := SRTTimeToSeconds(m[2])
		endSec := SRTTimeToSeconds(m[3])
		rawText := m[4]

		if IsSongOrKaraokeLine(rawText, startSec, endSec) {
			continue
		}

		block := fmt.Sprintf("%d\n%s --> %s\n%s\n", newIdx, m[2], m[3], strings.TrimSpace(rawText))
		keptBlocks = append(keptBlocks, block)
		newIdx++
	}

	outData := []byte(strings.Join(keptBlocks, "\n"))
	if err := os.WriteFile(outSrtPath, outData, 0644); err != nil {
		return totalCount, 0, err
	}

	return totalCount, len(keptBlocks), nil
}

// ParseSRT parses and cleans SRT subtitles.
func ParseSRT(srtPath string, lang string) ([]SubEntry, error) {
	content, err := os.ReadFile(srtPath)
	if err != nil {
		return nil, err
	}

	matches := reSRTBlock.FindAllStringSubmatch(string(content), -1)

	var entries []SubEntry
	for _, m := range matches {
		idx, _ := strconv.Atoi(m[1])
		startSec := SRTTimeToSeconds(m[2])
		endSec := SRTTimeToSeconds(m[3])
		rawText := m[4]

		if IsSongOrKaraokeLine(rawText, startSec, endSec) {
			continue
		}

		rawLines := strings.Split(rawText, "\n")
		var cleanLines []string
		for _, l := range rawLines {
			l = strings.TrimSpace(l)
			if l == "" || isStatLine(l) {
				continue
			}
			cleaned := CleanDialogueLine(l, lang)
			if cleaned != "" {
				cleanLines = append(cleanLines, cleaned)
			}
		}

		if len(cleanLines) == 0 {
			continue
		}

		finalText := strings.Join(cleanLines, " ")
		hasAlnum := false
		for _, r := range finalText {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				hasAlnum = true
				break
			}
		}

		if hasAlnum {
			entries = append(entries, SubEntry{
				Index:    idx,
				StartSec: startSec,
				EndSec:   endSec,
				Text:     finalText,
			})
		}
	}
	return entries, nil
}

// StripFontTags removes HTML font tags and ASS font-size overrides.
func StripFontTags(text string) string {
	text = reFontTag.ReplaceAllString(text, "")
	text = reFSOverride.ReplaceAllString(text, "")
	return text
}

// SRTTimeToSeconds converts an SRT timestamp ("HH:MM:SS,mmm") to seconds.
func SRTTimeToSeconds(timeStr string) float64 {
	timeStr = strings.ReplaceAll(timeStr, ",", ".")
	parts := strings.Split(timeStr, ":")
	if len(parts) < 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(parts[0], 64)
	m, _ := strconv.ParseFloat(parts[1], 64)
	s, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + s
}
