package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/hardware"
	"github.com/safari3308/go-dubber/internal/media"
	"github.com/safari3308/go-dubber/internal/progress"
	"github.com/safari3308/go-dubber/internal/queue"
	"github.com/safari3308/go-dubber/internal/server"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/subtitle"
	"github.com/safari3308/go-dubber/internal/textutil"
	"github.com/safari3308/go-dubber/internal/tts"
	"github.com/safari3308/go-dubber/internal/workspace"
	"github.com/safari3308/go-dubber/web"
)

func main() {
	configFlag := flag.String("config", "config.json", "Path to config.json")
	portFlag := flag.Int("port", 0, "HTTP server port (overrides config.json server_port)")
	devFlag := flag.Bool("dev", false, "Enable CORS for Vite frontend dev server")
	cliFlag := flag.Bool("cli", false, "Run legacy unattended CLI pipeline instead of Web App")
	flag.Parse()

	configPath := config.ResolveConfigPath(*configFlag)

	if *cliFlag {
		runCLIMode(configPath)
		return
	}

	runWebServer(configPath, *portFlag, *devFlag)
}

func runWebServer(configPath string, portOverride int, devMode bool) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Printf("⚠️ Could not load %s (%v); using defaults and creating config file.", configPath, err)
		cfg = config.DefaultConfig()
		_ = config.SaveConfig(configPath, cfg)
	}

	if portOverride > 0 {
		cfg.ServerPort = portOverride
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Auto-detect machine hardware & optimal FFmpeg profile
	fmt.Println("🔍 Detecting host hardware & FFmpeg encoders...")
	hw := hardware.Detect(ctx)
	fmt.Printf("🖥️  Hardware: %s/%s (%d cores) | Encoder: %s (GPU=%v)\n",
		hw.OS, hw.Arch, hw.CPUCores, hw.EncoderLabel, hw.HasGPU)

	// 2. Initialize persistent workdir & scan for unfinished work
	ws, err := workspace.NewManager(cfg.WorkDir)
	if err != nil {
		log.Fatalf("❌ Failed to initialize workdir: %v", err)
	}
	if recovered, err := ws.ScanUnfinished(); err == nil && len(recovered) > 0 {
		fmt.Printf("♻️  Found %d unfinished job(s) in %s ready for recovery in the Web UI!\n", len(recovered), ws.RootDir())
	}

	// 3. Initialize SSE broker, TTS health monitor, and live-mutable Queue Manager
	broker := sse.NewBroker()

	var srvRefMu sync.RWMutex
	var srvRef *server.Server
	getCfg := func() *config.Config {
		srvRefMu.RLock()
		s := srvRef
		srvRefMu.RUnlock()
		if s != nil {
			return s.GetConfig()
		}
		return cfg
	}

	ttsMon := tts.NewHealthMonitor(getCfg, 10*time.Second, func(st tts.HealthStatus) {
		broker.Publish("tts_status", st)
		if st.Available {
			broker.Log("info", "", fmt.Sprintf("TTS service online at %s (%dms)", st.URL, st.LatencyMs))
		} else {
			broker.Log("warn", "", fmt.Sprintf("TTS service unreachable at %s: %s", st.URL, st.Error))
		}
	})
	ttsMon.Start(ctx)

	qm := queue.NewManager(getCfg, hw, ws, broker, ttsMon)

	webFS, err := web.DistFS()
	if err != nil {
		log.Fatalf("❌ Failed to load embedded web assets: %v", err)
	}

	srv := server.New(configPath, cfg, hw, ws, qm, broker, ttsMon, webFS, devMode)
	srvRefMu.Lock()
	srvRef = srv
	srvRefMu.Unlock()

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	go func() {
		fmt.Println("==================================================")
		fmt.Printf("🚀 Go-Dubber Web App running at http://localhost%s\n", addr)
		fmt.Printf("📁 Workdir: %s\n", ws.RootDir())
		fmt.Printf("🌐 TTS Service: %s\n", cfg.ApiUrl)
		fmt.Println("==================================================")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ HTTP server error: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\n🛑 Shutting down Go-Dubber gracefully (preserving active workdir)...")
	_ = qm.Pause()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// =========================================================================
// Legacy CLI Mode (--cli)
// =========================================================================

type PendingDub struct {
	TargetLang string
	SubChoice  subtitle.SelectedSubtitle
}

func runCLIMode(configPath string) {
	iteration := 1
	for {
		configChanged, err := runDubbingSession(configPath)
		if err != nil {
			log.Fatalf("❌ Error during pipeline execution: %v", err)
		}
		if !configChanged {
			fmt.Println("\n🎉 COMPLETED ALL MEDIA PROCESSING JOBS.")
			break
		}
		iteration++
		fmt.Printf("\n🔄 CONFIG CHANGE DETECTED: Starting run #%d...\n", iteration)
	}
}

func runDubbingSession(configPath string) (bool, error) {
	initialChecksum, err := config.GetConfigFileChecksum(configPath)
	if err != nil {
		return false, fmt.Errorf("failed to read config file checksum: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return false, fmt.Errorf("failed to load %s: %w", configPath, err)
	}

	if err := tts.CheckServerHealth(cfg); err != nil {
		fmt.Printf("⚠️ Warning: TTS API server is unreachable at %s: %v\n", cfg.ApiUrl, err)
	}

	localTempDir := "./temp"
	if err := os.MkdirAll(localTempDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create local temp directory: %w", err)
	}
	defer os.RemoveAll(localTempDir)

	videos, err := textutil.DiscoverVideoFiles(cfg.NasDir)
	if err != nil {
		log.Printf("❌ Error sweeping NAS directory: %v", err)
	}

	for _, videoPath := range videos {
		processVideoCLI(videoPath, cfg, localTempDir)
	}

	currentChecksum, err := config.GetConfigFileChecksum(configPath)
	if err == nil && currentChecksum != initialChecksum {
		return true, nil
	}
	return false, nil
}

func processVideoCLI(nasVideoPath string, cfg *config.Config, localTempDir string) {
	if err := tts.CheckServerHealth(cfg); err != nil {
		fmt.Printf("❌ [ABORT] Server TTS is down: %v -> Skip this video!\n", err)
		return
	}

	videoInfo, err := media.InspectVideo(nasVideoPath)
	if err != nil {
		fmt.Printf("    ❌ Failed to inspect video metadata: %v\n", err)
		return
	}

	var pendingDubs []PendingDub
	for _, targetLang := range cfg.DubLanguages {
		if !cfg.ForceReprocess && videoInfo.HasAudioLanguage(targetLang) {
			continue
		}
		subChoice := subtitle.SelectSubtitleForLanguage(nasVideoPath, videoInfo.EmbeddedSubStreams, cfg, targetLang, media.PromptUserSelectSub)
		if !subChoice.Found {
			continue
		}
		pendingDubs = append(pendingDubs, PendingDub{TargetLang: targetLang, SubChoice: subChoice})
	}
	if len(pendingDubs) == 0 {
		return
	}

	baseName := strings.TrimSuffix(filepath.Base(nasVideoPath), filepath.Ext(nasVideoPath))
	localVideoPath := filepath.Join(localTempDir, filepath.Base(nasVideoPath))

	spinCopy := progress.StartSpinner("📥 Downloading video from NAS to Local SSD...")
	if err := textutil.CopyFile(nasVideoPath, localVideoPath); err != nil {
		spinCopy.Stop(fmt.Sprintf("Download failed: %v", err), true)
		return
	}
	spinCopy.Stop("Downloaded video to Local SSD!", false)
	defer os.Remove(localVideoPath)

	completedDubs := 0
	for _, dub := range pendingDubs {
		localInfo, err := media.InspectVideo(localVideoPath)
		if err != nil {
			localInfo = videoInfo
		}
		if err := processSingleDubCLI(localVideoPath, localInfo, dub.SubChoice, cfg, localTempDir, dub.TargetLang, baseName); err == nil {
			completedDubs++
		}
	}

	if completedDubs > 0 {
		_ = textutil.SafeReplaceOnNAS(localVideoPath, nasVideoPath)
	}
}

func processSingleDubCLI(localVideoPath string, videoInfo *media.VideoInfo, subChoice subtitle.SelectedSubtitle, cfg *config.Config, localTempDir, targetLang, baseName string) error {
	extSubPath := subChoice.SubPath
	isExternalSub := subChoice.IsExternal
	if !isExternalSub {
		extracted := filepath.Join(localTempDir, "extracted_"+baseName+"_"+targetLang+".srt")
		if err := media.ExtractEmbeddedSubtitle(localVideoPath, extracted, subChoice.EmbeddedIndex); err != nil {
			return err
		}
		extSubPath = extracted
		defer os.Remove(extracted)
	} else if strings.ToLower(filepath.Ext(extSubPath)) != ".srt" {
		converted := filepath.Join(localTempDir, "converted_"+baseName+"_"+targetLang+".srt")
		if err := media.ConvertToSRT(extSubPath, converted); err != nil {
			return err
		}
		extSubPath = converted
		defer os.Remove(converted)
	}

	localTtsWav := filepath.Join(localTempDir, "tts_"+baseName+"_"+targetLang+".wav")
	localOutVideo := filepath.Join(localTempDir, "out_"+filepath.Base(localVideoPath))
	defer textutil.CleanTempFiles(localTtsWav, localOutVideo)

	spinTTS := progress.StartSpinner(fmt.Sprintf("🎙️ Creating AI voice for '%s'...", targetLang))
	if _, err := media.ProcessDubbingPipeline(cfg, extSubPath, localTtsWav, localTempDir, targetLang, videoInfo.Duration, spinTTS); err != nil {
		spinTTS.Stop(err.Error(), true)
		return err
	}
	spinTTS.Stop("AI voice complete!", false)

	if err := media.RemuxVideo(cfg, localVideoPath, localTtsWav, extSubPath, localOutVideo, videoInfo, isExternalSub, targetLang); err != nil {
		return err
	}
	return os.Rename(localOutVideo, localVideoPath)
}
