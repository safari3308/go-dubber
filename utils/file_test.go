package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"Short string", "hello", 10, "hello"},
		{"Exact length string", "hello", 5, "hello"},
		{"Long string", "hello world", 5, "hello..."},
		{"Unicode runes", "xin chào thế giới", 8, "xin chào..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateString(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("TruncateString(%q, %d) = %q; want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.txt")

	if FileExists(filePath) {
		t.Errorf("FileExists(%q) = true; want false for non-existent file", filePath)
	}

	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if !FileExists(filePath) {
		t.Errorf("FileExists(%q) = false; want true for existing file", filePath)
	}

	if FileExists(tempDir) {
		t.Errorf("FileExists(%q) = true; want false for directory", tempDir)
	}
}

func TestCopyFile(t *testing.T) {
	tempDir := t.TempDir()
	srcPath := filepath.Join(tempDir, "src.txt")
	dstPath := filepath.Join(tempDir, "dst.txt")
	data := []byte("hello copy file")

	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("Failed to write src file: %v", err)
	}

	if err := CopyFile(srcPath, dstPath); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	copiedData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read dst file: %v", err)
	}

	if string(copiedData) != string(data) {
		t.Errorf("Copied content = %q; want %q", string(copiedData), string(data))
	}
}

func TestCopyAndRemove(t *testing.T) {
	tempDir := t.TempDir()
	srcPath := filepath.Join(tempDir, "src_rem.txt")
	dstPath := filepath.Join(tempDir, "dst_rem.txt")
	data := []byte("hello copy and remove")

	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("Failed to write src file: %v", err)
	}

	if err := CopyAndRemove(srcPath, dstPath); err != nil {
		t.Fatalf("CopyAndRemove failed: %v", err)
	}

	if FileExists(srcPath) {
		t.Errorf("Source file %q still exists after CopyAndRemove", srcPath)
	}

	copiedData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read dst file: %v", err)
	}

	if string(copiedData) != string(data) {
		t.Errorf("Copied content = %q; want %q", string(copiedData), string(data))
	}
}

func TestCleanTempFiles(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "temp1.txt")
	f2 := filepath.Join(tempDir, "temp2.txt")
	f3 := filepath.Join(tempDir, "non_existent.txt")

	_ = os.WriteFile(f1, []byte("1"), 0644)
	_ = os.WriteFile(f2, []byte("2"), 0644)

	CleanTempFiles(f1, f2, f3, "")

	if FileExists(f1) || FileExists(f2) {
		t.Errorf("CleanTempFiles failed to remove temp files")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1572864, "1.50 MB"},
		{1073741824, "1.00 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("FormatBytes(%d) = %q; want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{120 * time.Second, "2m 0s"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("FormatDuration(%v) = %q; want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestSafeReplaceOnNAS(t *testing.T) {
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "local.txt")
	nasFile := filepath.Join(tempDir, "nas_target.txt")

	localData := []byte("new nas file data")
	if err := os.WriteFile(localFile, localData, 0644); err != nil {
		t.Fatalf("Failed to write local file: %v", err)
	}

	// 1. Initial replacement when nas target file does not exist
	if err := SafeReplaceOnNAS(localFile, nasFile); err != nil {
		t.Fatalf("SafeReplaceOnNAS failed: %v", err)
	}

	gotData, err := os.ReadFile(nasFile)
	if err != nil || string(gotData) != string(localData) {
		t.Errorf("NAS file content = %q; want %q", string(gotData), string(localData))
	}

	// 2. Replacement when nas target file already exists
	updatedLocalData := []byte("updated nas file content")
	if err := os.WriteFile(localFile, updatedLocalData, 0644); err != nil {
		t.Fatalf("Failed to update local file: %v", err)
	}

	if err := SafeReplaceOnNAS(localFile, nasFile); err != nil {
		t.Fatalf("SafeReplaceOnNAS update failed: %v", err)
	}

	gotDataUpdated, err := os.ReadFile(nasFile)
	if err != nil || string(gotDataUpdated) != string(updatedLocalData) {
		t.Errorf("Updated NAS file content = %q; want %q", string(gotDataUpdated), string(updatedLocalData))
	}
}

func TestNumberToVietnameseWords(t *testing.T) {
	tests := []struct {
		num      int64
		expected string
	}{
		{0, "không"},
		{-5, "âm năm"},
		{7, "bảy"},
		{10, "mười"},
		{15, "mười lăm"},
		{21, "hai mươi mốt"},
		{25, "hai mươi lăm"},
		{100, "một trăm"},
		{105, "một trăm lẻ năm"},
		{115, "một trăm mười lăm"},
		{1000, "một nghìn"},
		{1005, "một nghìn không trăm lẻ năm"},
		{1000000, "một triệu"},
		{1000000000, "một tỷ"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := NumberToVietnameseWords(tt.num)
			if result != tt.expected {
				t.Errorf("NumberToVietnameseWords(%d) = %q; want %q", tt.num, result, tt.expected)
			}
		})
	}
}

func TestDigitsToVietnameseWords(t *testing.T) {
	result := DigitsToVietnameseWords("0123")
	expected := "không một hai ba"
	if result != expected {
		t.Errorf("DigitsToVietnameseWords(\"0123\") = %q; want %q", result, expected)
	}
}

func TestNormalizeVietnameseTextReplaceNumbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Formatted number", "Giá 1.000 đồng", "Giá một nghìn đồng"},
		{"Alphanumeric code", "Tập S01E02", "Tập Skhông mộtEkhông hai"},
		{"Number with leading zero", "Mã 01", "Mã không một"},
		{"Regular counting number", "Có 15 cái", "Có mười lăm cái"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeVietnameseTextReplaceNumbers(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeVietnameseTextReplaceNumbers(%q) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}
