package textutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLocalPath(t *testing.T) {
	tempDir := t.TempDir()
	f := filepath.Join(tempDir, "local_video.mkv")
	if err := os.WriteFile(f, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if !IsLocalPath(f) {
		t.Errorf("expected temp file %q on local disk to be detected as local", f)
	}
}
