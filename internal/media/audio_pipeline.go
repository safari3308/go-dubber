package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/progress"
	"github.com/safari3308/go-dubber/internal/subtitle"
	"github.com/safari3308/go-dubber/internal/tts"
)

// TTSLineRenderer synthesizes a single dialogue line into WAV bytes.
type TTSLineRenderer func(ctx context.Context, cfg *config.Config, text, lang, role string) ([]byte, error)

// DubbingPipelineOptions configures chunk caching and cancellation behavior.
type DubbingPipelineOptions struct {
	ChunksDir        string          // Explicit directory for chunk_*.wav; if empty, uses localTempDir/chunks_<wav>
	ReuseAudioChunks bool            // When true, reuse existing valid chunk_*.wav files; when false, discard them before synthesis
	KeepChunks       bool            // When true, keep chunk_*.wav files after packaging (cleaned up only after NAS upload)
	TTSRenderer      TTSLineRenderer // Optional custom renderer (e.g. in-browser kokoro-js fallback); defaults to tts.RenderSingleLineTTSContext
}

// ExtractPCMFromWAV finds the exact position of the 'data' chunk in WAV (handles any Header length).
func ExtractPCMFromWAV(wavData []byte) []byte {
	dataIdx := bytes.Index(wavData, []byte("data"))
	if dataIdx == -1 || len(wavData) < dataIdx+8 {
		if len(wavData) > 44 {
			return wavData[44:]
		}
		return nil
	}
	return wavData[dataIdx+8:]
}

// TrimSilencePCM16 trims leading/trailing silent PCM 16-bit samples safely.
func TrimSilencePCM16(pcm []byte, threshold int16) []byte {
	if len(pcm) < 4 {
		return pcm
	}
	end := len(pcm) - (len(pcm) % 2)
	start := 0

	for i := 0; i <= end-2; i += 2 {
		sample := int16(pcm[i]) | (int16(pcm[i+1]) << 8)
		if sample > threshold || sample < -threshold {
			start = i
			break
		}
	}

	for i := end - 2; i >= start; i -= 2 {
		sample := int16(pcm[i]) | (int16(pcm[i+1]) << 8)
		if sample > threshold || sample < -threshold {
			end = i + 2
			break
		}
	}

	if start >= end {
		return pcm
	}
	return pcm[start:end]
}

// MixPCM16 mixes PCM 16-bit audio samples onto canvas buffer.
func MixPCM16(canvas []byte, pcm []byte, startByte int) {
	if startByte%2 != 0 {
		startByte--
	}
	for i := 0; i < len(pcm)-1; i += 2 {
		targetIdx := startByte + i
		if targetIdx+1 >= len(canvas) {
			break
		}
		currSample := int16(canvas[targetIdx]) | (int16(canvas[targetIdx+1]) << 8)
		newSample := int16(pcm[i]) | (int16(pcm[i+1]) << 8)
		mixed := int32(currSample) + int32(newSample)
		if mixed > 32767 {
			mixed = 32767
		} else if mixed < -32768 {
			mixed = -32768
		}
		canvas[targetIdx] = byte(mixed & 0xff)
		canvas[targetIdx+1] = byte((mixed >> 8) & 0xff)
	}
}

// ProcessDubbingPipeline renders dialogue lines in parallel and places them on exact timeline.
func ProcessDubbingPipeline(cfg *config.Config, srtPath, outWavPath, localTempDir, lang string, videoDuration float64, reporter progress.Reporter) (int, error) {
	return ProcessDubbingPipelineContext(
		context.Background(),
		cfg,
		srtPath,
		outWavPath,
		DubbingPipelineOptions{
			ChunksDir:        filepath.Join(localTempDir, "chunks_"+filepath.Base(outWavPath)),
			ReuseAudioChunks: cfg.ReuseAudioChunks,
			KeepChunks:       false,
		},
		lang,
		videoDuration,
		reporter,
	)
}

// ProcessDubbingPipelineContext renders dialogue lines with context cancellation and optional audio chunk reuse.
func ProcessDubbingPipelineContext(ctx context.Context, cfg *config.Config, srtPath, outWavPath string, opts DubbingPipelineOptions, lang string, videoDuration float64, reporter progress.Reporter) (int, error) {
	if reporter == nil {
		reporter = progress.NopReporter{}
	}

	entries, err := subtitle.ParseSRT(srtPath, lang)
	if err != nil || len(entries) == 0 {
		return 0, fmt.Errorf("failed to read SRT subtitle or empty file: %v", err)
	}

	// PROTECTION LAYER 1: Remove ALL subtitle lines outside video duration (e.g., Credit subs at the end)
	var validEntries []subtitle.SubEntry
	for _, entry := range entries {
		if entry.StartSec < videoDuration {
			validEntries = append(validEntries, entry)
		}
	}
	entries = validEntries
	if len(entries) == 0 {
		return 0, fmt.Errorf("no valid subtitle entries within video duration")
	}

	chunksDir := opts.ChunksDir
	if chunksDir == "" {
		chunksDir = filepath.Join(filepath.Dir(outWavPath), "chunks_"+filepath.Base(outWavPath))
	}

	// If user toggled OFF audio chunk reuse, discard any existing chunks first.
	if !opts.ReuseAudioChunks {
		_ = os.RemoveAll(chunksDir)
	}
	if err := os.MkdirAll(chunksDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create chunks dir: %w", err)
	}

	totalEntries := len(entries)
	sampleRate := 24000
	bytesPerSample := 2
	numWorkers := cfg.Workers
	if numWorkers <= 0 {
		numWorkers = 2
	}

	type Job struct {
		Index int
		Entry subtitle.SubEntry
	}

	jobs := make(chan Job, totalEntries)
	var completedCount int32
	var reusedCount int32
	var wg sync.WaitGroup

	reporter.Update("synthesizing", 0, fmt.Sprintf("🎙️ AI voice rendering [0/%d - 0.0%%] (%d workers)...", totalEntries, numWorkers))

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk_%04d.wav", job.Index))
				reused := false
				if opts.ReuseAudioChunks {
					if info, err := os.Stat(chunkFile); err == nil && !info.IsDir() && info.Size() > 44 {
						reused = true
						atomic.AddInt32(&reusedCount, 1)
					}
				}
				if !reused {
					renderFn := opts.TTSRenderer
					if renderFn == nil {
						renderFn = tts.RenderSingleLineTTSContext
					}
					wavData, err := renderFn(ctx, cfg, job.Entry.Text, lang, "A")
					if err != nil && ctx.Err() == nil {
						wavData, err = renderFn(ctx, cfg, job.Entry.Text, lang, "A")
					}
					if ctx.Err() != nil {
						return
					}
					if err == nil && len(wavData) > 0 {
						tmpChunk := chunkFile + ".tmp"
						if wErr := os.WriteFile(tmpChunk, wavData, 0644); wErr == nil {
							_ = os.Rename(tmpChunk, chunkFile)
						}
					}
				}
				current := atomic.AddInt32(&completedCount, 1)
				percent := float64(current) / float64(totalEntries) * 100
				reporter.Update("synthesizing", percent, fmt.Sprintf("🎙️ AI voice rendering [%d/%d - %.1f%%] (%d workers, %d reused)...", current, totalEntries, percent, numWorkers, atomic.LoadInt32(&reusedCount)))
			}
		}()
	}

enqueueLoop:
	for i, entry := range entries {
		select {
		case <-ctx.Done():
			break enqueueLoop
		case jobs <- Job{Index: i, Entry: entry}:
		}
	}
	close(jobs)
	wg.Wait()

	// On pause/stop/cancel, preserve chunksDir so work can be resumed!
	if err := ctx.Err(); err != nil {
		return int(atomic.LoadInt32(&completedCount)), err
	}

	reporter.Update("packaging", 99, fmt.Sprintf("📦 Packaging WAV payload (%d lines)...", totalEntries))

	// PROTECTION LAYER 2: Fixed Canvas Size (100% match with Video Duration)
	canvasBytes := make([]byte, int(videoDuration*float64(sampleRate))*bytesPerSample)

	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return totalEntries, err
		}
		chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk_%04d.wav", i))
		wavData, err := os.ReadFile(chunkFile)
		if err != nil || len(wavData) <= 44 {
			continue
		}

		pcmData := ExtractPCMFromWAV(wavData)
		if len(pcmData) == 0 {
			continue
		}

		pcmData = TrimSilencePCM16(pcmData, 300)
		if len(pcmData) == 0 {
			continue
		}

		audioDuration := float64(len(pcmData)) / float64(sampleRate*bytesPerSample)
		targetMaxSec := entry.EndSec - entry.StartSec
		if i < len(entries)-1 {
			nextStartGap := entries[i+1].StartSec - entry.StartSec
			if nextStartGap > 0 && nextStartGap < targetMaxSec {
				targetMaxSec = nextStartGap
			}
		}
		if targetMaxSec < 0.5 {
			targetMaxSec = 0.5
		}

		if audioDuration > targetMaxSec+0.1 {
			speedup := audioDuration / targetMaxSec
			if speedup > 1.5 {
				speedup = 1.5
			}
			if speedup >= 1.05 {
				if respeededWav, err := adjustChunkSpeedContext(ctx, chunkFile, speedup); err == nil {
					respeededPcm := ExtractPCMFromWAV(respeededWav)
					respeededPcm = TrimSilencePCM16(respeededPcm, 300)
					if len(respeededPcm) > 0 {
						pcmData = respeededPcm
					}
				}
			}
		}

		startByte := int(entry.StartSec*float64(sampleRate)) * bytesPerSample
		endByte := startByte + len(pcmData)

		// PROTECTION LAYER 3: Truncate PCM if AI dialogue extends beyond video duration
		if startByte >= len(canvasBytes) {
			continue
		}
		if endByte > len(canvasBytes) {
			pcmData = pcmData[:len(canvasBytes)-startByte]
		}

		MixPCM16(canvasBytes, pcmData, startByte)
	}

	header := createWAVHeader(len(canvasBytes), sampleRate, 1, 16)
	finalWav := append(header, canvasBytes...)

	err = os.WriteFile(outWavPath, finalWav, 0644)
	if err == nil && !opts.KeepChunks {
		_ = os.RemoveAll(chunksDir)
	}
	return totalEntries, err
}

func adjustChunkSpeedContext(ctx context.Context, inputWavPath string, speedup float64) ([]byte, error) {
	outWavPath := inputWavPath + ".speed.wav"
	defer os.Remove(outWavPath)

	filter := fmt.Sprintf("atempo=%.4f", speedup)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", inputWavPath, "-filter:a", filter, outWavPath)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(outWavPath)
}

func createWAVHeader(dataLen, sampleRate, numChannels, bitsPerSample int) []byte {
	header := make([]byte, 44)
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	totalLen := dataLen + 36

	copy(header[0:4], []byte("RIFF"))
	header[4] = byte(totalLen & 0xff)
	header[5] = byte((totalLen >> 8) & 0xff)
	header[6] = byte((totalLen >> 16) & 0xff)
	header[7] = byte((totalLen >> 24) & 0xff)
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	header[16] = 16
	header[20] = 1
	header[22] = byte(numChannels)
	header[24] = byte(sampleRate & 0xff)
	header[25] = byte((sampleRate >> 8) & 0xff)
	header[26] = byte((sampleRate >> 16) & 0xff)
	header[27] = byte((sampleRate >> 24) & 0xff)
	header[28] = byte(byteRate & 0xff)
	header[29] = byte((byteRate >> 8) & 0xff)
	header[30] = byte((byteRate >> 16) & 0xff)
	header[31] = byte((byteRate >> 24) & 0xff)
	header[32] = byte(blockAlign)
	header[34] = byte(bitsPerSample)
	copy(header[36:40], []byte("data"))
	header[40] = byte(dataLen & 0xff)
	header[41] = byte((dataLen >> 8) & 0xff)
	header[42] = byte((dataLen >> 16) & 0xff)
	header[43] = byte((dataLen >> 24) & 0xff)

	return header
}
