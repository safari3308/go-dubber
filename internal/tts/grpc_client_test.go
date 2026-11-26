package tts

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/tts/ttsv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type mockTTSServer struct {
	ttsv1.UnimplementedTTSServiceServer
	expectedToken string
	lastText      string
	lastLang      ttsv1.Language
	lastRole      ttsv1.VoiceRole
	lastRefType   ttsv1.ReferenceType
	lastTargetSRT []byte
	lastRefBytes  []byte
}

func (s *mockTTSServer) checkAuth(ctx context.Context) error {
	if s.expectedToken == "" {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 || vals[0] != "Bearer "+s.expectedToken {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

func (s *mockTTSServer) Synthesize(ctx context.Context, req *ttsv1.SynthesizeRequest) (*ttsv1.SynthesizeResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	s.lastText = req.GetText()
	s.lastLang = req.GetLang()
	s.lastRole = req.GetRole()
	return &ttsv1.SynthesizeResponse{
		AudioWav:        []byte("RIFF-GRPC-WAV-DATA"),
		SampleRate:      24000,
		DurationSeconds: 1.25,
	}, nil
}

func (s *mockTTSServer) SyncSubtitleStream(stream grpc.ClientStreamingServer[ttsv1.SyncSubtitleChunk, ttsv1.SyncSubtitleResponse]) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	var targetBuf, refBuf bytes.Buffer
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch p := chunk.Payload.(type) {
		case *ttsv1.SyncSubtitleChunk_Metadata:
			s.lastRefType = p.Metadata.GetRefType()
		case *ttsv1.SyncSubtitleChunk_TargetSrtChunk:
			targetBuf.Write(p.TargetSrtChunk)
		case *ttsv1.SyncSubtitleChunk_ReferenceChunk:
			refBuf.Write(p.ReferenceChunk)
		}
	}
	s.lastTargetSRT = targetBuf.Bytes()
	s.lastRefBytes = refBuf.Bytes()
	return stream.SendAndClose(&ttsv1.SyncSubtitleResponse{
		SyncedSrt: []byte("1\n00:00:02,000 --> 00:00:03,000\nGRPC Synced\n"),
	})
}

func startMockGRPCServer(t *testing.T, mock *mockTTSServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := grpc.NewServer()
	ttsv1.RegisterTTSServiceServer(srv, mock)

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)

	go func() {
		_ = srv.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		srv.Stop()
		_ = lis.Close()
	}
}

func closedLocalAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

func TestResolveGrpcAddr(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"auto derive from http url", &config.Config{ApiUrl: "http://192.168.1.10:8000"}, "192.168.1.10:50051"},
		{"auto derive from localhost", &config.Config{ApiUrl: "http://localhost:8000"}, "localhost:50051"},
		{"explicit host:port", &config.Config{ApiUrl: "http://localhost:8000", GrpcUrl: "10.0.0.5:50052"}, "10.0.0.5:50052"},
		{"explicit scheme", &config.Config{GrpcUrl: "grpc://tts.local:6000"}, "tts.local:6000"},
		{"explicit host without port", &config.Config{GrpcUrl: "tts.local"}, "tts.local:50051"},
		{"disabled keyword", &config.Config{ApiUrl: "http://localhost:8000", GrpcUrl: "disabled"}, ""},
		{"off keyword", &config.Config{ApiUrl: "http://localhost:8000", GrpcUrl: "off"}, ""},
		{"empty config", &config.Config{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveGrpcAddr(tc.cfg); got != tc.want {
				t.Errorf("ResolveGrpcAddr(%+v) = %q; want %q", tc.cfg, got, tc.want)
			}
		})
	}
}

func TestGRPC_SynthesizeSyncAndHealth(t *testing.T) {
	ResetGRPCFallbackCache()
	mock := &mockTTSServer{expectedToken: "secret-token"}
	addr, cleanup := startMockGRPCServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		ApiUrl:   "http://127.0.0.1:1", // unreachable HTTP to prove gRPC is used
		GrpcUrl:  addr,
		ApiToken: "secret-token",
		TTSSpeed: 1.15,
	}

	// 1. Health check via HealthMonitor should report transport = "grpc"
	mon := NewHealthMonitor(func() *config.Config { return cfg }, time.Second, nil)
	st := mon.CheckNow(context.Background())
	if !st.Available || st.Transport != "grpc" {
		t.Fatalf("expected Available=true, Transport=grpc; got %+v", st)
	}

	// 2. Synthesize via gRPC
	wav, err := RenderSingleLineTTSContext(context.Background(), cfg, "Xin chào 2", "vi", "B")
	if err != nil {
		t.Fatalf("RenderSingleLineTTSContext via gRPC failed: %v", err)
	}
	if string(wav) != "RIFF-GRPC-WAV-DATA" {
		t.Errorf("wav = %q; want RIFF-GRPC-WAV-DATA", string(wav))
	}
	if !strings.Contains(mock.lastText, "Xin chào hai") {
		t.Errorf("expected Vietnamese number normalization, got %q", mock.lastText)
	}
	if mock.lastLang != ttsv1.Language_LANGUAGE_VI || mock.lastRole != ttsv1.VoiceRole_VOICE_ROLE_B {
		t.Errorf("unexpected proto lang/role: %v / %v", mock.lastLang, mock.lastRole)
	}

	// 3. SyncSubtitleStream via gRPC (audio reference)
	tmp := t.TempDir()
	targetSRT := filepath.Join(tmp, "target.srt")
	refAudio := filepath.Join(tmp, "ref.aac")
	outSRT := filepath.Join(tmp, "out.srt")
	_ = os.WriteFile(targetSRT, []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0644)
	_ = os.WriteFile(refAudio, []byte("fake-aac-audio"), 0644)

	if err := SyncSubtitleWithServerContext(context.Background(), cfg, targetSRT, refAudio, outSRT); err != nil {
		t.Fatalf("SyncSubtitleWithServerContext via gRPC failed: %v", err)
	}
	if mock.lastRefType != ttsv1.ReferenceType_REFERENCE_TYPE_AUDIO {
		t.Errorf("lastRefType = %v; want AUDIO", mock.lastRefType)
	}
	gotSynced, _ := os.ReadFile(outSRT)
	if !strings.Contains(string(gotSynced), "GRPC Synced") {
		t.Errorf("unexpected synced srt: %q", string(gotSynced))
	}

	// 4. SyncSubtitleStream via gRPC (subtitle reference)
	refSub := filepath.Join(tmp, "ref.srt")
	_ = os.WriteFile(refSub, []byte("1\n00:00:01,000 --> 00:00:02,000\nRef\n"), 0644)
	if err := SyncSubtitleWithServerContext(context.Background(), cfg, targetSRT, refSub, outSRT); err != nil {
		t.Fatalf("SyncSubtitleWithServerContext (sub ref) via gRPC failed: %v", err)
	}
	if mock.lastRefType != ttsv1.ReferenceType_REFERENCE_TYPE_SUB {
		t.Errorf("lastRefType = %v; want SUB", mock.lastRefType)
	}
}

func TestGRPC_AutomaticFallbackToHTTP(t *testing.T) {
	ResetGRPCFallbackCache()
	deadGRPC := closedLocalAddr(t)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/audio/speech":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("RIFF-HTTP-FALLBACK"))
		case "/v1/utils/subsync":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHTTP Fallback Synced\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer httpSrv.Close()

	cfg := &config.Config{
		ApiUrl:   httpSrv.URL,
		GrpcUrl:  deadGRPC,
		ApiToken: "token",
	}

	// 1. Synthesize should fall back cleanly to HTTP
	wav, err := RenderSingleLineTTSContext(context.Background(), cfg, "Hello world", "en", "A")
	if err != nil {
		t.Fatalf("expected HTTP fallback to succeed, got error: %v", err)
	}
	if string(wav) != "RIFF-HTTP-FALLBACK" {
		t.Errorf("wav = %q; want RIFF-HTTP-FALLBACK", string(wav))
	}

	// 2. SyncSubtitle should fall back cleanly to HTTP
	tmp := t.TempDir()
	targetSRT := filepath.Join(tmp, "target.srt")
	refAudio := filepath.Join(tmp, "ref.aac")
	outSRT := filepath.Join(tmp, "out.srt")
	_ = os.WriteFile(targetSRT, []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0644)
	_ = os.WriteFile(refAudio, []byte("audio"), 0644)

	if err := SyncSubtitleWithServerContext(context.Background(), cfg, targetSRT, refAudio, outSRT); err != nil {
		t.Fatalf("expected HTTP subsync fallback to succeed, got: %v", err)
	}
	gotSynced, _ := os.ReadFile(outSRT)
	if !strings.Contains(string(gotSynced), "HTTP Fallback Synced") {
		t.Errorf("unexpected synced srt: %q", string(gotSynced))
	}

	// 3. HealthMonitor should report Available=true, Transport="http"
	mon := NewHealthMonitor(func() *config.Config { return cfg }, time.Second, nil)
	st := mon.CheckNow(context.Background())
	if !st.Available || st.Transport != "http" {
		t.Fatalf("expected Available=true, Transport=http; got %+v", st)
	}
}

func TestGRPC_UnauthenticatedDoesNotFallback(t *testing.T) {
	ResetGRPCFallbackCache()
	mock := &mockTTSServer{expectedToken: "correct-token"}
	addr, cleanup := startMockGRPCServer(t, mock)
	defer cleanup()

	httpCalled := false
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer httpSrv.Close()

	cfg := &config.Config{
		ApiUrl:   httpSrv.URL,
		GrpcUrl:  addr,
		ApiToken: "wrong-token",
	}

	_, err := RenderSingleLineTTSContext(context.Background(), cfg, "Hello", "en", "A")
	if err == nil {
		t.Fatal("expected Unauthenticated error, got nil")
	}
	if httpCalled {
		t.Error("expected Unauthenticated gRPC error not to fall back to HTTP")
	}
}
