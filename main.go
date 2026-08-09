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
		if ext == ".mp4" || ext == ".mkv" || ext == ".avi" {
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
	Language      string // "vi" or "en"
	IsExternal    bool
	EmbeddedIndex int // 0-based index of subtitle stream
	Description   string
	Found         bool
}

func findExternalViSubtitle(videoPath string) (string, bool) {
	dir := filepath.Dir(videoPath)
	baseName := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

	viCandidates := []string{
		filepath.Join(dir, baseName+".vi.srt"),
		filepath.Join(dir, baseName+".vie.srt"),
		filepath.Join(dir, baseName+".vi.ass"),
		filepath.Join(dir, baseName+".vie.ass"),
		filepath.Join(dir, baseName+"_vi.srt"),
		filepath.Join(dir, baseName+"_vie.srt"),
		filepath.Join(dir, baseName+".srt"),
		filepath.Join(dir, baseName+".ass"),
		filepath.Join(dir, baseName+".vtt"),
	}
	for _, candidate := range viCandidates {
		if utils.FileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func findExternalEnSubtitle(videoPath string) (string, bool) {
	dir := filepath.Dir(videoPath)
	baseName := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

	enCandidates := []string{
		filepath.Join(dir, baseName+".en.srt"),
		filepath.Join(dir, baseName+".eng.srt"),
		filepath.Join(dir, baseName+".en.ass"),
		filepath.Join(dir, baseName+".eng.ass"),
		filepath.Join(dir, baseName+"_en.srt"),
		filepath.Join(dir, baseName+"_eng.srt"),
	}
	for _, candidate := range enCandidates {
		if utils.FileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func selectSubtitle(videoPath string, videoInfo *media.VideoInfo, cfg *config.Config) SelectedSubtitle {
	// Priority 0: force fallback sub
	if cfg.ForceFallbackSub {
		return fallbackEmbedSub(cfg, videoInfo)
	}

	// Priority 1: Embedded Vietnamese subtitle
	for _, sub := range videoInfo.EmbeddedSubStreams {
		if sub.Language == "vi" {
			desc := fmt.Sprintf("Selected embedded Vietnamese subtitle (track #%d", sub.SubIndex)
			if sub.Title != "" {
				desc += fmt.Sprintf(" - %s", sub.Title)
			}
			desc += ")"
			return SelectedSubtitle{
				Language:      "vi",
				IsExternal:    false,
				EmbeddedIndex: sub.SubIndex,
				Description:   desc,
				Found:         true,
			}
		}
	}

	// Priority 2: External Vietnamese subtitle
	if extViPath, found := findExternalViSubtitle(videoPath); found {
		return SelectedSubtitle{
			SubPath:     extViPath,
			Language:    "vi",
			IsExternal:  true,
			Description: fmt.Sprintf("Selected external Vietnamese subtitle: %s", filepath.Base(extViPath)),
			Found:       true,
		}
	}

	// 🌟 INTERACTIVE MODE: Hỏi người dùng chọn bằng tay nếu bật InteractiveMode và không tự động thấy VietSub
	if cfg.InteractiveMode && len(videoInfo.EmbeddedSubStreams) > 0 {
		fmt.Println("    ⚠️ Không tự động tìm thấy VietSub nhúng hoặc VietSub ngoài.")
		userSelected := media.PromptUserSelectSub(videoInfo.EmbeddedSubStreams)
		if userSelected != nil {
			desc := fmt.Sprintf("Manual selected embedded subtitle (track #%d", userSelected.SubIndex)
			if userSelected.Title != "" {
				desc += fmt.Sprintf(" - %s", userSelected.Title)
			}
			desc += ")"
			
			// Giả định sub người dùng chọn thủ công là VietSub (tiếng Việt)
			return SelectedSubtitle{
				Language:      "vi",
				IsExternal:    false,
				EmbeddedIndex: userSelected.SubIndex,
				Description:   desc,
				Found:         true,
			}
		}
	}

	// Priority 3: Embedded English subtitle
	for _, sub := range videoInfo.EmbeddedSubStreams {
		if sub.Language == "en" {
			desc := fmt.Sprintf("Selected embedded English subtitle (track #%d", sub.SubIndex)
			if sub.Title != "" {
				desc += fmt.Sprintf(" - %s", sub.Title)
			}
			desc += ")"
			return SelectedSubtitle{
				Language:      "en",
				IsExternal:    false,
				EmbeddedIndex: sub.SubIndex,
				Description:   desc,
				Found:         true,
			}
		}
	}

	// Priority 4: External English subtitle
	if extEnPath, found := findExternalEnSubtitle(videoPath); found {
		return SelectedSubtitle{
			SubPath:     extEnPath,
			Language:    "en",
			IsExternal:  true,
			Description: fmt.Sprintf("Selected external English subtitle: %s", filepath.Base(extEnPath)),
			Found:       true,
		}
	}

	// Priority 5: Fallback to first available embedded subtitle stream
	return fallbackEmbedSub(cfg, videoInfo)
}

func fallbackEmbedSub(cfg *config.Config, videoInfo *media.VideoInfo) SelectedSubtitle {
	if len(videoInfo.EmbeddedSubStreams) > 0 {
		if cfg.DefaultSubIndex >= len(videoInfo.EmbeddedSubStreams) {
			fmt.Println("    ⚠️ Default subtitle index is out of range. Using first subtitle.")
			cfg.DefaultSubIndex = 0
		}
		sub := videoInfo.EmbeddedSubStreams[cfg.DefaultSubIndex]
		desc := fmt.Sprintf("Selected embedded subtitle (track #%d, fallback)", sub.SubIndex)
		lang := sub.Language
		if lang == "unknown" {
			lang = cfg.SubLanguage
		}
		return SelectedSubtitle{
			Language:      lang,
			IsExternal:    false,
			EmbeddedIndex: sub.SubIndex,
			Description:   desc,
			Found:         true,
		}
	}
	return SelectedSubtitle{Found: false}
}

func processVideo(nasVideoPath string, cfg *config.Config, localTempDir string) {
	// 🌟 LỚP BẢO VỆ 1: Kiểm tra Server TTS còn sống không trước khi làm bất cứ việc gì (tránh tải video NAS vô ích)
	if err := api.CheckServerHealth(cfg); err != nil {
		fmt.Printf("❌ [ABORT] Server TTS ngưng hoạt động: %v -> Bỏ qua video này!\n", err)
		return
	}

	fmt.Printf("\n🎬 Inspecting: %s\n", filepath.Base(nasVideoPath))

	// Timer init
	totalStart := time.Now()
	var ttsDuration, remuxDuration time.Duration

	// Initial file size measurement on NAS
	origFileInfo, err := os.Stat(nasVideoPath)
	origSize := int64(0)
	if err == nil {
		origSize = origFileInfo.Size()
	}

	baseName := strings.TrimSuffix(filepath.Base(nasVideoPath), filepath.Ext(nasVideoPath))
	localVideoPath := filepath.Join(localTempDir, filepath.Base(nasVideoPath))

	// ==========================================
	// STEP 1: FAST METADATA PROBE OVER NAS NETWORK (<0.5S)
	// ==========================================
	videoInfo, err := media.InspectVideo(nasVideoPath)
	if err != nil {
		fmt.Printf("    ❌ Failed to inspect video metadata: %v\n", err)
		return
	}

	// Skip logic based on existing tracks and configuration
	if !cfg.ForceReprocess {
		if videoInfo.HasKokoroTrack {
			fmt.Println("    ⏭️ Skipped: Video ALREADY contains Kokoro AI voiceover track.")
			return
		}
		if !cfg.OnlyCheckKokoro && videoInfo.HasGenericDubTrack {
			fmt.Println("    ⏭️ Skipped: Video contains existing Vietnamese dub track.")
			return
		}
	}

	// ==========================================
	// STEP 2: SELECT BEST SUBTITLE
	// Priority: Embedded VI -> External VI -> Embedded EN -> External EN
	// ==========================================
	subChoice := selectSubtitle(nasVideoPath, videoInfo, cfg)
	if !subChoice.Found {
		fmt.Println("    ⚠️ No external or embedded subtitles found. Skipping video.")
		return
	}
	fmt.Printf("    💬 %s\n", subChoice.Description)

	// ==========================================
	// STEP 3: CONFIRM DUBBING ELIGIBILITY -> DOWNLOAD VIDEO TO LOCAL SSD
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
	currentLang := subChoice.Language
	isExternalSub := subChoice.IsExternal

	// If using embedded subtitle, extract subtitle track from local file
	if !isExternalSub {
		fmt.Printf("    📦 Extracting embedded subtitle (track #%d) from local video...\n", subChoice.EmbeddedIndex)
		extractedSubPath := filepath.Join(localTempDir, "extracted_"+baseName+".srt")

		err := media.ExtractEmbeddedSubtitle(localVideoPath, extractedSubPath, subChoice.EmbeddedIndex)
		if err != nil {
			fmt.Printf("    ❌ Failed to extract embedded subtitle: %v -> Skipping.\n", err)
			_ = os.Remove(localVideoPath)
			return
		}

		extSubPath = extractedSubPath
		fmt.Println("    ✅ Embedded subtitle extracted successfully!")
	}

	// Define temporary file paths on local SSD
	subExt := filepath.Ext(extSubPath)
	localAnchorAudio := filepath.Join(localTempDir, "anchor_"+baseName+".aac")
	localSyncedSub := filepath.Join(localTempDir, "synced_"+baseName+subExt)
	localTtsWav := filepath.Join(localTempDir, "tts_"+baseName+".wav")
	localOutVideo := filepath.Join(localTempDir, "out_"+filepath.Base(nasVideoPath))

	// Register temp file cleanup
	defer utils.CleanTempFiles(localVideoPath, localAnchorAudio, localSyncedSub, localTtsWav, localOutVideo)
	if !isExternalSub {
		defer os.Remove(extSubPath)
	}

	// ==========================================
	// STEP 4 & 5: SUBTITLE SYNCHRONIZATION LOGIC
	// ==========================================
	finalSubPath := extSubPath

	if !isExternalSub {
		// 🌟 KỊCH BẢN 1: Dùng Sub nhúng (Embedded)
		// Bỏ qua sync hoàn toàn -> KHÔNG extract anchor audio vô ích
		fmt.Println("ℹ️ [SYNC] Bỏ qua sync vì đang sử dụng Subtitle nhúng sẵn.")

	} else if len(videoInfo.EmbeddedSubStreams) > 0 {
		// 🌟 KỊCH BẢN 2: Dùng Sub ngoài + Video CÓ Sub nhúng
		// Ưu tiên Sync Sub-với-Sub (Nhanh, chuẩn 100%, không lo giọng thì thào)
		refSubStream := videoInfo.EmbeddedSubStreams[0] // Lấy track sub nhúng đầu tiên làm mốc
		localRefSubPath := filepath.Join(localTempDir, "ref_embedded.srt")

		spinExtractRef := utils.StartSpinner(fmt.Sprintf("📜 Trích xuất Sub nhúng #%d làm mốc tham chiếu...", refSubStream.SubIndex))
		if err := media.ExtractEmbeddedSubtitle(localVideoPath, localRefSubPath, refSubStream.SubIndex); err != nil {
			spinExtractRef.Stop(fmt.Sprintf("Lỗi trích xuất Sub mốc: %v", err), true)
		} else {
			spinExtractRef.Stop("Đã trích xuất Sub nhúng làm mốc thành công!", false)

			spinSync := utils.StartSpinner("🔄 Đang đồng bộ Sub ngoài với Sub nhúng (Sub-to-Sub)...")
			// Gọi API Sync Sub-với-Sub (truyền file sub mốc thay vì file audio)
			err := api.SyncSubtitleWithServer(cfg, extSubPath, localRefSubPath, localSyncedSub)
			if err != nil {
				spinSync.Stop(fmt.Sprintf("Sync Sub-to-Sub thất bại (%v), dùng sub gốc.", err), true)
			} else {
				spinSync.Stop("Đồng bộ Timeline Sub-to-Sub hoàn hảo!", false)
				finalSubPath = localSyncedSub
			}
		}

	} else {
		// 🌟 KỊCH BẢN 3: Dùng Sub ngoài + Video KHÔNG CÓ Sub nhúng nào
		// Fallback: Mới phải Extract Audio để Sync với sóng âm
		spinExt := utils.StartSpinner("⏱️ Trích xuất Anchor Audio để phục vụ Sync âm thanh...")
		// Lấy index track audio gốc chuẩn xác nhất theo config
		targetAudioIndex := videoInfo.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)

		// Sử dụng targetAudioIndex truyền vào lệnh FFmpeg (Ví dụ: 0:a:targetAudioIndex)
		fmt.Printf("🎙️ Track audio gốc được chọn làm Input: 0:a:%d\n", targetAudioIndex)
		if err := media.ExtractAudioAnchor(localVideoPath, localAnchorAudio, targetAudioIndex); err != nil {
			spinExt.Stop(fmt.Sprintf("Trích xuất Anchor Audio thất bại: %v", err), true)
		} else {
			spinExt.Stop("Trích xuất Anchor Audio thành công!", false)

			spinSync := utils.StartSpinner("🔄 Gửi dữ liệu tới Subsync server (Audio-based)...")
			err := api.SyncSubtitleWithServer(cfg, extSubPath, localAnchorAudio, localSyncedSub)
			if err != nil {
				spinSync.Stop(fmt.Sprintf("Audio Subsync thất bại (%v), dùng sub gốc.", err), true)
			} else {
				spinSync.Stop("Đồng bộ Sub với Audio thành công!", false)
				finalSubPath = localSyncedSub
			}
		}
	}

	// ==========================================
	// STEP 6: PARALLEL AI VOICE SYNTHESIS
	// ==========================================
	spinTTS := utils.StartSpinner("🎙️ Đang tạo giọng đọc AI (Kokoro TTS)...")
	ttsStart := time.Now()

	totalLines, err := media.ProcessDubbingPipeline(cfg, finalSubPath, localTtsWav, localTempDir, currentLang, videoInfo.Duration, spinTTS)
	if err != nil {
		spinTTS.Stop(fmt.Sprintf("AI Voice rendering failed: %v", err), true)
		return
	}
	ttsDuration = time.Since(ttsStart)

	// 🌟 LỚP BẢO VỆ 3: Kiểm tra dung lượng file WAV đầu ra (Tránh trường hợp server trả về file 0 byte)
	fi, err := os.Stat(localTtsWav)
	if err != nil || fi.Size() == 0 {
		spinTTS.Stop("File âm thanh TTS rỗng (0 byte) hoặc không tồn tại -> Hủy Remux!", true)
		return // 🔴 HỦY NGAY TẠI ĐÂY
	}

	spinTTS.Stop(fmt.Sprintf("Tạo giọng đọc AI hoàn tất cho %d câu thoại! (Thời gian: %s)", totalLines, utils.FormatDuration(ttsDuration)), false)

	// ==========================================
	// STEP 7: REMUX / DECOUPLED VIDEO ENCODING
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
	// STEP 8: SAFE NAS REPLACEMENT & VERIFICATION
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

	fmt.Println("\n    📊 TIME & STORAGE REPORT:")
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

