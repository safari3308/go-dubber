package queue

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/sse"
	"github.com/safari3308/go-dubber/internal/workspace"
)

func TestQueueManager_LiveMutationPauseAndResume(t *testing.T) {
	tempDir := t.TempDir()
	v1 := filepath.Join(tempDir, "ep01.mkv")
	v2 := filepath.Join(tempDir, "ep02.mkv")
	v3 := filepath.Join(tempDir, "ep03.mkv")
	for _, p := range []string{v1, v2, v3} {
		_ = os.WriteFile(p, []byte("dummy"), 0644)
	}

	cfg := config.DefaultConfig()
	ws, err := workspace.NewManager(filepath.Join(tempDir, "workdir"))
	if err != nil {
		t.Fatalf("workspace.NewManager failed: %v", err)
	}
	broker := sse.NewBroker()

	mgr := NewManager(func() *config.Config { return cfg }, nil, ws, broker, nil)

	startedCh := make(chan string, 10)
	var completedCount atomic.Int32

	mgr.SetExecutor(func(ctx context.Context, m *Manager, job *JobItem, reuseChunks bool) error {
		startedCh <- job.FileName
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(120 * time.Millisecond):
			completedCount.Add(1)
			return nil
		}
	})

	// Add 2 videos
	added, err := mgr.AddPaths([]string{v1, v2}, nil)
	if err != nil || len(added) != 2 {
		t.Fatalf("AddPaths failed: len=%d err=%v", len(added), err)
	}

	// Start queue
	reuse := true
	if err := mgr.Start(&reuse); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for ep01 to start
	select {
	case name := <-startedCh:
		if name != "ep01.mkv" {
			t.Fatalf("expected ep01.mkv to start first, got %s", name)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for ep01 to start")
	}

	// Live add v3 while running!
	if _, err := mgr.AddPaths([]string{v3}, nil); err != nil {
		t.Fatalf("live AddPaths while running failed: %v", err)
	}

	// Live remove v2 while running!
	if err := mgr.RemoveItem(added[1].ID); err != nil {
		t.Fatalf("live RemoveItem while running failed: %v", err)
	}

	// Pause while ep01 is running
	if err := mgr.Pause(); err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	// Wait until runner reaches RunnerPaused
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Snapshot().Status == RunnerPaused {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if st := mgr.Snapshot().Status; st != RunnerPaused {
		t.Fatalf("expected RunnerPaused, got %s", st)
	}

	// Resume and let ep01 and ep03 finish
	if err := mgr.Start(nil); err != nil {
		t.Fatalf("Resume Start failed: %v", err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Snapshot().Status == RunnerIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	snap := mgr.Snapshot()
	if snap.Status != RunnerIdle {
		t.Fatalf("expected RunnerIdle after completion, got %s", snap.Status)
	}
	if len(snap.Items) != 2 {
		t.Fatalf("expected 2 remaining items (ep01, ep03), got %d", len(snap.Items))
	}
	if completedCount.Load() != 2 {
		t.Fatalf("expected 2 completed jobs, got %d", completedCount.Load())
	}
}
