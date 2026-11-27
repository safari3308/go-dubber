package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/textutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const DefaultSampleRate = 24000

// ServerInfo describes the active TTS engine, model, and audio sample-rate configuration.
type ServerInfo struct {
	Engine           string            `json:"engine,omitempty"`
	Model            string            `json:"model,omitempty"`
	SampleRate       int               `json:"sample_rate"`
	NativeSampleRate int               `json:"native_sample_rate,omitempty"`
	Voices           map[string]string `json:"voices,omitempty"`
}

// VoiceInfo describes a single synthesis voice provided by the TTS backend.
type VoiceInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Languages   []string `json:"languages"`
	Gender      string   `json:"gender,omitempty"`
	Description string   `json:"description,omitempty"`
}

// VoicesResponse is returned by ListVoices and GET /v1/voices.
type VoicesResponse struct {
	Engine        string            `json:"engine,omitempty"`
	Voices        []VoiceInfo       `json:"voices"`
	DefaultVoices map[string]string `json:"default_voices,omitempty"`
}

type TTSRequest struct {
	Text  string  `json:"text"`
	Lang  string  `json:"lang"`
	Voice string  `json:"voice,omitempty"`
	Role  string  `json:"role,omitempty"`
	Speed float64 `json:"speed,omitempty"`
}

func isLegacyRoleAlias(v string) bool {
	u := strings.ToUpper(strings.TrimSpace(v))
	return u == "A" || u == "B"
}

// RenderSingleLineTTS sends a single dialogue text line to the TTS server.
func RenderSingleLineTTS(cfg *config.Config, text, lang, voice string) ([]byte, error) {
	return RenderSingleLineTTSContext(context.Background(), cfg, text, lang, voice)
}

// RenderSingleLineTTSContext sends a single dialogue text line to the TTS server with context cancellation.
// It prefers gRPC when available and automatically falls back to HTTP if gRPC is unreachable.
// When voice is empty, cfg.VoiceForLanguage(lang) is used.
func RenderSingleLineTTSContext(ctx context.Context, cfg *config.Config, text, lang, voice string) ([]byte, error) {
	speed := cfg.TTSSpeed
	if speed <= 0 {
		speed = 1.1
	}

	resolvedVoice := strings.TrimSpace(voice)
	if resolvedVoice == "" {
		resolvedVoice = cfg.VoiceForLanguage(lang)
	}

	normLang := strings.ToLower(strings.TrimSpace(lang))
	switch normLang {
	case "vi", "vie", "vn", "vietnamese":
		text = textutil.NormalizeVietnameseTextReplaceNumbers(text)
	}

	if grpcAddr := ResolveGrpcAddr(cfg); grpcAddr != "" && !isGRPCUnavailable(grpcAddr) {
		wav, err := renderSingleLineTTSGRPC(ctx, grpcAddr, cfg.ApiToken, text, lang, resolvedVoice, speed)
		if err == nil {
			return wav, nil
		}
		if !shouldFallbackToHTTP(ctx, err) || strings.TrimSpace(cfg.ApiUrl) == "" {
			return nil, err
		}
		markGRPCUnavailable(grpcAddr)
	}

	return renderSingleLineTTSHTTP(ctx, cfg, text, lang, resolvedVoice, speed)
}

func renderSingleLineTTSHTTP(ctx context.Context, cfg *config.Config, text, lang, voice string, speed float64) ([]byte, error) {
	payload := TTSRequest{Text: text, Lang: lang, Speed: speed}
	if voice != "" {
		if isLegacyRoleAlias(voice) {
			payload.Role = strings.ToUpper(voice)
		} else {
			payload.Voice = voice
		}
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/audio/speech", strings.TrimRight(cfg.ApiUrl, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

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

// SyncSubtitleWithServer posts subtitle and reference audio/subtitle track to Subsync API endpoint.
func SyncSubtitleWithServer(cfg *config.Config, targetSubPath, refAudioPath, outputSubPath string) error {
	return SyncSubtitleWithServerContext(context.Background(), cfg, targetSubPath, refAudioPath, outputSubPath)
}

// SyncSubtitleWithServerContext syncs a target subtitle against a reference audio or subtitle track.
// It prefers gRPC streaming when available and automatically falls back to HTTP multipart upload.
func SyncSubtitleWithServerContext(ctx context.Context, cfg *config.Config, targetSubPath, refAudioPath, outputSubPath string) error {
	protoRefType, httpRefType := detectRefType(refAudioPath)

	if grpcAddr := ResolveGrpcAddr(cfg); grpcAddr != "" && !isGRPCUnavailable(grpcAddr) {
		err := syncSubtitleGRPC(ctx, grpcAddr, cfg.ApiToken, targetSubPath, refAudioPath, outputSubPath, protoRefType)
		if err == nil {
			return nil
		}
		if !shouldFallbackToHTTP(ctx, err) || strings.TrimSpace(cfg.ApiUrl) == "" {
			return err
		}
		markGRPCUnavailable(grpcAddr)
	}

	return syncSubtitleHTTP(ctx, cfg, targetSubPath, refAudioPath, outputSubPath, httpRefType)
}

func syncSubtitleHTTP(ctx context.Context, cfg *config.Config, targetSubPath, refAudioPath, outputSubPath, refType string) error {
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

	// 2. Reference audio/subtitle stream payload
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
	_ = writer.WriteField("ref_type", refType)
	_ = writer.Close()

	url := fmt.Sprintf("%s/v1/utils/subsync", strings.TrimRight(cfg.ApiUrl, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

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

// RenderTTSFromServer sends full SRT file to TTS engine server.
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

// CheckServerHealth checks if the TTS server is reachable and healthy.
func CheckServerHealth(cfg *config.Config) error {
	return CheckServerHealthContext(context.Background(), cfg)
}

// CheckServerHealthContext checks if the TTS server is reachable and healthy via gRPC or HTTP.
func CheckServerHealthContext(ctx context.Context, cfg *config.Config) error {
	_, _, err := CheckServerHealthWithTransportContext(ctx, cfg)
	return err
}

// CheckServerHealthWithTransportContext probes gRPC first (if enabled/derivable) and falls back to HTTP.
// Returns the active transport ("grpc" or "http"), the endpoint checked, and any error if both failed.
func CheckServerHealthWithTransportContext(ctx context.Context, cfg *config.Config) (string, string, error) {
	var grpcErr error
	grpcAddr := ResolveGrpcAddr(cfg)
	if grpcAddr != "" {
		if err := CheckGRPCHealthContext(ctx, grpcAddr); err == nil {
			return "grpc", grpcAddr, nil
		} else {
			grpcErr = err
			markGRPCUnavailable(grpcAddr)
		}
	}

	if strings.TrimSpace(cfg.ApiUrl) != "" {
		if err := checkHTTPHealthContext(ctx, cfg); err == nil {
			return "http", cfg.ApiUrl, nil
		} else {
			return "", cfg.ApiUrl, err
		}
	}

	if grpcErr != nil {
		return "", grpcAddr, fmt.Errorf("cannot connect to TTS gRPC server (%s): %w", grpcAddr, grpcErr)
	}
	return "", "", fmt.Errorf("no TTS server URL configured")
}

func checkHTTPHealthContext(ctx context.Context, cfg *config.Config) error {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.ApiUrl, "/")+"/health", nil)
	if err != nil {
		return err
	}
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to TTS server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TTS server returned status code: %d", resp.StatusCode)
	}
	return nil
}

// GetServerInfo queries the TTS server for engine/model metadata and active sample rate.
// It tries gRPC GetInfo first and falls back to HTTP GET /v1/info.
// Older servers returning Unimplemented or 404 gracefully return SampleRate=24000 with empty Engine/Model.
func GetServerInfo(ctx context.Context, cfg *config.Config) (*ServerInfo, error) {
	if cfg == nil {
		return &ServerInfo{SampleRate: DefaultSampleRate}, nil
	}

	grpcUnimplemented := false
	if grpcAddr := ResolveGrpcAddr(cfg); grpcAddr != "" && !isGRPCUnavailable(grpcAddr) {
		info, err := getServerInfoGRPC(ctx, grpcAddr)
		if err == nil {
			return info, nil
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			grpcUnimplemented = true
		} else if !shouldFallbackToHTTP(ctx, err) {
			return nil, err
		}
	}

	if strings.TrimSpace(cfg.ApiUrl) != "" {
		info, statusCode, err := getServerInfoHTTP(ctx, cfg)
		if err == nil {
			return info, nil
		}
		// Older HTTP-only server without /v1/info -> graceful default.
		if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented {
			return &ServerInfo{SampleRate: DefaultSampleRate}, nil
		}
		if grpcUnimplemented {
			return &ServerInfo{SampleRate: DefaultSampleRate}, nil
		}
		return nil, err
	}

	if grpcUnimplemented {
		return &ServerInfo{SampleRate: DefaultSampleRate}, nil
	}
	return &ServerInfo{SampleRate: DefaultSampleRate}, nil
}

func getServerInfoHTTP(ctx context.Context, cfg *config.Config) (*ServerInfo, int, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.ApiUrl, "/")+"/v1/info", nil)
	if err != nil {
		return nil, 0, err
	}
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot query TTS server info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("TTS /v1/info returned status code: %d", resp.StatusCode)
	}

	var info ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to decode /v1/info response: %w", err)
	}
	if info.SampleRate <= 0 {
		info.SampleRate = DefaultSampleRate
	}
	if info.NativeSampleRate <= 0 {
		info.NativeSampleRate = info.SampleRate
	}
	return &info, resp.StatusCode, nil
}

// ListVoices queries the TTS server for its available voices (optionally filtered by lang)
// and per-language defaults. It tries gRPC ListVoices first, then HTTP GET /v1/voices,
// and gracefully falls back to GetServerInfo for older servers.
func ListVoices(ctx context.Context, cfg *config.Config, lang string) (*VoicesResponse, error) {
	if cfg == nil {
		return &VoicesResponse{Voices: []VoiceInfo{}, DefaultVoices: map[string]string{}}, nil
	}

	normLang := strings.ToLower(strings.TrimSpace(lang))
	grpcUnimplemented := false

	if grpcAddr := ResolveGrpcAddr(cfg); grpcAddr != "" && !isGRPCUnavailable(grpcAddr) {
		resp, err := listVoicesGRPC(ctx, grpcAddr, normLang)
		if err == nil {
			return resp, nil
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			grpcUnimplemented = true
		} else if !shouldFallbackToHTTP(ctx, err) {
			return nil, err
		}
	}

	if strings.TrimSpace(cfg.ApiUrl) != "" {
		resp, statusCode, err := listVoicesHTTP(ctx, cfg, normLang)
		if err == nil {
			return resp, nil
		}
		if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed || statusCode == http.StatusNotImplemented {
			return fallbackVoicesFromInfo(ctx, cfg, normLang), nil
		}
		if grpcUnimplemented {
			return fallbackVoicesFromInfo(ctx, cfg, normLang), nil
		}
		return nil, err
	}

	if grpcUnimplemented {
		return fallbackVoicesFromInfo(ctx, cfg, normLang), nil
	}
	return &VoicesResponse{Voices: []VoiceInfo{}, DefaultVoices: map[string]string{}}, nil
}

func listVoicesHTTP(ctx context.Context, cfg *config.Config, lang string) (*VoicesResponse, int, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := strings.TrimRight(cfg.ApiUrl, "/") + "/v1/voices"
	if lang != "" {
		endpoint += "?lang=" + lang
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if cfg.ApiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.ApiToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot query TTS voices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("TTS /v1/voices returned status code: %d", resp.StatusCode)
	}

	var out VoicesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to decode /v1/voices response: %w", err)
	}
	if out.Voices == nil {
		out.Voices = []VoiceInfo{}
	}
	if out.DefaultVoices == nil {
		out.DefaultVoices = map[string]string{}
	}
	return &out, resp.StatusCode, nil
}

func fallbackVoicesFromInfo(ctx context.Context, cfg *config.Config, lang string) *VoicesResponse {
	out := &VoicesResponse{
		Voices:        []VoiceInfo{},
		DefaultVoices: map[string]string{},
	}
	info, err := GetServerInfo(ctx, cfg)
	if err != nil || info == nil {
		return out
	}
	out.Engine = info.Engine
	seen := map[string]*VoiceInfo{}
	add := func(id, voiceLang string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if existing, ok := seen[id]; ok {
			for _, l := range existing.Languages {
				if l == voiceLang {
					return
				}
			}
			existing.Languages = append(existing.Languages, voiceLang)
			return
		}
		v := &VoiceInfo{ID: id, Name: id, Languages: []string{voiceLang}}
		seen[id] = v
	}
	if v := info.Voices["vi_a"]; v != "" {
		out.DefaultVoices["vi"] = v
		add(v, "vi")
	}
	if v := info.Voices["vi_b"]; v != "" {
		add(v, "vi")
	}
	if v := info.Voices["en_a"]; v != "" {
		out.DefaultVoices["en"] = v
		add(v, "en")
	}
	if v := info.Voices["en_b"]; v != "" {
		add(v, "en")
	}
	for _, v := range seen {
		if lang != "" {
			matched := false
			for _, l := range v.Languages {
				if l == lang {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out.Voices = append(out.Voices, *v)
	}
	return out
}
