package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TruncateString truncates a string for clean progress display
func TruncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// FileExists checks whether a file exists
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// CopyFile copies a file from src to dst using efficient stream buffer
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// CopyAndRemove copies data and removes temporary file across drives
func CopyAndRemove(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	err = os.WriteFile(dst, input, 0644)
	if err != nil {
		return err
	}
	return os.Remove(src)
}

// CleanTempFiles safely removes a slice of temporary file paths
func CleanTempFiles(paths ...string) {
	for _, p := range paths {
		if p != "" && FileExists(p) {
			_ = os.Remove(p)
		}
	}
}

// FormatBytes formats byte sizes into human readable units (KB, MB, GB)
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats duration into human readable minutes / seconds
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// SafeReplaceOnNAS safely copies local asset onto NAS with atomic swap and data integrity checks
func SafeReplaceOnNAS(srcLocal, dstNAS string) error {
	// 1. Validate local temp file
	srcInfo, err := os.Stat(srcLocal)
	if err != nil {
		return fmt.Errorf("local temp file does not exist: %w", err)
	}

	dstDir := filepath.Dir(dstNAS)
	baseName := filepath.Base(dstNAS)
	nasTemp := filepath.Join(dstDir, ".upload_"+baseName)
	nasBak := filepath.Join(dstDir, ".bak_"+baseName)

	// Always cleanup temporary upload files on exit
	defer func() {
		_ = os.Remove(nasTemp)
		_ = os.Remove(nasBak)
	}()

	// 2. LAYER 1: Chunked copy from Local SSD to temp .upload_ file on NAS
	in, err := os.Open(srcLocal)
	if err != nil {
		return fmt.Errorf("cannot open local file: %w", err)
	}
	defer in.Close()

	out, err := os.Create(nasTemp)
	if err != nil {
		return fmt.Errorf("cannot create temp file on NAS (check write permissions): %w", err)
	}

	buf := make([]byte, 2*1024*1024) // 2MB buffer
	_, err = io.CopyBuffer(out, in, buf)
	_ = out.Sync()
	out.Close()

	if err != nil {
		return fmt.Errorf("network disconnect during NAS copy: %w", err)
	}

	// 3. LAYER 2: Data integrity check
	nasInfo, err := os.Stat(nasTemp)
	if err != nil || nasInfo.Size() != srcInfo.Size() {
		return fmt.Errorf("NAS file size mismatch with Local (Local: %d, NAS: %d). Aborting operation", srcInfo.Size(), nasInfo.Size())
	}

	// 4. LAYER 3: Atomic file swap
	hasBak := false
	if FileExists(dstNAS) {
		if err := os.Rename(dstNAS, nasBak); err != nil {
			return fmt.Errorf("cannot backup original NAS file: %w", err)
		}
		hasBak = true
	}

	// Rename temp file to official asset path
	if err := os.Rename(nasTemp, dstNAS); err != nil {
		if hasBak {
			_ = os.Rename(nasBak, dstNAS)
		}
		return fmt.Errorf("cannot overwrite target file on NAS: %w", err)
	}

	if hasBak {
		_ = os.Remove(nasBak)
	}

	return nil
}

var ones = []string{"không", "một", "hai", "ba", "bốn", "năm", "sáu", "bảy", "tám", "chín"}

// NumberToVietnameseWords convert int64 to Vietnamese words
func NumberToVietnameseWords(n int64) string {
	if n == 0 {
		return "không"
	}
	if n < 0 {
		return "âm " + NumberToVietnameseWords(-n)
	}
	return readThreeDigitsGroup(n, false)
}

func readThreeDigitsGroup(n int64, hasHigherGroup bool) string {
	if n == 0 {
		return ""
	}

	// Billions (>= 1,000,000,000)
	if n >= 1000000000 {
		ty := n / 1000000000
		rem := n % 1000000000
		res := readThreeDigitsGroup(ty, hasHigherGroup) + " tỷ"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Millions (>= 1,000,000)
	if n >= 1000000 {
		trieu := n / 1000000
		rem := n % 1000000
		res := readThreeDigitsGroup(trieu, hasHigherGroup) + " triệu"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Thousands (>= 1.000)
	if n >= 1000 {
		nghin := n / 1000
		rem := n % 1000
		res := readThreeDigitsGroup(nghin, hasHigherGroup) + " nghìn"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Hundreds, Tens, Units (< 1000)
	hundred := n / 100
	rem := n % 100

	var parts []string

	if hundred > 0 || hasHigherGroup {
		parts = append(parts, ones[hundred]+" trăm")
	}

	if rem > 0 {
		ten := rem / 10
		unit := rem % 10

		if ten > 1 {
			parts = append(parts, ones[ten]+" mươi")
			if unit == 1 {
				parts = append(parts, "mốt")
			} else if unit == 5 {
				parts = append(parts, "lăm")
			} else if unit > 0 {
				parts = append(parts, ones[unit])
			}
		} else if ten == 1 {
			parts = append(parts, "mười")
			if unit == 5 {
				parts = append(parts, "lăm")
			} else if unit > 0 {
				parts = append(parts, ones[unit])
			}
		} else { // ten == 0
			if hundred > 0 || hasHigherGroup {
				parts = append(parts, "lẻ")
			}
			parts = append(parts, ones[unit])
		}
	}

	return strings.Join(parts, " ")
}

// DigitsToVietnameseWords read each digit (for code/episode 01)
func DigitsToVietnameseWords(digits string) string {
	var words []string
	for _, ch := range digits {
		if ch >= '0' && ch <= '9' {
			words = append(words, ones[ch-'0'])
		}
	}
	return strings.Join(words, " ")
}

// NormalizeVietnameseTextReplaceNumbers scan and replace numbers intelligently
func NormalizeVietnameseTextReplaceNumbers(text string) string {
	// Remove thousands separators 
	reFormattedNum := regexp.MustCompile(`\b(\d{1,3})[.,](\d{3})\b`)
	text = reFormattedNum.ReplaceAllString(text, "$1$2")

	// 1. Alphanumeric (H10, S01E02, 1080p)
	reAlphanumeric := regexp.MustCompile(`(?i)\b([a-z]+)(\d+)\b|\b(\d+)([a-z]+)\b`)
	text = reAlphanumeric.ReplaceAllStringFunc(text, func(match string) string {
		reDigit := regexp.MustCompile(`\d+`)
		return reDigit.ReplaceAllStringFunc(match, func(d string) string {
			return " " + DigitsToVietnameseWords(d) + " "
		})
	})

	// 2. Convert remaining numbers
	reNum := regexp.MustCompile(`\d+`)
	return reNum.ReplaceAllStringFunc(text, func(match string) string {
		// Leading zeros (01, 007) -> Read digit by digit
		if len(match) > 1 && strings.HasPrefix(match, "0") {
			return DigitsToVietnameseWords(match)
		}
		// Too long number (> 10 digits) -> Read digit by digit
		if len(match) > 10 {
			return DigitsToVietnameseWords(match)
		}

		num, err := strconv.ParseInt(match, 10, 64)
		if err != nil {
			return DigitsToVietnameseWords(match)
		}
		return NumberToVietnameseWords(num)
	})
}
