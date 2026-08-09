package media

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/safari3308/go-dubber/api"
	"github.com/safari3308/go-dubber/config"
	"github.com/safari3308/go-dubber/utils"
)

type SubEntry struct {
	Index    int
	StartSec float64
	EndSec   float64
	Text     string
}

// Blacklist for Game/Anime UI stat tables (ignored by AI reader)
var statBlacklist = []string{
	"name:", "sex:", "level:", "hp:", "mp:", "strength:",
	"stamina:", "intelligence:", "spirit:", "speed:", "dexterity:",
	"fire:", "water:", "wind:", "earth:", "light:", "dark:",
}

// collapseRepeatedChars shorten repeated characters that appear more than 2 times in a row (wwwwwhattttttt -> what, nooooo -> no)
// Keep valid 2-letter repeated words (look, speed, apple...)
func collapseRepeatedChars(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}

	var result []rune
	i := 0
	for i < len(runes) {
		j := i
		// Count the number of identical characters in a row (case-insensitive)
		for j < len(runes) && unicode.ToLower(runes[j]) == unicode.ToLower(runes[i]) {
			j++
		}

		runLength := j - i
		if runLength >= 3 {
			// If repeated 3 times or more -> Shorten to 1 character
			result = append(result, runes[i])
		} else {
			// If repeated 1 or 2 times -> Keep original
			result = append(result, runes[i:j]...)
		}
		i = j
	}
	return string(result)
}

// CleanDialogueLine cleans dialogue text lines for TTS synthesis
func CleanDialogueLine(textLine string, lang string) string {
	// 1. Remove ASS/SSA {...} and HTML <...>
	reASS := regexp.MustCompile(`\{[^}]+\}`)
	reHTML := regexp.MustCompile(`<[^>]+>`)
	clean := reASS.ReplaceAllString(textLine, "")
	clean = reHTML.ReplaceAllString(clean, "")

	// 💡 Giữ lại nội dung bên trong (), [], chỉ bỏ dấu ngoặc (vd: "(Tập 53)" -> "Tập 53")
	clean = strings.ReplaceAll(clean, "(", " ")
	clean = strings.ReplaceAll(clean, ")", " ")
	clean = strings.ReplaceAll(clean, "[", " ")
	clean = strings.ReplaceAll(clean, "]", " ")

	// 2. Remove speaker name prefixes (e.g. "JOHN: Hello", "ANNOUNCER 1: ")
	rePrefix := regexp.MustCompile(`^[A-ZÁÀẢÃẠÉÈẺẼẸÓÒỎÕỌÚÙỦŨỤỨỪỬỮỰÍÌỈĨỊÝỲỶỸỴĐ\s\d]+:\s*`)
	clean = rePrefix.ReplaceAllString(clean, "")

	// 3. Remove music symbols and slashes
	clean = strings.ReplaceAll(clean, "\\", "")
	clean = strings.ReplaceAll(clean, "♪", "")
	clean = strings.ReplaceAll(clean, "♫", "")

	// 4. Handle stuttering (b-but -> but, wh-what -> what)
	reStutter := regexp.MustCompile(`(?i)\b([a-zA-ZđĐ]+)-([a-zA-ZđĐ]+)`)
	oldClean := ""
	for oldClean != clean {
		oldClean = clean
		clean = reStutter.ReplaceAllStringFunc(clean, func(m string) string {
			parts := strings.Split(m, "-")
			if len(parts) != 2 {
				return m
			}
			p0, p1 := parts[0], parts[1]
			p0Lower, p1Lower := strings.ToLower(p0), strings.ToLower(p1)
			if p0Lower == p1Lower {
				return p0
			}
			if strings.HasPrefix(p1Lower, p0Lower) {
				if len(p0) > 0 && unicode.IsUpper([]rune(p0)[0]) {
					runes := []rune(p1)
					runes[0] = unicode.ToUpper(runes[0])
					return string(runes)
				}
				return p1
			}
			return m
		})
	}

	// 🌟 4.5. Process collapse repeated characters
	clean = collapseRepeatedChars(clean)

	// 🌟 4.6. Chuyển đổi số thành chữ tiếng Việt (Được gọi sau khi đã lọc HTML sạch sẽ)
	normLang := strings.ToLower(strings.TrimSpace(lang))
	if normLang == "vi" || normLang == "vie" || normLang == "vn" || normLang == "vietnamese" {
		clean = utils.NormalizeVietnameseTextReplaceNumbers(clean)
	}

	// 5. Normalize whitespace
	reSpace := regexp.MustCompile(`\s+`)
	clean = strings.TrimSpace(reSpace.ReplaceAllString(clean, " "))
	if clean == "" {
		return ""
	}

	// 6. Handle ALL-CAPS words
	words := strings.Fields(clean)
	var fixedWords []string
	reAlphaNum := regexp.MustCompile(`[^\w\s]`)
	for _, w := range words {
		core := reAlphaNum.ReplaceAllString(w, "")
		if len(core) > 1 && isAllUpper(core) {
			fixedWords = append(fixedWords, strings.ToLower(w))
		} else {
			fixedWords = append(fixedWords, w)
		}
	}
	clean = strings.Join(fixedWords, " ")

	// 7. Capitalize first letter
	if len(clean) > 0 {
		runes := []rune(clean)
		runes[0] = unicode.ToUpper(runes[0])
		clean = string(runes)
	}

	// Bên trong CleanDialogueLine
	clean = StripFontTags(clean)

	return clean
}

func isAllUpper(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func isStatLine(line string) bool {
	lower := strings.ToLower(line)
	for _, stat := range statBlacklist {
		if strings.Contains(lower, stat) {
			return true
		}
	}
	return false
}

// ParseSRT parses and cleans SRT subtitles
func ParseSRT(srtPath string, lang string) ([]SubEntry, error) {
	content, err := os.ReadFile(srtPath)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(`(?m)^(\d+)\r?\n(\d{2}:\d{2}:\d{2}[,\.]\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}[,\.]\d{3})\r?\n([\s\S]*?)(?:\r?\n\r?\n|\z)`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	var entries []SubEntry
	for _, m := range matches {
		idx, _ := strconv.Atoi(m[1])
		startSec := srtTimeToSeconds(m[2])
		endSec := srtTimeToSeconds(m[3])

		rawLines := strings.Split(m[4], "\n")
		var cleanLines []string
		for _, l := range rawLines {
			l = strings.TrimSpace(l)
			if l == "" || isStatLine(l) {
				continue
			}
			cleaned := CleanDialogueLine(l, lang)
			if cleaned != "" {
				cleanLines = append(cleanLines, cleaned)
			}
		}

		if len(cleanLines) == 0 {
			continue
		}

		finalText := strings.Join(cleanLines, " ")
		hasAlnum := false
		for _, r := range finalText {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				hasAlnum = true
				break
			}
		}

		if hasAlnum {
			entries = append(entries, SubEntry{
				Index:    idx,
				StartSec: startSec,
				EndSec:   endSec,
				Text:     finalText,
			})
		}
	}
	return entries, nil
}
func StripFontTags(text string) string {
    // 1. Xóa thẻ <font ...> và </font> (không phân biệt hoa thường)
    reFont := regexp.MustCompile(`(?i)</?font[^>]*>`)
    text = reFont.ReplaceAllString(text, "")

    // 2. Xóa các thuộc tính size/fs nếu còn sót lại dạng ASS override {\fs20}
    reFS := regexp.MustCompile(`(?i)\{\\fs\d+\}`)
    text = reFS.ReplaceAllString(text, "")

    return text
}

func srtTimeToSeconds(timeStr string) float64 {
	timeStr = strings.ReplaceAll(timeStr, ",", ".")
	parts := strings.Split(timeStr, ":")
	if len(parts) < 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(parts[0], 64)
	m, _ := strconv.ParseFloat(parts[1], 64)
	s, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + s
}

// ExtractPCMFromWAV tìm vị trí chunk 'data' chuẩn xác trong file WAV (xử lý mọi độ dài Header)
func ExtractPCMFromWAV(wavData []byte) []byte {
	dataIdx := bytes.Index(wavData, []byte("data"))
	if dataIdx == -1 || len(wavData) < dataIdx+8 {
		if len(wavData) > 44 {
			return wavData[44:]
		}
		return nil
	}
	// 'data' tag (4 bytes) + chunk size (4 bytes) -> Dữ liệu PCM nằm từ byte (dataIdx + 8)
	return wavData[dataIdx+8:]
}

// TrimSilencePCM16 trims leading/trailing silent PCM 16-bit samples safely
func TrimSilencePCM16(pcm []byte, threshold int16) []byte {
	if len(pcm) < 4 {
		return pcm
	}
	// Ép số lượng byte phải chia hết cho 2 để chuẩn hóa 16-bit sample
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

// MixPCM16 mixes PCM 16-bit audio samples onto canvas buffer
func MixPCM16(canvas []byte, pcm []byte, startByte int) {
	// Đảm bảo startByte chia hết cho 2
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

// ProcessDubbingPipeline renders dialogue lines in parallel and places them on exact timeline
func ProcessDubbingPipeline(cfg *config.Config, srtPath, outWavPath, localTempDir, lang string, videoDuration float64, spinner *utils.Spinner) (int, error) {
	entries, err := ParseSRT(srtPath, lang)
	if err != nil || len(entries) == 0 {
		return 0, fmt.Errorf("failed to read SRT subtitle or empty file: %v", err)
	}

	// 🌟 LỚP BẢO VỆ 1: Lọc bỏ hoàn toàn các dòng sub nằm ngoài độ dài video (ví dụ: Sub Credit ở cuối)
    var validEntries []SubEntry
    for _, entry := range entries {
        if entry.StartSec < videoDuration {
            validEntries = append(validEntries, entry)
        }
    }
    entries = validEntries

	chunksDir := filepath.Join(localTempDir, "chunks_"+filepath.Base(outWavPath))
	_ = os.MkdirAll(chunksDir, 0755)

	totalEntries := len(entries)
	sampleRate := 24000
	bytesPerSample := 2
	numWorkers := cfg.Workers
	if numWorkers <= 0 {
		numWorkers = 2
	}

	type Job struct {
		Index int
		Entry SubEntry
	}

	jobs := make(chan Job, totalEntries)
	var completedCount int32
	var wg sync.WaitGroup

	if spinner != nil {
		spinner.UpdateMessage(fmt.Sprintf("🎙️ AI voice rendering [0/%d - 0.0%%] (%d workers)...", totalEntries, numWorkers))
	}

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk_%04d.wav", job.Index))
				if !utils.FileExists(chunkFile) {
					wavData, err := api.RenderSingleLineTTS(cfg, job.Entry.Text, lang, "A")
					if err != nil {
						wavData, err = api.RenderSingleLineTTS(cfg, job.Entry.Text, lang, "A")
					}
					if err == nil && len(wavData) > 0 {
						_ = os.WriteFile(chunkFile, wavData, 0644)
					}
				}
				current := atomic.AddInt32(&completedCount, 1)
				percent := float64(current) / float64(totalEntries) * 100
				if spinner != nil {
					spinner.UpdateMessage(fmt.Sprintf("🎙️ AI voice rendering [%d/%d - %.1f%%] (%d workers)...", current, totalEntries, percent, numWorkers))
				}
			}
		}()
	}

	for i, entry := range entries {
		jobs <- Job{Index: i, Entry: entry}
	}
	close(jobs)
	wg.Wait()

	if spinner != nil {
		spinner.UpdateMessage(fmt.Sprintf("📦 Packaging WAV payload (%d lines)...", totalEntries))
	}

	// 🌟 LỚP BẢO VỆ 2: Khóa kích thước Canvas vừa khít 100% với Video Duration
    // Không dùng entries[len-1].EndSec + 10 nữa!
    canvasBytes := make([]byte, int(videoDuration*float64(sampleRate))*bytesPerSample)

	for i, entry := range entries {
		chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk_%04d.wav", i))
		wavData, err := os.ReadFile(chunkFile)
		if err != nil || len(wavData) <= 44 {
			continue
		}

		// 🌟 Dùng ExtractPCMFromWAV thay cho wavData[44:] để lấy đúng PCM payload
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

		// Hybrid Adaptive Speedup:
		if audioDuration > targetMaxSec+0.1 {
			speedup := audioDuration / targetMaxSec
			if speedup > 1.5 {
				speedup = 1.5
			}
			if speedup >= 1.05 {
				if respeededWav, err := adjustChunkSpeed(chunkFile, speedup); err == nil {
					// 🌟 Bóc tách PCM từ file respeeded của FFmpeg bằng ExtractPCMFromWAV
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

		// 🌟 LỚP BẢO VỆ 3: Cắt gọn PCM nếu đoạn thoại AI kéo dài vượt quá thời lượng phim
        if endByte > len(canvasBytes) {
            pcmData = pcmData[:len(canvasBytes)-startByte]
        }

		MixPCM16(canvasBytes, pcmData, startByte)
	}

	header := createWAVHeader(len(canvasBytes), sampleRate, 1, 16)
	finalWav := append(header, canvasBytes...)

	err = os.WriteFile(outWavPath, finalWav, 0644)
	_ = os.RemoveAll(chunksDir)
	return totalEntries, err
}

func adjustChunkSpeed(inputWavPath string, speedup float64) ([]byte, error) {
	outWavPath := inputWavPath + ".speed.wav"
	defer os.Remove(outWavPath)

	filter := fmt.Sprintf("atempo=%.4f", speedup)
	cmd := exec.Command("ffmpeg", "-y", "-i", inputWavPath, "-filter:a", filter, outWavPath)
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
