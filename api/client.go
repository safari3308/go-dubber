package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/safari3308/go-dubber/config"
	"github.com/safari3308/go-dubber/utils"
)

type TTSRequest struct {
	Text  string  `json:"text"`
	Lang  string  `json:"lang"`
	Role  string  `json:"role"`
	Speed float64 `json:"speed,omitempty"`
}

// RenderSingleLineTTS sends a single dialogue text line to the TTS server
func RenderSingleLineTTS(cfg *config.Config, text, lang, role string) ([]byte, error) {
	speed := cfg.TTSSpeed
	if speed <= 0 {
		speed = 1.1
	}

	normLang := strings.ToLower(strings.TrimSpace(lang))

	switch normLang {
	case "vi", "vie", "vn", "vietnamese":
		text = utils.NormalizeVietnameseTextReplaceNumbers(text)
	}

	payload := TTSRequest{Text: text, Lang: lang, Role: role, Speed: speed}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/audio/speech", cfg.ApiUrl)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(respBody))
	}

	return io.ReadAll(resp.Body)
}

// SyncSubtitleWithServer posts subtitle and reference audio track to Subsync API endpoint
func SyncSubtitleWithServer(cfg *config.Config, targetSubPath, refAudioPath, outputSubPath string) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 1. Target subtitle stream payload
	subFile, err := os.Open(targetSubPath)
	if err != nil {
		return err
	}
	defer subFile.Close()
	partSub, err := writer.CreateFormFile("target_sub", filepath.Base(targetSubPath))
	if err != nil {
		return err
	}
	_, _ = io.Copy(partSub, subFile)

	// 2. Reference audio stream payload
	audioFile, err := os.Open(refAudioPath)
	if err != nil {
		return err
	}
	defer audioFile.Close()
	partAudio, err := writer.CreateFormFile("reference_file", filepath.Base(refAudioPath))
	if err != nil {
		return err
	}
	_, _ = io.Copy(partAudio, audioFile)

	// 3. Set reference type
	_ = writer.WriteField("ref_type", "audio")
	_ = writer.Close()

	url := fmt.Sprintf("%s/v1/utils/subsync", cfg.ApiUrl)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error %d: %s", resp.StatusCode, string(respBody))
	}

	// Write synchronized SRT payload to destination path
	out, err := os.Create(outputSubPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// RenderTTSFromServer sends full SRT file to TTS engine server
func RenderTTSFromServer(cfg *config.Config, subPath, outputWavPath, lang string) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	subFile, err := os.Open(subPath)
	if err != nil {
		return err
	}
	defer subFile.Close()

	part, err := writer.CreateFormFile("file", filepath.Base(subPath))
	if err != nil {
		return err
	}
	_, _ = io.Copy(part, subFile)

	_ = writer.WriteField("lang", lang)
	_ = writer.WriteField("role", "A")
	speed := cfg.TTSSpeed
	if speed <= 0 {
		speed = 1.1
	}
	_ = writer.WriteField("speed", fmt.Sprintf("%.2f", speed))

	_ = writer.Close()

	url := fmt.Sprintf("%s/v1/audio/speech", cfg.ApiUrl)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("TTS Server error %d: %s", resp.StatusCode, string(respBody))
	}

	out, err := os.Create(outputWavPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// CheckServerHealth kiểm tra nhanh server TTS có khả dụng không
func CheckServerHealth(cfg *config.Config) error {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", cfg.ApiUrl+"/health", nil) // Hoặc endpoint ping bất kỳ
	if err != nil {
		return err
	}
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("không thể kết nối tới TTS server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TTS server trả về status code: %d", resp.StatusCode)
	}
	return nil
}
