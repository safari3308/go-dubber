package subtitle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/safari3308/go-dubber/internal/config"
)

// EmbeddedSubInfo represents a subtitle stream embedded inside a video container.
type EmbeddedSubInfo struct {
	SubIndex  int    `json:"sub_index"`  // Relative 0-based subtitle stream index
	StreamIdx int    `json:"stream_idx"` // Absolute stream index in video file
	Language  string `json:"language"`   // "vi", "en", "unknown"
	Title     string `json:"title"`
	CodecName string `json:"codec_name"` // "subrip", "ass", "dvd_subtitle", etc.
	IsBitmap  bool   `json:"is_bitmap"`  // true if graphic/image subtitle
}

// SelectedSubtitle describes a resolved subtitle track (external file or embedded stream).
type SelectedSubtitle struct {
	SubPath       string `json:"sub_path"`
	Language      string `json:"language"` // e.g. "vi", "en"
	IsExternal    bool   `json:"is_external"`
	EmbeddedIndex int    `json:"embedded_index"` // 0-based index of subtitle stream
	Description   string `json:"description"`
	Found         bool   `json:"found"`
}

func GetLanguageTokens(targetLang string) []string {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	tokens := []string{targetLang}

	switch targetLang {
	case "vi":
		tokens = append(tokens, "vie", "viet", "vietnamese", "vietsub", "vsub", "tiengviet", "tiếng việt")
	case "en":
		tokens = append(tokens, "eng", "english", "engsub")
	case "ja", "jp":
		tokens = append(tokens, "ja", "jp", "jpn", "japanese", "japsub")
	case "zh", "cn":
		tokens = append(tokens, "zh", "cn", "chi", "zho", "chinese", "chinsub")
	}

	seen := make(map[string]bool)
	var unique []string
	for _, tok := range tokens {
		lTok := strings.ToLower(tok)
		if !seen[lTok] {
			seen[lTok] = true
			unique = append(unique, tok)
		}
	}
	return unique
}

var (
	reCodeSE = regexp.MustCompile(`(?i)S(\d+)E(\d+)`)
	reCodeX  = regexp.MustCompile(`(?i)\b(\d+)x(\d+)\b`)
	reCodeEp = regexp.MustCompile(`(?i)\bEP?(\d+)\b`)
	reNumSE  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,4})[\.\-_ ]*E(\d{1,4})(?:[^a-z0-9]|$)`)
	reNumX   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{1,4})(?:[^a-z0-9]|$)`)
	reNumEp  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:EP|E|Episode)[\.\-_ ]*(\d{1,4})(?:[^a-z0-9]|$)`)
)

func ExtractEpisodeCodes(baseName string) []string {
	var codes []string
	seen := make(map[string]bool)
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c != "" && !seen[strings.ToLower(c)] {
			seen[strings.ToLower(c)] = true
			codes = append(codes, c)
		}
	}

	if matchesSE := reCodeSE.FindStringSubmatch(baseName); len(matchesSE) == 3 {
		add(matchesSE[0])
		sNum, _ := strconv.Atoi(matchesSE[1])
		eNum, _ := strconv.Atoi(matchesSE[2])
		add(fmt.Sprintf("S%02dE%02d", sNum, eNum))
		add(fmt.Sprintf("S%dE%d", sNum, eNum))
		add(fmt.Sprintf("%dx%02d", sNum, eNum))
		add(fmt.Sprintf("E%02d", eNum))
		add(fmt.Sprintf("E%03d", eNum))
		add(fmt.Sprintf("EP%02d", eNum))
		add(fmt.Sprintf("EP%03d", eNum))
	}

	if matchesX := reCodeX.FindStringSubmatch(baseName); len(matchesX) == 3 {
		add(matchesX[0])
		sNum, _ := strconv.Atoi(matchesX[1])
		eNum, _ := strconv.Atoi(matchesX[2])
		add(fmt.Sprintf("S%02dE%02d", sNum, eNum))
		add(fmt.Sprintf("E%02d", eNum))
		add(fmt.Sprintf("EP%02d", eNum))
	}

	if matchesEp := reCodeEp.FindStringSubmatch(baseName); len(matchesEp) == 2 {
		add(matchesEp[0])
		eNum, _ := strconv.Atoi(matchesEp[1])
		add(fmt.Sprintf("E%02d", eNum))
		add(fmt.Sprintf("E%03d", eNum))
		add(fmt.Sprintf("EP%02d", eNum))
		add(fmt.Sprintf("EP%03d", eNum))
	}

	return codes
}

func ExtractEpisodeNumbers(name string) (season int, episode int, found bool) {
	if m := reNumSE.FindStringSubmatch(name); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, true
	}
	if m := reNumX.FindStringSubmatch(name); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, true
	}
	if m := reNumEp.FindStringSubmatch(name); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		return -1, e, true
	}
	return -1, -1, false
}

func HasEpisodeConflict(videoBase, subName string, subDirTarget string) bool {
	vSeason, vEp, vHas := ExtractEpisodeNumbers(videoBase)
	if !vHas {
		sSeason, sEp, sHas := ExtractEpisodeNumbers(subName)
		if sHas && (sSeason != -1 || sEp != -1) {
			return true
		}
		if subDirTarget != "" {
			dSeason, dEp, dHas := ExtractEpisodeNumbers(subDirTarget)
			if dHas && (dSeason != -1 || dEp != -1) {
				return true
			}
		}
		return false
	}

	if subDirTarget != "" {
		dSeason, dEp, dHas := ExtractEpisodeNumbers(subDirTarget)
		if dHas {
			if dEp != -1 && dEp != vEp {
				return true
			}
			if dSeason != -1 && vSeason != -1 && dSeason != vSeason {
				return true
			}
		}
	}

	sSeason, sEp, sHas := ExtractEpisodeNumbers(subName)
	if sHas {
		if sEp != -1 && sEp != vEp {
			return true
		}
		if sSeason != -1 && vSeason != -1 && sSeason != vSeason {
			return true
		}
	}

	return false
}

func IsSubMatchingLanguage(subLang, subTitle, targetLang string) bool {
	subLang = strings.ToLower(strings.TrimSpace(subLang))
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	subTitle = strings.ToLower(strings.TrimSpace(subTitle))

	if subLang == targetLang {
		return true
	}

	tokens := GetLanguageTokens(targetLang)
	for _, tok := range tokens {
		lTok := strings.ToLower(tok)
		if subLang == lTok {
			return true
		}
		if subTitle != "" && strings.Contains(subTitle, lTok) {
			return true
		}
	}
	return false
}

func HasLanguageToken(filename string, targetLang string, tokens []string) bool {
	fLower := strings.ToLower(filename)
	ext := strings.ToLower(filepath.Ext(fLower))
	fNoExt := strings.TrimSuffix(fLower, ext)

	for _, tok := range tokens {
		lTok := strings.ToLower(tok)
		if fNoExt == lTok {
			return true
		}
	}

	for _, tok := range tokens {
		lTok := strings.ToLower(tok)
		if len(lTok) >= 4 && strings.Contains(fLower, lTok) {
			return true
		}
	}

	delims := []string{".", "_", "-", " ", "(", ")", "[", "]", "!"}
	for _, tok := range tokens {
		lTok := strings.ToLower(tok)
		for _, dBefore := range delims {
			for _, dAfter := range delims {
				pattern := dBefore + lTok + dAfter
				if strings.Contains(fLower, pattern) {
					return true
				}
			}
			if strings.HasSuffix(fNoExt, dBefore+lTok) {
				return true
			}
			if strings.HasPrefix(fNoExt, lTok+dBefore) {
				return true
			}
		}
	}

	return false
}

func IsSupportedSubtitleExtension(ext string) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".sbv", ".smi", ".sami", ".ttml", ".dfxp", ".lrc":
		return true
	default:
		return false
	}
}

type candidateMatch struct {
	path  string
	score int
}

func FindExternalSubtitleForLanguage(videoPath string, targetLang string, cfg *config.Config) (string, bool) {
	dir := filepath.Dir(videoPath)
	baseName := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	tokens := GetLanguageTokens(targetLang)
	epCodes := ExtractEpisodeCodes(baseName)

	type searchDirInfo struct {
		path         string
		subDirTarget string
	}

	searchDirs := []searchDirInfo{
		{path: dir},
		{path: filepath.Join(dir, "Subs")},
		{path: filepath.Join(dir, "subtitles")},
		{path: filepath.Join(dir, "Sub")},
		{path: filepath.Join(dir, "subtitle")},
		{path: filepath.Join(dir, "Subs", baseName), subDirTarget: baseName},
		{path: filepath.Join(dir, "subtitles", baseName), subDirTarget: baseName},
	}
	for _, epCode := range epCodes {
		searchDirs = append(searchDirs,
			searchDirInfo{path: filepath.Join(dir, "Subs", epCode), subDirTarget: epCode},
			searchDirInfo{path: filepath.Join(dir, "subtitles", epCode), subDirTarget: epCode},
		)
	}

	lowerBase := strings.ToLower(baseName)
	var bestMatch candidateMatch

	for _, sd := range searchDirs {
		entries, err := os.ReadDir(sd.path)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fName := entry.Name()
			fExt := strings.ToLower(filepath.Ext(fName))
			if !IsSupportedSubtitleExtension(fExt) {
				continue
			}

			fLower := strings.ToLower(fName)
			fNoExt := strings.TrimSuffix(fLower, fExt)

			if HasEpisodeConflict(baseName, fNoExt, sd.subDirTarget) {
				continue
			}

			hasLang := HasLanguageToken(fName, targetLang, tokens)
			score := 0

			if strings.HasPrefix(fNoExt, lowerBase) && hasLang {
				score = 100
			} else if sd.subDirTarget != "" && hasLang {
				score = 95
			} else if hasLang && matchesAnyCode(fNoExt, epCodes) {
				score = 85
			} else if isStandaloneLang(fNoExt, tokens) {
				score = 75
			} else if (targetLang == "vi" || targetLang == strings.ToLower(strings.TrimSpace(cfg.SubLanguage))) &&
				(strings.HasPrefix(fNoExt, lowerBase) || matchesAnyCode(fNoExt, epCodes)) {
				score = 60
			}

			if score > bestMatch.score {
				bestMatch = candidateMatch{
					path:  filepath.Join(sd.path, fName),
					score: score,
				}
				if score == 100 {
					return bestMatch.path, true
				}
			}
		}
	}

	if bestMatch.score > 0 {
		return bestMatch.path, true
	}

	return "", false
}

func matchWordBoundary(text, token string) bool {
	tLen := len(token)
	textLen := len(text)
	if tLen == 0 || textLen < tLen {
		return false
	}
	for i := 0; i <= textLen-tLen; i++ {
		if text[i:i+tLen] == token {
			if i > 0 {
				prev := text[i-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
					continue
				}
			}
			if i+tLen < textLen {
				next := text[i+tLen]
				if (next >= 'a' && next <= 'z') || (next >= '0' && next <= '9') {
					continue
				}
			}
			return true
		}
	}
	return false
}

func matchesAnyCode(fNoExt string, epCodes []string) bool {
	fLower := strings.ToLower(fNoExt)
	for _, code := range epCodes {
		lCode := strings.ToLower(code)
		if matchWordBoundary(fLower, lCode) {
			return true
		}
	}
	return false
}

func isStandaloneLang(fNoExt string, tokens []string) bool {
	for _, tok := range tokens {
		if fNoExt == strings.ToLower(tok) {
			return true
		}
	}
	return false
}

// PromptFunc optionally asks an interactive user to pick an embedded subtitle stream.
type PromptFunc func([]EmbeddedSubInfo) *EmbeddedSubInfo

func SelectSubtitleForLanguage(videoPath string, embeddedSubs []EmbeddedSubInfo, cfg *config.Config, targetLang string, promptFn PromptFunc) SelectedSubtitle {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	// Priority 0: force fallback sub
	if cfg.ForceFallbackSub {
		return FallbackEmbedSubForLanguage(cfg, embeddedSubs, targetLang)
	}

	// Priority 1: Embedded text subtitle matching targetLang
	for _, sub := range embeddedSubs {
		if IsSubMatchingLanguage(sub.Language, sub.Title, targetLang) {
			if sub.IsBitmap {
				fmt.Printf("    ⚠️ Skipping embedded subtitle track #%d (%s, lang: %s): Bitmap format (%s) cannot be extracted as text for TTS.\n", sub.SubIndex, sub.Title, sub.Language, sub.CodecName)
				continue
			}
			desc := fmt.Sprintf("Selected embedded subtitle track #%d (lang: %s", sub.SubIndex, sub.Language)
			if sub.Title != "" {
				desc += fmt.Sprintf(" - %s", sub.Title)
			}
			desc += ")"
			return SelectedSubtitle{
				Language:      targetLang,
				IsExternal:    false,
				EmbeddedIndex: sub.SubIndex,
				Description:   desc,
				Found:         true,
			}
		}
	}

	// Priority 2: External subtitle matching targetLang
	if extPath, found := FindExternalSubtitleForLanguage(videoPath, targetLang, cfg); found {
		return SelectedSubtitle{
			SubPath:     extPath,
			Language:    targetLang,
			IsExternal:  true,
			Description: fmt.Sprintf("Selected external subtitle for '%s': %s", targetLang, filepath.Base(extPath)),
			Found:       true,
		}
	}

	// Priority 3: Interactive mode selection if enabled
	if cfg.InteractiveMode && len(embeddedSubs) > 0 && promptFn != nil {
		fmt.Printf("    ⚠️ No embedded or external subtitle automatically found for language '%s'.\n", targetLang)
		userSelected := promptFn(embeddedSubs)
		if userSelected != nil {
			if userSelected.IsBitmap {
				fmt.Printf("    ⚠️ Selected track #%d is a bitmap subtitle (%s) which cannot be extracted as text.\n", userSelected.SubIndex, userSelected.CodecName)
			}
			desc := fmt.Sprintf("Manual selected embedded subtitle (track #%d", userSelected.SubIndex)
			if userSelected.Title != "" {
				desc += fmt.Sprintf(" - %s", userSelected.Title)
			}
			desc += ")"
			return SelectedSubtitle{
				Language:      targetLang,
				IsExternal:    false,
				EmbeddedIndex: userSelected.SubIndex,
				Description:   desc,
				Found:         true,
			}
		}
	}

	return SelectedSubtitle{Found: false}
}

func FallbackEmbedSubForLanguage(cfg *config.Config, embeddedSubs []EmbeddedSubInfo, targetLang string) SelectedSubtitle {
	if len(embeddedSubs) > 0 {
		if cfg.DefaultSubIndex >= 0 && cfg.DefaultSubIndex < len(embeddedSubs) {
			sub := embeddedSubs[cfg.DefaultSubIndex]
			if !sub.IsBitmap {
				desc := fmt.Sprintf("Selected embedded subtitle (track #%d, fallback)", sub.SubIndex)
				return SelectedSubtitle{
					Language:      targetLang,
					IsExternal:    false,
					EmbeddedIndex: sub.SubIndex,
					Description:   desc,
					Found:         true,
				}
			}
		}
		for _, sub := range embeddedSubs {
			if !sub.IsBitmap {
				desc := fmt.Sprintf("Selected embedded subtitle (track #%d, fallback)", sub.SubIndex)
				return SelectedSubtitle{
					Language:      targetLang,
					IsExternal:    false,
					EmbeddedIndex: sub.SubIndex,
					Description:   desc,
					Found:         true,
				}
			}
		}
	}
	return SelectedSubtitle{Found: false}
}
