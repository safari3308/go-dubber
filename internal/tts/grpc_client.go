package tts

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/tts/ttsv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultGRPCPort       = "50051"
	grpcMaxMessageBytes   = 100 << 20 // 100 MB (matches tts-service grpc_max_message_mb)
	grpcStreamChunkBytes  = 256 << 10 // 256 KB per streaming chunk
	grpcHealthTimeout     = 2500 * time.Millisecond
	grpcFallbackCooldown  = 30 * time.Second
)

var (
	grpcPoolMu sync.Mutex
	grpcConns  = make(map[string]*grpc.ClientConn)

	grpcFallbackMu    sync.RWMutex
	grpcUnavailableAt = make(map[string]time.Time)
)

// ResolveGrpcAddr determines the gRPC target address ("host:port") from cfg.
// If cfg.GrpcUrl is set, it is normalized (defaulting to port 50051 if omitted).
// Setting cfg.GrpcUrl to "off", "disabled", or "none" disables gRPC explicitly.
// If cfg.GrpcUrl is empty, the hostname from cfg.ApiUrl is combined with port 50051.
func ResolveGrpcAddr(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	raw := strings.TrimSpace(cfg.GrpcUrl)
	if raw != "" {
		lower := strings.ToLower(raw)
		if lower == "off" || lower == "disabled" || lower == "none" {
			return ""
		}
		return normalizeHostPort(raw, defaultGRPCPort)
	}

	apiURL := strings.TrimSpace(cfg.ApiUrl)
	if apiURL == "" {
		return ""
	}
	if !strings.Contains(apiURL, "://") {
		apiURL = "http://" + apiURL
	}
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, defaultGRPCPort)
}

func normalizeHostPort(raw, defaultPort string) string {
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			s = u.Host
		}
	}
	s = strings.TrimRight(s, "/")
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	// Strip brackets if someone passed bare IPv6 like "[::1]"
	s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	if s == "" {
		return ""
	}
	return net.JoinHostPort(s, defaultPort)
}

func getGRPCConn(addr string) (*grpc.ClientConn, error) {
	grpcPoolMu.Lock()
	defer grpcPoolMu.Unlock()

	if conn, ok := grpcConns[addr]; ok {
		return conn, nil
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(grpcMaxMessageBytes),
			grpc.MaxCallSendMsgSize(grpcMaxMessageBytes),
		),
	)
	if err != nil {
		return nil, err
	}
	grpcConns[addr] = conn
	return conn, nil
}

func isGRPCUnavailable(addr string) bool {
	grpcFallbackMu.RLock()
	until, ok := grpcUnavailableAt[addr]
	grpcFallbackMu.RUnlock()
	if !ok {
		return false
	}
	return time.Now().Before(until)
}

func markGRPCUnavailable(addr string) {
	grpcFallbackMu.Lock()
	grpcUnavailableAt[addr] = time.Now().Add(grpcFallbackCooldown)
	grpcFallbackMu.Unlock()
}

func clearGRPCUnavailable(addr string) {
	grpcFallbackMu.Lock()
	delete(grpcUnavailableAt, addr)
	grpcFallbackMu.Unlock()
}

// ResetGRPCFallbackCache clears all cached gRPC unavailability cooldowns.
func ResetGRPCFallbackCache() {
	grpcFallbackMu.Lock()
	grpcUnavailableAt = make(map[string]time.Time)
	grpcFallbackMu.Unlock()
}

func withGRPCAuth(ctx context.Context, apiToken string) context.Context {
	if apiToken == "" {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+apiToken))
}

// shouldFallbackToHTTP returns true when a gRPC error indicates the gRPC endpoint
// is unavailable or unimplemented, so the caller should transparently fall back to HTTP.
func shouldFallbackToHTTP(parentCtx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// Never fall back if the caller itself cancelled the request.
	if parentCtx.Err() != nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	switch st.Code() {
	case codes.Unavailable, codes.Unimplemented, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func mapProtoLanguage(lang string) ttsv1.Language {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en", "eng", "english":
		return ttsv1.Language_LANGUAGE_EN
	default:
		return ttsv1.Language_LANGUAGE_VI
	}
}

func mapProtoRole(role string) ttsv1.VoiceRole {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "B":
		return ttsv1.VoiceRole_VOICE_ROLE_B
	default:
		return ttsv1.VoiceRole_VOICE_ROLE_A
	}
}

func detectRefType(refPath string) (ttsv1.ReferenceType, string) {
	switch strings.ToLower(filepath.Ext(refPath)) {
	case ".srt", ".ass", ".ssa", ".vtt":
		return ttsv1.ReferenceType_REFERENCE_TYPE_SUB, "sub"
	default:
		return ttsv1.ReferenceType_REFERENCE_TYPE_AUDIO, "audio"
	}
}

// CheckGRPCHealthContext probes the standard grpc.health.v1.Health/Check endpoint.
func CheckGRPCHealthContext(ctx context.Context, grpcAddr string) error {
	if grpcAddr == "" {
		return fmt.Errorf("gRPC address not configured")
	}
	conn, err := getGRPCConn(grpcAddr)
	if err != nil {
		return err
	}

	probeCtx, cancel := context.WithTimeout(ctx, grpcHealthTimeout)
	defer cancel()

	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(probeCtx, &grpc_health_v1.HealthCheckRequest{Service: ""})
	if err != nil {
		return err
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("gRPC health status: %s", resp.GetStatus().String())
	}
	clearGRPCUnavailable(grpcAddr)
	return nil
}

func getServerInfoGRPC(ctx context.Context, grpcAddr string) (*ServerInfo, error) {
	if grpcAddr == "" {
		return nil, fmt.Errorf("gRPC address not configured")
	}
	conn, err := getGRPCConn(grpcAddr)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, grpcHealthTimeout)
	defer cancel()

	client := ttsv1.NewTTSServiceClient(conn)
	resp, err := client.GetInfo(callCtx, &ttsv1.GetInfoRequest{})
	if err != nil {
		return nil, err
	}

	sr := int(resp.GetSampleRate())
	if sr <= 0 {
		sr = DefaultSampleRate
	}
	nativeSR := int(resp.GetNativeSampleRate())
	if nativeSR <= 0 {
		nativeSR = sr
	}
	voices := make(map[string]string, len(resp.GetVoices()))
	for k, v := range resp.GetVoices() {
		voices[k] = v
	}

	return &ServerInfo{
		Engine:           resp.GetEngine(),
		Model:            resp.GetModel(),
		SampleRate:       sr,
		NativeSampleRate: nativeSR,
		Voices:           voices,
	}, nil
}

func listVoicesGRPC(ctx context.Context, grpcAddr string, lang string) (*VoicesResponse, error) {
	if grpcAddr == "" {
		return nil, fmt.Errorf("gRPC address not configured")
	}
	conn, err := getGRPCConn(grpcAddr)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, grpcHealthTimeout)
	defer cancel()

	var protoLang ttsv1.Language
	if lang != "" {
		protoLang = mapProtoLanguage(lang)
	}

	client := ttsv1.NewTTSServiceClient(conn)
	resp, err := client.ListVoices(callCtx, &ttsv1.ListVoicesRequest{Lang: protoLang})
	if err != nil {
		return nil, err
	}

	voices := make([]VoiceInfo, 0, len(resp.GetVoices()))
	for _, v := range resp.GetVoices() {
		langs := append([]string(nil), v.GetLanguages()...)
		voices = append(voices, VoiceInfo{
			ID:          v.GetId(),
			Name:        v.GetName(),
			Languages:   langs,
			Gender:      v.GetGender(),
			Description: v.GetDescription(),
		})
	}
	defaults := make(map[string]string, len(resp.GetDefaultVoices()))
	for k, v := range resp.GetDefaultVoices() {
		defaults[k] = v
	}

	return &VoicesResponse{
		Engine:        resp.GetEngine(),
		Voices:        voices,
		DefaultVoices: defaults,
	}, nil
}

func renderSingleLineTTSGRPC(
	ctx context.Context,
	grpcAddr string,
	apiToken string,
	text string,
	lang string,
	voice string,
	speed float64,
) ([]byte, error) {
	conn, err := getGRPCConn(grpcAddr)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(withGRPCAuth(ctx, apiToken), 30*time.Second)
	defer cancel()

	req := &ttsv1.SynthesizeRequest{
		Text:  text,
		Lang:  mapProtoLanguage(lang),
		Speed: float32(speed),
	}
	if voice != "" {
		if isLegacyRoleAlias(voice) {
			req.Role = mapProtoRole(voice)
		} else {
			req.Voice = voice
		}
	}

	client := ttsv1.NewTTSServiceClient(conn)
	resp, err := client.Synthesize(callCtx, req)
	if err != nil {
		return nil, err
	}
	return resp.GetAudioWav(), nil
}

func syncSubtitleGRPC(
	ctx context.Context,
	grpcAddr string,
	apiToken string,
	targetSubPath string,
	refPath string,
	outputSubPath string,
	refType ttsv1.ReferenceType,
) error {
	conn, err := getGRPCConn(grpcAddr)
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(withGRPCAuth(ctx, apiToken), 5*time.Minute)
	defer cancel()

	client := ttsv1.NewTTSServiceClient(conn)
	stream, err := client.SyncSubtitleStream(callCtx)
	if err != nil {
		return err
	}

	// 1. Metadata chunk specifying reference track type (must be first).
	if err := stream.Send(&ttsv1.SyncSubtitleChunk{
		Payload: &ttsv1.SyncSubtitleChunk_Metadata{
			Metadata: &ttsv1.SyncSubtitleMetadata{RefType: refType},
		},
	}); err != nil {
		return err
	}

	// 2. Stream target SRT bytes.
	if err := streamFileChunks(targetSubPath, func(chunk []byte) error {
		return stream.Send(&ttsv1.SyncSubtitleChunk{
			Payload: &ttsv1.SyncSubtitleChunk_TargetSrtChunk{TargetSrtChunk: chunk},
		})
	}); err != nil {
		return err
	}

	// 3. Stream reference subtitle or audio bytes.
	if err := streamFileChunks(refPath, func(chunk []byte) error {
		return stream.Send(&ttsv1.SyncSubtitleChunk{
			Payload: &ttsv1.SyncSubtitleChunk_ReferenceChunk{ReferenceChunk: chunk},
		})
	}); err != nil {
		return err
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}

	return os.WriteFile(outputSubPath, resp.GetSyncedSrt(), 0644)
}

func streamFileChunks(path string, send func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, grpcStreamChunkBytes)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := send(chunk); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
