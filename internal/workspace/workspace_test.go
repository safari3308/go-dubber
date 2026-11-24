package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspace_RecoveryAndChunkReuse(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "workdir")
	nasVideo := filepath.Join(tempDir, "Episode01.mkv")
	videoBytes := []byte("simulated 4GB nas video payload")
	if err := os.WriteFile(nasVideo, videoBytes, 0644); err != nil {
		t.Fatalf("failed to write nas video: %v", err)
	}

	mgr, err := NewManager(workDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	jobID := "job-001"
	if err := mgr.EnsureJobDirs(jobID); err != nil {
		t.Fatalf("EnsureJobDirs failed: %v", err)
	}

	// Simulate completed NAS->local copy
	localPath := mgr.LocalVideoPath(jobID, nasVideo)
	if err := os.WriteFile(localPath, videoBytes, 0644); err != nil {
		t.Fatalf("failed to write local copy: %v", err)
	}

	state := &JobState{
		JobID:           jobID,
		SourceVideoPath: nasVideo,
		SourceSize:      int64(len(videoBytes)),
		LocalVideoFile:  filepath.Base(localPath),
		LocalCopyReady:  true,
		Stage:           "synthesizing",
	}
	if err := mgr.SaveState(state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	// Simulate 2 cached TTS chunks (>44 bytes each) and 1 corrupt empty chunk
	chunksDir := mgr.ChunksDir(jobID, "vi")
	_ = os.MkdirAll(chunksDir, 0755)
	validWav := make([]byte, 128)
	_ = os.WriteFile(filepath.Join(chunksDir, "chunk_0000.wav"), validWav, 0644)
	_ = os.WriteFile(filepath.Join(chunksDir, "chunk_0001.wav"), validWav, 0644)
	_ = os.WriteFile(filepath.Join(chunksDir, "chunk_0002.wav"), []byte("tiny"), 0644)

	// Verify HasValidLocalCopy skips re-copy
	gotLocal, ok := mgr.HasValidLocalCopy(jobID, nasVideo)
	if !ok || gotLocal != localPath {
		t.Fatalf("HasValidLocalCopy = (%q, %v); want (%q, true)", gotLocal, ok, localPath)
	}

	// Verify ScanUnfinished finds the interrupted job + valid chunks
	recovered, err := mgr.ScanUnfinished()
	if err != nil {
		t.Fatalf("ScanUnfinished failed: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected 1 recoverable job, got %d", len(recovered))
	}
	if !recovered[0].LocalVideoValid {
		t.Errorf("expected LocalVideoValid=true")
	}
	if recovered[0].TotalChunks != 2 || recovered[0].ChunkCounts["vi"] != 2 {
		t.Errorf("expected 2 valid vi chunks, got total=%d counts=%v", recovered[0].TotalChunks, recovered[0].ChunkCounts)
	}

	// Test ClearChunks (when ReuseAudioChunks toggle is OFF)
	if err := mgr.ClearChunks(jobID, "vi"); err != nil {
		t.Fatalf("ClearChunks failed: %v", err)
	}
	if cnt := mgr.CountValidChunks(jobID, "vi"); cnt != 0 {
		t.Errorf("expected 0 chunks after ClearChunks, got %d", cnt)
	}

	// Test DiscardJob
	if err := mgr.DiscardJob(jobID); err != nil {
		t.Fatalf("DiscardJob failed: %v", err)
	}
	recoveredAfter, _ := mgr.ScanUnfinished()
	if len(recoveredAfter) != 0 {
		t.Errorf("expected 0 recoverable jobs after DiscardJob, got %d", len(recoveredAfter))
	}
}
