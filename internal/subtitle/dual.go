package subtitle

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/safari3308/go-dubber/internal/config"
)

// SubtitleSource identifies either an external subtitle file or an embedded subtitle stream.
type SubtitleSource struct {
	Type          string `json:"type"`           // "external", "embedded", "none"
	Path          string `json:"path,omitempty"` // External subtitle path
	EmbeddedIndex int    `json:"embedded_index"` // 0-based subtitle stream index
	Format        string `json:"format"`         // "srt", "ass", "ssa", "vtt", etc.
	Description   string `json:"description"`
}

// IsValid returns true when the source points to an actual subtitle.
func (s SubtitleSource) IsValid() bool {
	if s.Type == "external" {
		return s.Path != ""
	}
	return s.Type == "embedded"
}

// IsASS returns true when the subtitle format is ASS or SSA.
func (s SubtitleSource) IsASS() bool {
	f := strings.ToLower(strings.TrimPrefix(s.Format, "."))
	return f == "ass" || f == "ssa"
}

// LanguageDubSpec defines the dual-subtitle configuration for a single dubbing language.
type LanguageDubSpec struct {
	Language     string          `json:"language"`
	TTSSub       SubtitleSource  `json:"tts_sub"`                 // Subtitle used for TTS voiceover generation
	EmbedSub     *SubtitleSource `json:"embed_sub,omitempty"`     // Optional separate subtitle muxed into output video (e.g. .ass with descriptions)
	SyncTTSSub   bool            `json:"sync_tts_sub"`            // Run subsync on TTSSub when external
	SyncEmbedSub bool            `json:"sync_embed_sub"`          // Run subsync on EmbedSub when external
}

// CandidateSubtitle represents a discovered external subtitle file for a video.
type CandidateSubtitle struct {
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	Format   string `json:"format"`
	Language string `json:"language"`
	Score    int    `json:"score"`
	IsASS    bool   `json:"is_ass"`
}

// DiscoverExternalSubtitlesForLanguage finds all matching external subtitle candidates for videoPath and targetLang,
// sorted by score descending (with .srt vs .ass preserved so callers can pick dual subtitles).
func DiscoverExternalSubtitlesForLanguage(videoPath string, targetLang string, cfg *config.Config) []CandidateSubtitle {
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
	seenPaths := make(map[string]bool)
	var candidates []CandidateSubtitle

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
			} else if cfg != nil && (targetLang == "vi" || targetLang == strings.ToLower(strings.TrimSpace(cfg.SubLanguage))) &&
				(strings.HasPrefix(fNoExt, lowerBase) || matchesAnyCode(fNoExt, epCodes)) {
				score = 60
			}

			if score > 0 {
				fullPath := filepath.Clean(filepath.Join(sd.path, fName))
				if seenPaths[fullPath] {
					continue
				}
				seenPaths[fullPath] = true

				fmtName := strings.TrimPrefix(fExt, ".")
				candidates = append(candidates, CandidateSubtitle{
					Path:     fullPath,
					FileName: fName,
					Format:   fmtName,
					Language: targetLang,
					Score:    score,
					IsASS:    fmtName == "ass" || fmtName == "ssa",
				})
			}
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].FileName < candidates[j].FileName
	})

	return candidates
}

// ResolveDualSubtitle automatically resolves both the TTS subtitle and the Embed subtitle (e.g. ASS) for a language.
// Returns nil if no subtitle is found for targetLang.
func ResolveDualSubtitle(videoPath string, embeddedSubs []EmbeddedSubInfo, cfg *config.Config, targetLang string) *LanguageDubSpec {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	candidates := DiscoverExternalSubtitlesForLanguage(videoPath, targetLang, cfg)

	var bestSRT *CandidateSubtitle
	var bestASS *CandidateSubtitle
	var bestAny *CandidateSubtitle

	for i := range candidates {
		c := &candidates[i]
		if bestAny == nil {
			bestAny = c
		}
		if c.IsASS && bestASS == nil {
			bestASS = c
		}
		if !c.IsASS && bestSRT == nil {
			bestSRT = c
		}
	}

	// Check embedded text subtitle matching targetLang
	var matchedEmbedded *EmbeddedSubInfo
	for i := range embeddedSubs {
		sub := &embeddedSubs[i]
		if !sub.IsBitmap && IsSubMatchingLanguage(sub.Language, sub.Title, targetLang) {
			matchedEmbedded = sub
			break
		}
	}

	var ttsSub SubtitleSource
	var embedSub *SubtitleSource

	// Priority for TTS sub:
	// 1. If ForceFallbackSub -> embedded fallback
	// 2. Embedded text sub matching targetLang
	// 3. External non-ASS sub (SRT/VTT) — cleaner for TTS when paired with ASS
	// 4. Any external sub (including ASS, which will be converted to SRT for TTS)
	if cfg.ForceFallbackSub {
		fb := FallbackEmbedSubForLanguage(cfg, embeddedSubs, targetLang)
		if fb.Found {
			ttsSub = SubtitleSource{
				Type:          "embedded",
				EmbeddedIndex: fb.EmbeddedIndex,
				Format:        "srt",
				Description:   fb.Description,
			}
		}
	} else if matchedEmbedded != nil {
		ttsSub = SubtitleSource{
			Type:          "embedded",
			EmbeddedIndex: matchedEmbedded.SubIndex,
			Format:        matchedEmbedded.CodecName,
			Description:   fmt.Sprintf("Embedded track #%d (%s)", matchedEmbedded.SubIndex, matchedEmbedded.Language),
		}
	} else if bestSRT != nil {
		ttsSub = SubtitleSource{
			Type:        "external",
			Path:        bestSRT.Path,
			Format:      bestSRT.Format,
			Description: fmt.Sprintf("External %s: %s", strings.ToUpper(bestSRT.Format), bestSRT.FileName),
		}
	} else if bestAny != nil {
		ttsSub = SubtitleSource{
			Type:        "external",
			Path:        bestAny.Path,
			Format:      bestAny.Format,
			Description: fmt.Sprintf("External %s: %s", strings.ToUpper(bestAny.Format), bestAny.FileName),
		}
	}

	if !ttsSub.IsValid() {
		return nil
	}

	// Priority for Embed sub:
	// 1. If an external ASS/SSA subtitle exists for this language, prefer it for embedding (preserves signs/descriptions!)
	// 2. Otherwise, if TTSSub is external, embed TTSSub as well.
	// 3. If TTSSub is embedded and no external ASS exists, no extra embed sub needed.
	if bestASS != nil {
		embedSub = &SubtitleSource{
			Type:        "external",
			Path:        bestASS.Path,
			Format:      bestASS.Format,
			Description: fmt.Sprintf("External ASS: %s", bestASS.FileName),
		}
	} else if ttsSub.Type == "external" {
		copied := ttsSub
		embedSub = &copied
	}

	syncTTS := ttsSub.Type == "external" && !cfg.SkipSubSync
	// Avoid running subsync on ASS by default unless user explicitly opts in, because subsync outputs SRT and strips ASS styling.
	syncEmbed := embedSub != nil && embedSub.Type == "external" && !embedSub.IsASS() && !cfg.SkipSubSync

	return &LanguageDubSpec{
		Language:     targetLang,
		TTSSub:       ttsSub,
		EmbedSub:     embedSub,
		SyncTTSSub:   syncTTS,
		SyncEmbedSub: syncEmbed,
	}
}
