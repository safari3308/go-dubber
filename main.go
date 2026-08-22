package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/safari3308/go-dubber/api"
	"github.com/safari3308/go-dubber/config"
	"github.com/safari3308/go-dubber/media"
	"github.com/safari3308/go-dubber/utils"
)

func isSupportedVideoExtension(ext string) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".m4v", ".webm", ".flv", ".wmv", ".ts", ".mts", ".m2ts":
		return true
	default:
		return false
	}
}

func main() {
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("❌ Failed to load config.json: %v", err)
	}

	fmt.Println("==================================================")
	fmt.Println("🚀 ACTIVATING NAS DUBBER AUTOMATED PIPELINE")
	fmt.Printf("📁 NAS Directory: %s\n", cfg.NasDir)
	fmt.Printf("🌐 API Server: %s\n", cfg.ApiUrl)
	fmt.Printf("⚙️ Mode: GPU=%v | ForceReprocess=%v | OnlyCheckKokoro=%v | TTSSpeed=%.2fx\n", cfg.UseGPU, cfg.ForceReprocess, cfg.OnlyCheckKokoro, cfg.TTSSpeed)
	fmt.Println("==================================================")

	// Automatically create local temporary directory on local SSD
	localTempDir := "./temp"
	if err := os.MkdirAll(localTempDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create local temp directory: %v", err)
	}

	err = filepath.Walk(cfg.NasDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if isSupportedVideoExtension(ext) {
			if strings.HasPrefix(filepath.Base(path), "temp_") {
				return nil
			}
			processVideo(path, cfg, localTempDir)
		}
		return nil
	})

	if err != nil {
		log.Printf("❌ Error sweeping NAS directory: %v", err)
	}

	// Purge local temp workspace after processing
	_ = os.RemoveAll(localTempDir)

	fmt.Println("\n🎉 COMPLETED ALL MEDIA PROCESSING JOBS.")
}

type SelectedSubtitle struct {
	SubPath       string
	Language      string // e.g. "vi", "en"
	IsExternal    bool
	EmbeddedIndex int // 0-based index of subtitle stream
	Description   string
	Found         bool
}

func findExternalSubtitleForLanguage(videoPath string, targetLang string, cfg *config.Config) (string, bool) {
	dir := filepath.Dir(videoPath)
	baseName := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	iso3 := targetLang
	switch targetLang {
	case "vi":
		iso3 = "vie"
	case "en":
		iso3 = "eng"
	case "ja":
		iso3 = "jpn"
	case "zh", "cn":
		iso3 = "chi"
	}

	candidates := []string{
		filepath.Join(dir, baseName+"."+targetLang+".srt"),
		filepath.Join(dir, baseName+"."+iso3+".srt"),
		filepath.Join(dir, baseName+"."+targetLang+".ass"),
		filepath.Join(dir, baseName+"."+iso3+".ass"),
		filepath.Join(dir, baseName+"_"+targetLang+".srt"),
		filepath.Join(dir, baseName+"_"+iso3+".srt"),
		filepath.Join(dir, baseName+"."+targetLang+".vtt"),
		filepath.Join(dir, baseName+"."+iso3+".vtt"),
	}

	// Also check generic .srt / .ass without language code if targetLang matches cfg.SubLanguage or if targetLang == "vi"
	if targetLang == "vi" || targetLang == strings.ToLower(strings.TrimSpace(cfg.SubLanguage)) {
		candidates = append(candidates,
			filepath.Join(dir, baseName+".srt"),
			filepath.Join(dir, baseName+".ass"),
			filepath.Join(dir, baseName+".vtt"),
		)
	}

	for _, candidate := range candidates {
		if utils.FileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func selectSubtitleForLanguage(videoPath string, videoInfo *media.VideoInfo, cfg *config.Config, targetLang string) SelectedSubtitle {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))

	// Priority 0: force fallback sub
	if cfg.ForceFallbackSub {
		return fallbackEmbedSubForLanguage(cfg, videoInfo, targetLang)
	}

	// Priority 1: Embedded text subtitle matching targetLang
	for _, sub := range videoInfo.EmbeddedSubStreams {
		if sub.Language == targetLang {
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
	if extPath, found := findExternalSubtitleForLanguage(videoPath, targetLang, cfg); found {
		return SelectedSubtitle{
			SubPath:     extPath,
			Language:    targetLang,
			IsExternal:  true,
			Description: fmt.Sprintf("Selected external subtitle for '%s': %s", targetLang, filepath.Base(extPath)),
			Found:       true,
		}
	}

	// Priority 3: Interactive mode selection if enabled
	if cfg.InteractiveMode && len(videoInfo.EmbeddedSubStreams) > 0 {
		fmt.Printf("    ⚠️ No embedded or external subtitle automatically found for language '%s'.\n", targetLang)
		userSelected := media.PromptUserSelectSub(videoInfo.EmbeddedSubStreams)
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

func fallbackEmbedSubForLanguage(cfg *config.Config, videoInfo *media.VideoInfo, targetLang string) SelectedSubtitle {
	if len(videoInfo.EmbeddedSubStreams) > 0 {
		if cfg.DefaultSubIndex >= 0 && cfg.DefaultSubIndex < len(videoInfo.EmbeddedSubStreams) {
			sub := videoInfo.EmbeddedSubStreams[cfg.DefaultSubIndex]
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
		for _, sub := range videoInfo.EmbeddedSubStreams {
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

func processVideo(nasVideoPath string, cfg *config.Config, localTempDir string) {
	// 🌟 PROTECT 1: Check Server TTS health before doing anything
	if err := api.CheckServerHealth(cfg); err != nil {
		fmt.Printf("❌ [ABORT] Server TTS is down: %v -> Skip this video!\n", err)
		return
	}

	fmt.Printf("\n🎬 Inspecting video: %s\n", filepath.Base(nasVideoPath))

	for _, targetLang := range cfg.DubLanguages {
		// Re-probe video metadata for each target language (in case previous language iteration updated the file on NAS)
		videoInfo, err := media.InspectVideo(nasVideoPath)
		if err != nil {
			fmt.Printf("    ❌ Failed to inspect video metadata: %v\n", err)
			continue
		}

		// Skip logic based on existing tracks and configuration
		if !cfg.ForceReprocess {
			if videoInfo.HasAudioLanguage(targetLang) {
				fmt.Printf("    ⏭️ Skipped [%s]: Video ALREADY contains audio track matching language '%s'.\n", targetLang, targetLang)
				continue
			}
		}

		// Select subtitle matching targetLang
		subChoice := selectSubtitleForLanguage(nasVideoPath, videoInfo, cfg, targetLang)
		if !subChoice.Found {
			fmt.Printf("    ⚠️ No subtitle matching language '%s' found. Skipping '%s' dub.\n", targetLang, targetLang)
			continue
		}
		fmt.Printf("    💬 %s\n", subChoice.Description)

		processVideoForLanguage(nasVideoPath, videoInfo, subChoice, cfg, localTempDir, targetLang)
	}
}

func processVideoForLanguage(nasVideoPath string, videoInfo *media.VideoInfo, subChoice SelectedSubtitle, cfg *config.Config, localTempDir string, targetLang string) {
	totalStart := time.Now()
	var ttsDuration, remuxDuration time.Duration

	origFileInfo, err := os.Stat(nasVideoPath)
	origSize := int64(0)
	if err == nil {
		origSize = origFileInfo.Size()
	}

	baseName := strings.TrimSuffix(filepath.Base(nasVideoPath), filepath.Ext(nasVideoPath))
	localVideoPath := filepath.Join(localTempDir, filepath.Base(nasVideoPath))

	// ==========================================
	// DOWNLOAD VIDEO TO LOCAL SSD
	// ==========================================
	spinCopy := utils.StartSpinner("📥 Downloading original video from NAS to Local SSD...")
	copyStart := time.Now()
	err = utils.CopyFile(nasVideoPath, localVideoPath)
	if err != nil {
		spinCopy.Stop(fmt.Sprintf("Download failed: %v", err), true)
		return
	}
	spinCopy.Stop(fmt.Sprintf("Downloaded original video to Local SSD! (%s)", utils.FormatDuration(time.Since(copyStart))), false)

	extSubPath := subChoice.SubPath
	currentLang := targetLang
	isExternalSub := subChoice.IsExternal

	// If using embedded subtitle, extract subtitle track from local file
	if !isExternalSub {
		fmt.Printf("    📦 Extracting embedded subtitle (track #%d) from local video...\n", subChoice.EmbeddedIndex)
		extractedSubPath := filepath.Join(localTempDir, "extracted_"+baseName+"_"+targetLang+".srt")

		err := media.ExtractEmbeddedSubtitle(localVideoPath, extractedSubPath, subChoice.EmbeddedIndex)
		if err != nil {
			fmt.Printf("    ❌ Failed to extract embedded subtitle: %v -> Skipping.\n", err)
			_ = os.Remove(localVideoPath)
			return
		}

		extSubPath = extractedSubPath
		if cfg.DropSongSubtitles {
			total, kept, err := media.FilterSongAndKaraokeSubtitles(extractedSubPath, extractedSubPath)
			if err == nil && total > kept {
				fmt.Printf("    🎵 Filtered song/karaoke subtitles: %d -> %d entries kept\n", total, kept)
			}
		}
		fmt.Println("    ✅ Embedded subtitle extracted successfully!")
	}

	// Define temporary file paths on local SSD
	subExt := filepath.Ext(extSubPath)
	if subExt == "" {
		subExt = ".srt"
	}
	localAnchorAudio := filepath.Join(localTempDir, "anchor_"+baseName+"_"+targetLang+".aac")
	localSyncedSub := filepath.Join(localTempDir, "synced_"+baseName+"_"+targetLang+subExt)
	localTtsWav := filepath.Join(localTempDir, "tts_"+baseName+"_"+targetLang+".wav")
	localOutVideo := filepath.Join(localTempDir, "out_"+filepath.Base(nasVideoPath))

	// Register temp file cleanup
	defer utils.CleanTempFiles(localVideoPath, localAnchorAudio, localSyncedSub, localTtsWav, localOutVideo)
	if !isExternalSub {
		defer os.Remove(extSubPath)
	}

	// ==========================================
	// SUBTITLE SYNCHRONIZATION LOGIC
	// ==========================================
	finalSubPath := extSubPath

	// Prepare target subtitle path for sync (filter song text from external subtitle if needed)
	targetSubPathForSync := extSubPath
	if isExternalSub && cfg.DropSongSubtitles {
		cleanExtSubPath := filepath.Join(localTempDir, "clean_ext_"+filepath.Base(extSubPath))
		total, kept, err := media.FilterSongAndKaraokeSubtitles(extSubPath, cleanExtSubPath)
		if err == nil && total > kept {
			fmt.Printf("    🎵 Filtered song text from external subtitle (%d -> %d entries kept)\n", total, kept)
			targetSubPathForSync = cleanExtSubPath
			defer os.Remove(cleanExtSubPath)
		}
	}

	if !isExternalSub || cfg.SkipSubSync {
		// CASE 1: Use embedded subtitle or skip_sub_sync is true
		fmt.Println("ℹ️ [SYNC] Skipping sync because embedded subtitle or skip_sub_sync is true.")

	} else if len(videoInfo.EmbeddedSubStreams) > 0 {
		// CASE 2: Use external subtitle + Video HAS embedded subtitle (Sub-to-Sub sync)
		var refSubStream *media.EmbeddedSubInfo
		for i := range videoInfo.EmbeddedSubStreams {
			if !videoInfo.EmbeddedSubStreams[i].IsBitmap {
				refSubStream = &videoInfo.EmbeddedSubStreams[i]
				break
			}
		}

		if refSubStream != nil {
			localRefSubPath := filepath.Join(localTempDir, "ref_embedded.srt")

			spinExtractRef := utils.StartSpinner(fmt.Sprintf("📜 Extracting embedded subtitle #%d as reference...", refSubStream.SubIndex))
			if err := media.ExtractEmbeddedSubtitle(localVideoPath, localRefSubPath, refSubStream.SubIndex); err != nil {
				spinExtractRef.Stop(fmt.Sprintf("Failed to extract reference subtitle: %v", err), true)
			} else {
				if cfg.DropSongSubtitles {
					total, kept, err := media.FilterSongAndKaraokeSubtitles(localRefSubPath, localRefSubPath)
					if err == nil && total > kept {
						spinExtractRef.Stop(fmt.Sprintf("Extracted reference subtitle & filtered song text (%d -> %d entries)!", total, kept), false)
					} else {
						spinExtractRef.Stop("Extracted embedded subtitle as reference successfully!", false)
					}
				} else {
					spinExtractRef.Stop("Extracted embedded subtitle as reference successfully!", false)
				}

				spinSync := utils.StartSpinner("🔄 Synchronizing external subtitle with embedded subtitle (Sub-to-Sub)...")
				err := api.SyncSubtitleWithServer(cfg, targetSubPathForSync, localRefSubPath, localSyncedSub)
				if err != nil {
					spinSync.Stop(fmt.Sprintf("Sync Sub-to-Sub failed (%v), using original subtitle.", err), true)
				} else {
					spinSync.Stop("Sub-to-Sub Timeline Sync Perfect!", false)
					finalSubPath = localSyncedSub
				}
			}
		} else {
			fmt.Println("ℹ️ [SYNC] Embedded subtitles are bitmap format; falling back to Audio Sync.")
			if !cfg.SkipSubSync {
				spinExt := utils.StartSpinner("⏱️ Extracting Anchor Audio for Audio Sync...")
				targetAudioIndex := videoInfo.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)
				fmt.Printf("🎙️ Original audio track selected as input: 0:a:%d\n", targetAudioIndex)
				if err := media.ExtractAudioAnchor(localVideoPath, localAnchorAudio, targetAudioIndex); err != nil {
					spinExt.Stop(fmt.Sprintf("Failed to extract Anchor Audio: %v", err), true)
				} else {
					spinExt.Stop("Successfully extracted Anchor Audio!", false)

					spinSync := utils.StartSpinner("🔄 Sending data to Subsync server (Audio-based)...")
					err := api.SyncSubtitleWithServer(cfg, targetSubPathForSync, localAnchorAudio, localSyncedSub)
					if err != nil {
						spinSync.Stop(fmt.Sprintf("Audio Subsync failed (%v), using original subtitle.", err), true)
					} else {
						spinSync.Stop("Audio Subsync Perfect!", false)
						finalSubPath = localSyncedSub
					}
				}
			}
		}

	} else if cfg.SkipSubSync {
		fmt.Println("ℹ️ [SYNC] Skipping sync because SkipSubSync is enabled.")
	} else {
		// CASE 3: Use external subtitle + Video HAS NO embedded subtitle (Audio-based sync)
		spinExt := utils.StartSpinner("⏱️ Extracting Anchor Audio for Audio Sync...")
		targetAudioIndex := videoInfo.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)

		fmt.Printf("🎙️ Original audio track selected as input: 0:a:%d\n", targetAudioIndex)
		if err := media.ExtractAudioAnchor(localVideoPath, localAnchorAudio, targetAudioIndex); err != nil {
			spinExt.Stop(fmt.Sprintf("Failed to extract Anchor Audio: %v", err), true)
		} else {
			spinExt.Stop("Successfully extracted Anchor Audio!", false)

			spinSync := utils.StartSpinner("🔄 Sending data to Subsync server (Audio-based)...")
			err := api.SyncSubtitleWithServer(cfg, extSubPath, localAnchorAudio, localSyncedSub)
			if err != nil {
				spinSync.Stop(fmt.Sprintf("Audio Subsync failed (%v), using original subtitle.", err), true)
			} else {
				spinSync.Stop("Audio Subsync Perfect!", false)
				finalSubPath = localSyncedSub
			}
		}
	}

	// ==========================================
	// PARALLEL AI VOICE SYNTHESIS
	// ==========================================
	spinTTS := utils.StartSpinner(fmt.Sprintf("🎙️ Creating AI voice for '%s' (Kokoro TTS)...", currentLang))
	ttsStart := time.Now()

	totalLines, err := media.ProcessDubbingPipeline(cfg, finalSubPath, localTtsWav, localTempDir, currentLang, videoInfo.Duration, spinTTS)
	if err != nil {
		spinTTS.Stop(fmt.Sprintf("AI Voice rendering failed: %v", err), true)
		return
	}
	ttsDuration = time.Since(ttsStart)

	fi, err := os.Stat(localTtsWav)
	if err != nil || fi.Size() == 0 {
		spinTTS.Stop("TTS audio file empty (0 byte) or does not exist -> Cancel Remux!", true)
		return
	}

	spinTTS.Stop(fmt.Sprintf("Created AI voice for %d lines! (Time: %s)", totalLines, utils.FormatDuration(ttsDuration)), false)

	// ==========================================
	// REMUX / DECOUPLED VIDEO ENCODING
	// ==========================================
	if videoInfo.IsHEVC {
		fmt.Println("    ⚡ HEVC/H.265 codec detected -> Enabling high-speed Copy Stream!")
	} else if videoInfo.IsWellCompressed {
		bitrateKbps := videoInfo.Bitrate / 1000
		fmt.Printf("    ⚡ Bitrate already optimized (%dp @ %d kbps) -> Enabling high-speed Copy Stream!\n", videoInfo.Width, bitrateKbps)
	} else {
		bitrateKbps := videoInfo.Bitrate / 1000
		encoderLabel := "GPU NVENC"
		if !cfg.UseGPU {
			encoderLabel = "CPU libx265"
		}
		fmt.Printf("    🎬 Bitrate unoptimized (%dp @ %d kbps) -> Transcoding via %s...\n", videoInfo.Width, bitrateKbps, encoderLabel)
	}

	spinRemux := utils.StartSpinner("🎜 Remuxing audio mix, embedding sub & processing video...")
	remuxStart := time.Now()

	err = media.RemuxVideo(cfg, localVideoPath, localTtsWav, finalSubPath, localOutVideo, videoInfo, isExternalSub, currentLang)
	if err != nil {
		spinRemux.Stop(fmt.Sprintf("FFmpeg Remux Error: %v", err), true)
		return
	}
	remuxDuration = time.Since(remuxStart)
	spinRemux.Stop(fmt.Sprintf("Video processing complete! (Duration: %s)", utils.FormatDuration(remuxDuration)), false)

	// ==========================================
	// SAFE NAS REPLACEMENT & VERIFICATION
	// ==========================================
	spinPush := utils.StartSpinner("🚚 Transferring finalized asset back to NAS...")
	err = utils.SafeReplaceOnNAS(localOutVideo, nasVideoPath)
	if err != nil {
		spinPush.Stop(fmt.Sprintf("Failed to transfer asset to NAS: %v", err), true)
		return
	}
	spinPush.Stop("Asset verified 100% and safely replaced on NAS!", false)

	// ==========================================
	// EXECUTION METRICS REPORT
	// ==========================================
	totalDuration := time.Since(totalStart)
	newFileInfo, err := os.Stat(nasVideoPath)
	newSize := int64(0)
	if err == nil {
		newSize = newFileInfo.Size()
	}

	fmt.Printf("\n    📊 TIME & STORAGE REPORT FOR '%s' DUB:\n", targetLang)
	fmt.Printf("        🎙️  AI Dubbing:          %s\n", utils.FormatDuration(ttsDuration))
	fmt.Printf("        ⚡ Remux / Transcode:    %s\n", utils.FormatDuration(remuxDuration))
	fmt.Printf("        ⏳ Total Execution Time: %s\n", utils.FormatDuration(totalDuration))

	if origSize > 0 && newSize > 0 {
		diff := origSize - newSize
		if diff > 0 {
			pct := (float64(diff) / float64(origSize)) * 100
			fmt.Printf("        💾 Original Size:        %s\n", utils.FormatBytes(origSize))
			fmt.Printf("        💾 Optimized Size:       %s\n", utils.FormatBytes(newSize))
			fmt.Printf("        🎉 SAVED:                %s (Reduced disk space by %.1f%%)\n", utils.FormatBytes(diff), pct)
		} else if diff < 0 {
			increase := -diff
			pct := (float64(increase) / float64(origSize)) * 100
			fmt.Printf("        💾 Original Size:        %s\n", utils.FormatBytes(origSize))
			fmt.Printf("        💾 Optimized Size:       %s (+%.1f%% due to extra audio/sub tracks)\n", utils.FormatBytes(newSize), pct)
		} else {
			fmt.Printf("        💾 Size Unchanged:       %s\n", utils.FormatBytes(origSize))
		}
	}
	fmt.Println("    --------------------------------------------------")
}

