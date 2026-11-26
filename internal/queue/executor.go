package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/gemini"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/progress"
	"github.com/safari3308/go-dubber/internal/subtitle"
	"github.com/safari3308/go-dubber/internal/textutil"
	"github.com/safari3308/go-dubber/internal/tts"
	"github.com/safari3308/go-dubber/internal/workspace"
)

// ExecuteJob runs the full dubbing pipeline for a single queued JobItem, integrating:
// - automatic TTS health check
// - hardware-detected FFmpeg profile merged with per-item overrides
// - workdir recovery (skips re-copying verified local NAS video)
// - dual-subtitle handling (separate TTS subtitle vs. Embed ASS subtitle)
// - audio chunk reuse toggle & preservation on pause/stop
func ExecuteJob(ctx context.Context, mgr *Manager, job *JobItem, reuseChunks bool) error {
	globalCfg := mgr.GetConfig()
	ws := mgr.Workspace()
	broker := mgr.Broker()

	logInfo := func(msg string) {
		if broker != nil {
			broker.Log("info", job.ID, msg)
		}
	}
	logWarn := func(msg string) {
		if broker != nil {
			broker.Log("warn", job.ID, msg)
		}
	}

	// Build effective config combining global defaults + hardware detection + per-item overrides
	effCfg := *globalCfg
	effCfg.DubLanguages = job.Config.DubLanguages
	effCfg.OriginalLanguage = job.Config.OriginalLanguage
	effCfg.OriginalAudioIndex = job.Config.OriginalAudioIndex
	effCfg.TTSSpeed = job.Config.TTSSpeed
	effCfg.Workers = job.Config.Workers
	effCfg.UseGPU = job.Config.UseGPU
	effCfg.SkipEncode = job.Config.SkipEncode
	effCfg.SkipSubSync = job.Config.SkipSubSync
	effCfg.ForceReprocess = job.Config.ForceReprocess
	effCfg.AITrackAsFirstTrack = job.Config.AITrackAsFirstTrack
	effCfg.DropSongSubtitles = job.Config.DropSongSubtitles
	effCfg.ReuseAudioChunks = reuseChunks
	if job.Config.VolumeBoost != "" {
		effCfg.FFmpeg.VolumeBoost = job.Config.VolumeBoost
	}
	useGPU, resolvedFF := hardware.ResolveFFmpegConfig(&effCfg, mgr.Hardware())
	effCfg.UseGPU = useGPU
	effCfg.FFmpeg = resolvedFF

	// 1. Check TTS server availability (with in-browser KokoroJS fallback for English)
	remoteTTSAvailable := false
	var remoteTTSErr string
	if mon := mgr.TTSMonitor(); mon != nil {
		st := mon.CheckNow(ctx)
		remoteTTSAvailable = st.Available
		remoteTTSErr = st.Error
	} else if err := tts.CheckServerHealthContext(ctx, &effCfg); err == nil {
		remoteTTSAvailable = true
	} else {
		remoteTTSErr = err.Error()
	}

	browserBridge := mgr.BrowserTTS()
	useBrowserForEN := !remoteTTSAvailable && browserBridge != nil && browserBridge.IsReady()

	if !remoteTTSAvailable {
		if !useBrowserForEN {
			return fmt.Errorf("TTS server unavailable (%s): %s (enable Browser EN KokoroJS in header for local English fallback)", effCfg.ApiUrl, remoteTTSErr)
		}
		// Ensure all requested languages are English when relying on Browser KokoroJS
		for _, l := range effCfg.DubLanguages {
			if strings.ToLower(strings.TrimSpace(l)) != "en" {
				return fmt.Errorf("remote TTS server is offline and Browser KokoroJS fallback only supports English (en), not %q", l)
			}
		}
		logInfo("Remote TTS offline — using In-Browser KokoroJS (WebGPU/WASM) fallback for English!")
	}

	// 2. Inspect source video metadata
	logInfo(fmt.Sprintf("Inspecting %s...", job.FileName))
	mgr.UpdateJobProgress(job.ID, JobDownloading, "inspecting", 2, "Inspecting video streams...")
	videoInfo, err := media.InspectVideo(job.SourcePath)
	if err != nil {
		return fmt.Errorf("failed to inspect video: %w", err)
	}

	// Load or initialize persistent job state
	state, err := ws.LoadState(job.ID)
	if err != nil {
		var origSize, origMod int64
		if fi, statErr := os.Stat(job.SourcePath); statErr == nil {
			origSize = fi.Size()
			origMod = fi.ModTime().Unix()
		}
		state = &workspace.JobState{
			JobID:           job.ID,
			SourceVideoPath: job.SourcePath,
			SourceSize:      origSize,
			SourceModTime:   origMod,
			LocalVideoFile:  filepath.Base(ws.LocalVideoPath(job.ID, job.SourcePath)),
			Stage:           "downloading",
		}
		_ = ws.SaveState(state)
	}

	// Build language dub specs filtered to the languages ticked in effCfg.DubLanguages
	enabledLangs := make(map[string]bool, len(effCfg.DubLanguages))
	for _, l := range effCfg.DubLanguages {
		enabledLangs[strings.ToLower(strings.TrimSpace(l))] = true
	}

	var specs []subtitle.LanguageDubSpec
	specByLang := make(map[string]subtitle.LanguageDubSpec)
	for _, sp := range job.Config.LanguageSpecs {
		specByLang[strings.ToLower(strings.TrimSpace(sp.Language))] = sp
	}
	for _, lang := range effCfg.DubLanguages {
		normLang := strings.ToLower(strings.TrimSpace(lang))
		if !enabledLangs[normLang] {
			continue
		}
		if sp, ok := specByLang[normLang]; ok && sp.TTSSub.IsValid() {
			specs = append(specs, sp)
		} else if resolved := subtitle.ResolveDualSubtitle(job.SourcePath, videoInfo.EmbeddedSubStreams, &effCfg, normLang); resolved != nil {
			specs = append(specs, *resolved)
		} else if job.Config.UseGemini && job.Config.GeminiTranslate && strings.TrimSpace(effCfg.GeminiAPIKey) != "" {
			// Fallback: no subtitle in target lang, so pick the best available source subtitle to auto-translate with Gemini
			if fallbackSpec := pickFallbackSubForGeminiTranslation(job.SourcePath, videoInfo.EmbeddedSubStreams, &effCfg, normLang); fallbackSpec != nil {
				logInfo(fmt.Sprintf("[%s] No native '%s' subtitle found — using %s for Google AI auto-translation.", normLang, normLang, fallbackSpec.TTSSub.Description))
				specs = append(specs, *fallbackSpec)
			}
		}
	}

	var pendingSpecs []subtitle.LanguageDubSpec
	for _, spec := range specs {
		if state.HasCompletedLang(spec.Language) {
			logInfo(fmt.Sprintf("Skipping '%s': already completed in local workdir.", spec.Language))
			continue
		}
		if !spec.TTSSub.IsValid() {
			logWarn(fmt.Sprintf("No TTS subtitle configured for '%s', skipping.", spec.Language))
			continue
		}
		pendingSpecs = append(pendingSpecs, spec)
	}

	if len(pendingSpecs) == 0 && len(state.CompletedLangs) == 0 {
		logInfo("No pending dubs required for this video.")
		_ = ws.MarkCompletedAndCleanup(job.ID)
		return nil
	}

	// 3. Ensure local SSD video path:
	//    - If source is already on a local disk (not SMB/NFS/NAS), read directly & skip downloading!
	//    - If source is on NAS, reuse verified local SSD copy if present, otherwise download once.
	if err := ws.EnsureJobDirs(job.ID); err != nil {
		return err
	}
	isLocalSource := textutil.IsLocalPath(job.SourcePath)
	localVideoPath, hasValidCopy := ws.HasValidLocalCopy(job.ID, job.SourcePath)

	switch {
	case hasValidCopy:
		logInfo(fmt.Sprintf("Reusing existing verified local SSD copy: %s (skipped download!)", filepath.Base(localVideoPath)))
		mgr.UpdateJobProgress(job.ID, JobDownloading, "downloading", 15, "Reused verified local video copy")
	case isLocalSource:
		localVideoPath = job.SourcePath
		logInfo(fmt.Sprintf("Local file detected (%s) — skipping download and reading directly from local disk!", job.FileName))
		mgr.UpdateJobProgress(job.ID, JobDownloading, "downloading", 15, "Local file detected — skipped download")
	default:
		localVideoPath = ws.LocalVideoPath(job.ID, job.SourcePath)
		state.LocalVideoFile = filepath.Base(localVideoPath)
		state.Stage = "downloading"
		_ = ws.SaveState(state)

		logInfo(fmt.Sprintf("Network/NAS file detected — downloading %s to local SSD...", job.FileName))
		lastEmit := time.Now()
		err = textutil.CopyFileContext(ctx, job.SourcePath, localVideoPath, func(copied, total int64) {
			if total > 0 && time.Since(lastEmit) > 500*time.Millisecond {
				pct := 2.0 + (float64(copied)/float64(total))*13.0 // 2%..15%
				mgr.UpdateJobProgress(job.ID, JobDownloading, "downloading", pct,
					fmt.Sprintf("Downloading %s / %s", textutil.FormatBytes(copied), textutil.FormatBytes(total)))
				lastEmit = time.Now()
			}
		})
		if err != nil {
			return err
		}

		state.LocalCopyReady = true
		_ = ws.SaveState(state)
		mgr.UpdateJobProgress(job.ID, JobDownloading, "downloading", 15, "Downloaded video to local SSD")
	}

	totalStart := time.Now()
	completedDubs := len(state.CompletedLangs)
	langCount := len(pendingSpecs)

	// 4. Process each pending language on localVideoPath
	for idx, spec := range pendingSpecs {
		if err := ctx.Err(); err != nil {
			return err
		}
		lang := spec.Language
		basePct := 15.0 + (float64(idx)/float64(max(langCount, 1)))*75.0
		langSpan := 75.0 / float64(max(langCount, 1))

		localInfo, err := media.InspectVideo(localVideoPath)
		if err != nil {
			localInfo = videoInfo
		}

		subsDir := ws.SubsDir(job.ID)
		chunksDir := ws.ChunksDir(job.ID, lang)

		// 4a. Prepare TTS subtitle (always converted to .srt for TTS synthesis)
		ttsSrtPath, err := prepareTTSSubtitle(ctx, localVideoPath, spec, subsDir, &effCfg)
		if err != nil {
			return fmt.Errorf("[%s] failed to prepare TTS subtitle: %w", lang, err)
		}

		// 4b. Sync TTS subtitle if external & sync enabled
		if spec.SyncTTSSub && spec.TTSSub.Type == "external" {
			state.Stage = "syncing"
			_ = ws.SaveState(state)
			mgr.UpdateJobProgress(job.ID, JobSyncing, "syncing", basePct+langSpan*0.05, fmt.Sprintf("[%s] Synchronizing TTS subtitle...", lang))
			syncedPath := filepath.Join(subsDir, fmt.Sprintf("synced_%s.srt", lang))
			if synced, syncErr := syncExternalSubtitle(ctx, localVideoPath, localInfo, ttsSrtPath, syncedPath, subsDir, &effCfg); syncErr == nil && synced {
				ttsSrtPath = syncedPath
				logInfo(fmt.Sprintf("[%s] Subtitle timeline synced.", lang))
			} else if syncErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				logWarn(fmt.Sprintf("[%s] Subsync failed (%v), continuing with original timing.", lang, syncErr))
			}
		}

		// 4c. Optional Google AI (Gemini) subtitle cleanup / translation for TTS
		//     If source is .ass/.ssa, use native ASS parser to produce BOTH:
		//       - gemini_<lang>.ass (preserving styles, signs & descriptions for video embed)
		//       - gemini_<lang>.srt (strictly spoken dialogue for clean TTS voiceover)
		geminiEmbedASS := ""
		if job.Config.UseGemini && (job.Config.GeminiCleanForTTS || job.Config.GeminiTranslate) {
			if strings.TrimSpace(effCfg.GeminiAPIKey) == "" {
				logWarn(fmt.Sprintf("[%s] Gemini AI is enabled for this item, but no Gemini API key is configured in Settings — skipping AI subtitle pass.", lang))
			} else {
				geminiSrt := filepath.Join(subsDir, fmt.Sprintf("gemini_%s.srt", lang))
				geminiAss := filepath.Join(subsDir, fmt.Sprintf("gemini_%s.ass", lang))
				if textutil.FileExists(geminiSrt) {
					ttsSrtPath = geminiSrt
					if textutil.FileExists(geminiAss) {
						geminiEmbedASS = geminiAss
					}
					logInfo(fmt.Sprintf("[%s] Reusing cached Gemini-processed subtitle.", lang))
				} else {
					state.Stage = "gemini_sub"
					_ = ws.SaveState(state)
					gClient := gemini.NewClient(effCfg.GeminiAPIKey)
					style := job.Config.GeminiStyle
					if style == "" {
						style = effCfg.GeminiStyle
					}
					opts := gemini.ProcessOptions{
						TargetLang:  lang,
						Translate:   job.Config.GeminiTranslate,
						CleanForTTS: job.Config.GeminiCleanForTTS,
						Style:       style,
					}
					progressFn := func(done, total int) {
						pct := basePct + langSpan*(0.08+0.04*(float64(done)/float64(max(total, 1))))
						mgr.UpdateJobProgress(job.ID, JobSyncing, "gemini_sub", pct, fmt.Sprintf("[%s] Google AI subtitle [%d/%d]...", lang, done, total))
					}

					if spec.TTSSub.IsASS() {
						rawASS := filepath.Join(subsDir, fmt.Sprintf("raw_%s.ass", lang))
						assReady := false
						if spec.TTSSub.Type == "embedded" {
							if err := media.ExtractEmbeddedSubtitleCopyContext(ctx, localVideoPath, rawASS, spec.TTSSub.EmbeddedIndex); err == nil {
								assReady = true
							}
						} else if textutil.FileExists(spec.TTSSub.Path) {
							rawASS = spec.TTSSub.Path
							assReady = true
						}

						if assReady {
							logInfo(fmt.Sprintf("[%s] Processing ASS natively with Google AI (style=%s, clean=%v, translate=%v)...", lang, style, opts.CleanForTTS, opts.Translate))
							mgr.UpdateJobProgress(job.ID, JobSyncing, "gemini_sub", basePct+langSpan*0.08, fmt.Sprintf("[%s] Processing ASS with Google AI...", lang))
							res, gErr := gClient.ProcessASSFile(ctx, rawASS, geminiAss, geminiSrt, opts, progressFn)
							if gErr != nil {
								if ctx.Err() != nil {
									return ctx.Err()
								}
								logWarn(fmt.Sprintf("[%s] Google AI ASS processing failed (%v), continuing with original SRT.", lang, gErr))
							} else if res.DialogueCount > 0 {
								ttsSrtPath = geminiSrt
								if opts.Translate {
									geminiEmbedASS = geminiAss
								}
								logInfo(fmt.Sprintf("[%s] Google AI ASS done: %d dialogue (TTS), %d signs kept in ASS, %d skipped (filter_only=%v).", lang, res.DialogueCount, res.SignCount, res.SkippedCount, res.FilterOnlyMode))
							}
						}
					}

					// Standard SRT path (or fallback if ASS extraction wasn't available)
					if ttsSrtPath != geminiSrt {
						logInfo(fmt.Sprintf("[%s] Processing SRT with Google AI (clean=%v, translate=%v)...", lang, opts.CleanForTTS, opts.Translate))
						mgr.UpdateJobProgress(job.ID, JobSyncing, "gemini_sub", basePct+langSpan*0.08, fmt.Sprintf("[%s] Processing subtitle with Google AI...", lang))
						totalCues, keptCues, gErr := gClient.ProcessSRTFile(ctx, ttsSrtPath, geminiSrt, opts, progressFn)
						if gErr != nil {
							if ctx.Err() != nil {
								return ctx.Err()
							}
							logWarn(fmt.Sprintf("[%s] Google AI subtitle processing failed (%v), continuing with original SRT.", lang, gErr))
						} else if keptCues > 0 {
							ttsSrtPath = geminiSrt
							logInfo(fmt.Sprintf("[%s] Google AI processed subtitle (%d -> %d cues kept).", lang, totalCues, keptCues))
						}
					}
				}
			}
		}

		// 4d. Determine Embed subtitle (may be a separate .ass file for styled descriptions!)
		embedSubPath := ""
		isExternalEmbed := false
		if spec.EmbedSub != nil && spec.EmbedSub.IsValid() && spec.EmbedSub.Type == "external" {
			isExternalEmbed = true
			if !spec.EmbedSub.IsASS() && spec.EmbedSub.Path == spec.TTSSub.Path {
				// Same non-ASS file -> use synced/processed SRT if available
				embedSubPath = ttsSrtPath
			} else {
				// Separate embed subtitle (e.g. .ass with signs/descriptions) -> preserve original file!
				embedSubPath = spec.EmbedSub.Path
			}
		} else if geminiEmbedASS != "" && textutil.FileExists(geminiEmbedASS) {
			// Embed the translated styled ASS (with signs & descriptions!)
			embedSubPath = geminiEmbedASS
			isExternalEmbed = true
			logInfo(fmt.Sprintf("[%s] Embedding Google AI translated ASS subtitle (with signs & descriptions).", lang))
		} else if job.Config.UseGemini && job.Config.GeminiTranslate {
			// If Gemini translated a subtitle and the video has no native subtitle in target lang, embed the translated SRT
			hasNativeEmbedded := false
			for _, sub := range localInfo.EmbeddedSubStreams {
				if subtitle.IsSubMatchingLanguage(sub.Language, sub.Title, lang) {
					hasNativeEmbedded = true
					break
				}
			}
			if !hasNativeEmbedded && textutil.FileExists(ttsSrtPath) {
				embedSubPath = ttsSrtPath
				isExternalEmbed = true
				logInfo(fmt.Sprintf("[%s] Embedding translated subtitle into video (no native '%s' subtitle track existed).", lang, lang))
			}
		}

		// 4d. Parallel TTS synthesis with chunk reuse (or Browser KokoroJS fallback for English)
		state.Stage = "synthesizing"
		_ = ws.SaveState(state)
		ttsWavPath := filepath.Join(ws.JobDir(job.ID), fmt.Sprintf("tts_%s.wav", lang))
		logInfo(fmt.Sprintf("[%s] Starting AI voice synthesis (reuse_chunks=%v)...", lang, reuseChunks))

		var lastEmitMs atomic.Int64
		reporter := progress.FuncReporter{
			OnUpdate: func(stage string, pct float64, msg string) {
				nowMs := time.Now().UnixMilli()
				prevMs := lastEmitMs.Load()
				if pct < 99 && nowMs-prevMs < 250 {
					return
				}
				if !lastEmitMs.CompareAndSwap(prevMs, nowMs) && pct < 99 {
					return
				}
				overall := basePct + langSpan*(0.10+0.65*(pct/100.0))
				mgr.UpdateJobProgress(job.ID, JobSynthesizing, stage, overall, fmt.Sprintf("[%s] %s", lang, msg))
			},
		}

		ttsCfg := effCfg
		var customRenderer media.TTSLineRenderer
		if useBrowserForEN && strings.ToLower(strings.TrimSpace(lang)) == "en" {
			ttsCfg.Workers = 1 // Keep browser WebGPU/WASM inference sequential for stability
			customRenderer = func(rCtx context.Context, c *config.Config, text, _ string, _ string) ([]byte, error) {
				return browserBridge.Synthesize(rCtx, text, c.TTSSpeed)
			}
		}

		totalLines, err := media.ProcessDubbingPipelineContext(
			ctx,
			&ttsCfg,
			ttsSrtPath,
			ttsWavPath,
			media.DubbingPipelineOptions{
				ChunksDir:        chunksDir,
				ReuseAudioChunks: reuseChunks,
				KeepChunks:       true, // Cleaned up only after NAS upload succeeds
				TTSRenderer:      customRenderer,
			},
			lang,
			localInfo.Duration,
			reporter,
		)
		if err != nil {
			return err
		}
		logInfo(fmt.Sprintf("[%s] Synthesized %d TTS dialogue lines.", lang, totalLines))

		// 4e. Remux audio + embed subtitle (preserving ASS codec in MKV!) + encode/copy video
		state.Stage = "remuxing"
		_ = ws.SaveState(state)
		logInfo(fmt.Sprintf("[%s] Remuxing video & embedding subtitle...", lang))
		mgr.UpdateJobProgress(job.ID, JobRemuxing, "remuxing", basePct+langSpan*0.80, fmt.Sprintf("[%s] Remuxing video & embedding subtitle...", lang))

		outTempVideo := filepath.Join(ws.JobDir(job.ID), "out_"+filepath.Base(localVideoPath))
		remuxOpts := media.RemuxOptions{
			ReplaceAudioIndex: spec.ReplaceAudioIndex,
		}
		if job.Config.SourceAudioIndex != nil {
			if *job.Config.SourceAudioIndex < 0 {
				remuxOpts.NoBackgroundAudio = true
			} else {
				remuxOpts.OriginalAudioIndex = job.Config.SourceAudioIndex
			}
		} else if localInfo.AudioTrackCount == 0 {
			remuxOpts.NoBackgroundAudio = true
		}

		if err := media.RemuxVideoWithOptions(ctx, &effCfg, localVideoPath, ttsWavPath, embedSubPath, outTempVideo, localInfo, isExternalEmbed, lang, remuxOpts); err != nil {
			_ = os.Remove(outTempVideo)
			return err
		}
		_ = os.Remove(ttsWavPath)

		// Move remuxed output to the job's working file inside workdir (never overwriting original SourcePath mid-pipeline)
		workVideoPath := ws.LocalVideoPath(job.ID, job.SourcePath)
		if err := os.Rename(outTempVideo, workVideoPath); err != nil {
			if copyErr := textutil.CopyFileContext(ctx, outTempVideo, workVideoPath, nil); copyErr != nil {
				return fmt.Errorf("failed to update local video asset: %w", copyErr)
			}
			_ = os.Remove(outTempVideo)
		}
		localVideoPath = workVideoPath
		state.LocalVideoFile = filepath.Base(workVideoPath)
		state.LocalCopyReady = true

		state.MarkLangCompleted(lang)
		_ = ws.SaveState(state)
		completedDubs++
		logInfo(fmt.Sprintf("[%s] Language dub completed on local asset.", lang))
	}

	if completedDubs == 0 {
		return fmt.Errorf("no dubs succeeded")
	}

	// 5. Safe NAS replacement
	state.Stage = "uploading"
	_ = ws.SaveState(state)
	logInfo(fmt.Sprintf("Finalizing & replacing %s...", job.FileName))
	mgr.UpdateJobProgress(job.ID, JobUploading, "uploading", 92, "Uploading finalized video back to NAS...")
	if err := textutil.SafeReplaceOnNAS(localVideoPath, job.SourcePath); err != nil {
		return fmt.Errorf("failed to replace file on NAS: %w", err)
	}

	var newSize int64
	if fi, err := os.Stat(job.SourcePath); err == nil {
		newSize = fi.Size()
	}
	job.Metrics = &JobMetrics{
		OrigSizeBytes: state.SourceSize,
		NewSizeBytes:  newSize,
		DurationSec:   time.Since(totalStart).Seconds(),
		CompletedDubs: completedDubs,
	}

	// Clean up workdir ONLY after NAS upload succeeded!
	_ = ws.MarkCompletedAndCleanup(job.ID)
	logInfo(fmt.Sprintf("Completed %s (%d dub(s) in %s).", job.FileName, completedDubs, textutil.FormatDuration(time.Since(totalStart))))
	return nil
}

func prepareTTSSubtitle(ctx context.Context, localVideoPath string, spec subtitle.LanguageDubSpec, subsDir string, cfg *config.Config) (string, error) {
	lang := spec.Language
	outSrt := filepath.Join(subsDir, fmt.Sprintf("tts_%s.srt", lang))

	if spec.TTSSub.Type == "embedded" {
		if err := media.ExtractEmbeddedSubtitleContext(ctx, localVideoPath, outSrt, spec.TTSSub.EmbeddedIndex); err != nil {
			return "", err
		}
	} else {
		ext := strings.ToLower(filepath.Ext(spec.TTSSub.Path))
		if ext == ".srt" {
			if err := textutil.CopyFileContext(ctx, spec.TTSSub.Path, outSrt, nil); err != nil {
				return "", err
			}
		} else {
			if err := media.ConvertToSRTContext(ctx, spec.TTSSub.Path, outSrt); err != nil {
				return "", err
			}
		}
	}

	if cfg.DropSongSubtitles {
		_, _, _ = subtitle.FilterSongAndKaraokeSubtitles(outSrt, outSrt)
	}
	return outSrt, nil
}

// pickFallbackSubForGeminiTranslation selects the best available source subtitle (embedded text sub
// or external en/zh/ja sub) when a video lacks a native subtitle in targetLang so Gemini can translate it.
func pickFallbackSubForGeminiTranslation(videoPath string, embeddedSubs []subtitle.EmbeddedSubInfo, cfg *config.Config, targetLang string) *subtitle.LanguageDubSpec {
	// 1. Prefer an embedded English text subtitle, then any non-bitmap embedded text subtitle
	var firstText *subtitle.EmbeddedSubInfo
	for i := range embeddedSubs {
		sub := &embeddedSubs[i]
		if sub.IsBitmap {
			continue
		}
		if firstText == nil {
			firstText = sub
		}
		if strings.ToLower(sub.Language) == "en" {
			firstText = sub
			break
		}
	}
	if firstText != nil {
		return &subtitle.LanguageDubSpec{
			Language: targetLang,
			TTSSub: subtitle.SubtitleSource{
				Type:          "embedded",
				EmbeddedIndex: firstText.SubIndex,
				Format:        firstText.CodecName,
				Description:   fmt.Sprintf("Embedded track #%d (%s)", firstText.SubIndex, firstText.Language),
			},
			SyncTTSSub: false,
		}
	}

	// 2. Fall back to external subtitle in common source languages (en, zh, ja)
	for _, srcLang := range []string{"en", "zh", "ja"} {
		if srcLang == targetLang {
			continue
		}
		cands := subtitle.DiscoverExternalSubtitlesForLanguage(videoPath, srcLang, cfg)
		if len(cands) > 0 {
			best := cands[0]
			return &subtitle.LanguageDubSpec{
				Language: targetLang,
				TTSSub: subtitle.SubtitleSource{
					Type:        "external",
					Path:        best.Path,
					Format:      best.Format,
					Description: fmt.Sprintf("External %s: %s", strings.ToUpper(best.Format), best.FileName),
				},
				SyncTTSSub: !cfg.SkipSubSync,
			}
		}
	}

	return nil
}

func syncExternalSubtitle(ctx context.Context, localVideoPath string, info *media.VideoInfo, targetSrt, outSyncedSrt, subsDir string, cfg *config.Config) (bool, error) {
	// Try Sub-to-Sub sync first if a non-bitmap embedded subtitle exists
	for i := range info.EmbeddedSubStreams {
		if !info.EmbeddedSubStreams[i].IsBitmap {
			refSubPath := filepath.Join(subsDir, "ref_embedded.srt")
			if err := media.ExtractEmbeddedSubtitleContext(ctx, localVideoPath, refSubPath, info.EmbeddedSubStreams[i].SubIndex); err == nil {
				if cfg.DropSongSubtitles {
					_, _, _ = subtitle.FilterSongAndKaraokeSubtitles(refSubPath, refSubPath)
				}
				if err := tts.SyncSubtitleWithServerContext(ctx, cfg, targetSrt, refSubPath, outSyncedSrt); err == nil {
					return true, nil
				}
			}
			break
		}
	}

	// Fallback to Audio-based sync
	anchorPath := filepath.Join(subsDir, "anchor.aac")
	defer os.Remove(anchorPath)
	audioIdx := info.SelectOriginalAudioIndex(cfg.OriginalLanguage, cfg.OriginalAudioIndex)
	if err := media.ExtractAudioAnchorContext(ctx, localVideoPath, anchorPath, audioIdx); err != nil {
		return false, err
	}
	if err := tts.SyncSubtitleWithServerContext(ctx, cfg, targetSrt, anchorPath, outSyncedSrt); err != nil {
		return false, err
	}
	return true, nil
}
