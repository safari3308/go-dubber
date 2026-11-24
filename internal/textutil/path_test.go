package textutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizePath(t *testing.T) {
	tempDir := t.TempDir()
	unicodeFile := filepath.Join(tempDir, "My Senior Brother is Too Steady 168 TMPĐ_Giacobe Ngọc.mkv")
	if err := os.WriteFile(unicodeFile, []byte("video"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// 1. macOS Finder Copy as Pathname wrapped in single quotes with Vietnamese Unicode
	singleQuoted := "'" + unicodeFile + "'"
	if got := SanitizePath(singleQuoted); got != unicodeFile {
		t.Errorf("SanitizePath(singleQuoted) = %q; want %q", got, unicodeFile)
	}

	// 2. Double-quoted with surrounding whitespace
	doubleQuoted := `  "` + unicodeFile + `"  `
	if got := SanitizePath(doubleQuoted); got != unicodeFile {
		t.Errorf("SanitizePath(doubleQuoted) = %q; want %q", got, unicodeFile)
	}

	// 3. Shell-escaped spaces (Terminal drag-and-drop)
	escapedFile := filepath.Join(tempDir, "My\\ Senior\\ Brother\\ is\\ Too\\ Steady\\ 168\\ TMPĐ_Giacobe\\ Ngọc.mkv")
	if got := SanitizePath(escapedFile); got != unicodeFile {
		t.Errorf("SanitizePath(shellEscaped) = %q; want %q", got, unicodeFile)
	}

	// 4. Tilde expansion
	home, err := os.UserHomeDir()
	if err == nil {
		want := filepath.Join(home, "Downloads", "test.mkv")
		if got := SanitizePath("~/Downloads/test.mkv"); got != want {
			t.Errorf("SanitizePath(~/...) = %q; want %q", got, want)
		}
	}
}
