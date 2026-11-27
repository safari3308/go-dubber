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
	expectedToken   string
	engine          string
	model           string
	sampleRate      int32
	nativeRate      int32
	voices          map[string]string
	voiceCatalog    []*ttsv1.VoiceInfo
	defaultVoices   map[string]string
	disableGetInfo  bool
	lastText        string
	lastLang        ttsv1.Language
	lastRole        ttsv1.VoiceRole
	lastVoice       string
	lastRefType     ttsv1.ReferenceType
	lastTargetSRT   []byte
	lastRefBytes    []byte
}

func (s *mockTTSServer) GetInfo(ctx context.Context, _ *ttsv1.GetInfoRequest) (*ttsv1.GetInfoResponse, error) {
	if s.disableGetInfo {
		return nil, status.Error(codes.Unimplemented, "method GetInfo not implemented")
	}
	sr := s.sampleRate
	if sr == 0 {
		sr = 24000
	}
	nsr := s.nativeRate
	if nsr == 0 {
		nsr = sr
	}
	return &ttsv1.GetInfoResponse{
		Engine:           s.engine,
		Model:            s.model,
		SampleRate:       sr,
		NativeSampleRate: nsr,
		Voices:           s.voices,
	}, nil
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

func (s *mockTTSServer) ListVoices(ctx context.Context, req *ttsv1.ListVoicesRequest) (*ttsv1.ListVoicesResponse, error) {
	if s.disableGetInfo {
		return nil, status.Error(codes.Unimplemented, "method ListVoices not implemented")
	}
	var filtered []*ttsv1.VoiceInfo
	wantLang := ""
	switch req.GetLang() {
	case ttsv1.Language_LANGUAGE_VI:
		wantLang = "vi"
	case ttsv1.Language_LANGUAGE_EN:
		wantLang = "en"
	}
	for _, v := range s.voiceCatalog {
		if wantLang == "" {
			filtered = append(filtered, v)
			continue
		}
		for _, l := range v.GetLanguages() {
			if l == wantLang {
				filtered = append(filtered, v)
				break
			}
		}
	}
	return &ttsv1.ListVoicesResponse{
		Engine:        s.engine,
		Voices:        filtered,
		DefaultVoices: s.defaultVoices,
	}, nil
}

func (s *mockTTSServer) Synthesize(ctx context.Context, req *ttsv1.SynthesizeRequest) (*ttsv1.SynthesizeResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	s.lastText = req.GetText()
	s.lastLang = req.GetLang()
	s.lastRole = req.GetRole()
	s.lastVoice = req.GetVoice()
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

func TestGetServerInfo_GRPCAndMonitor(t *testing.T) {
	ResetGRPCFallbackCache()
	mock := &mockTTSServer{
		engine:     "vieneu-tts",
		model:      "v3turbo",
		sampleRate: 48000,
		nativeRate: 48000,
		voices:     map[string]string{"vi_a": "Hải Đăng", "vi_b": "Mai Anh"},
	}
	addr, cleanup := startMockGRPCServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		ApiUrl:  "http://127.0.0.1:1",
		GrpcUrl: addr,
	}

	info, err := GetServerInfo(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GetServerInfo via gRPC failed: %v", err)
	}
	if info.Engine != "vieneu-tts" || info.Model != "v3turbo" || info.SampleRate != 48000 || info.NativeSampleRate != 48000 {
		t.Errorf("unexpected ServerInfo: %+v", info)
	}
	if info.Voices["vi_a"] != "Hải Đăng" {
		t.Errorf("unexpected voices: %+v", info.Voices)
	}

	// Verify HealthMonitor populates Engine, Model, and SampleRate
	mon := NewHealthMonitor(func() *config.Config { return cfg }, time.Second, nil)
	st := mon.CheckNow(context.Background())
	if !st.Available || st.Transport != "grpc" || st.Engine != "vieneu-tts" || st.Model != "v3turbo" || st.SampleRate != 48000 {
		t.Fatalf("unexpected HealthStatus: %+v", st)
	}
}

func TestGetServerInfo_HTTPFallbackAndLegacyDefault(t *testing.T) {
	ResetGRPCFallbackCache()
	deadGRPC := closedLocalAddr(t)

	// 1. HTTP /v1/info fallback
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"engine":"kokoro","model":"kokoro-v1","sample_rate":24000,"native_sample_rate":24000,"voices":{"vi_a":"vf_dora"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer httpSrv.Close()

	cfg := &config.Config{ApiUrl: httpSrv.URL, GrpcUrl: deadGRPC}
	info, err := GetServerInfo(context.Background(), cfg)
	if err != nil {
		t.Fatalf("GetServerInfo via HTTP fallback failed: %v", err)
	}
	if info.Engine != "kokoro" || info.Model != "kokoro-v1" || info.SampleRate != 24000 {
		t.Errorf("unexpected HTTP ServerInfo: %+v", info)
	}

	// 2. Legacy server returning 404 / Unimplemented -> graceful 24000 Hz default
	legacyGRPCMock := &mockTTSServer{disableGetInfo: true}
	legacyAddr, cleanup := startMockGRPCServer(t, legacyGRPCMock)
	defer cleanup()

	legacyHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer legacyHTTP.Close()

	legacyCfg := &config.Config{ApiUrl: legacyHTTP.URL, GrpcUrl: legacyAddr}
	legacyInfo, err := GetServerInfo(context.Background(), legacyCfg)
	if err != nil {
		t.Fatalf("expected graceful fallback on legacy server, got error: %v", err)
	}
	if legacyInfo.SampleRate != DefaultSampleRate || legacyInfo.Engine != "" || legacyInfo.Model != "" {
		t.Errorf("unexpected legacy ServerInfo: %+v", legacyInfo)
	}
}

func TestListVoices_GRPCAndHTTPFallback(t *testing.T) {
	ResetGRPCFallbackCache()
	mock := &mockTTSServer{
		engine: "vieneu-tts",
		voiceCatalog: []*ttsv1.VoiceInfo{
			{Id: "Hải Đăng", Name: "Hải Đăng", Languages: []string{"vi", "en"}, Gender: "male", Description: "Nam · Bắc"},
			{Id: "Ngọc Huyền", Name: "Ngọc Huyền", Languages: []string{"vi", "en"}, Gender: "female", Description: "Nữ · Bắc"},
		},
		defaultVoices: map[string]string{"vi": "Hải Đăng", "en": "Hải Đăng"},
	}
	grpcAddr, cleanup := startMockGRPCServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		GrpcUrl:   grpcAddr,
		ApiToken:  "secret",
		TTSVoices: map[string]string{"vi": "Ngọc Huyền", "en": "Hải Đăng"},
	}
	mock.expectedToken = "secret"

	// 1. ListVoices via gRPC
	res, err := ListVoices(context.Background(), cfg, "en")
	if err != nil {
		t.Fatalf("ListVoices via gRPC failed: %v", err)
	}
	if res.Engine != "vieneu-tts" || len(res.Voices) != 2 || res.DefaultVoices["vi"] != "Hải Đăng" {
		t.Fatalf("unexpected ListVoices gRPC response: %+v", res)
	}

	// 2. RenderSingleLineTTSContext sends explicit voice from cfg.TTSVoices when voice arg is empty
	if _, err := RenderSingleLineTTSContext(context.Background(), cfg, "Xin chào", "vi", ""); err != nil {
		t.Fatalf("RenderSingleLineTTSContext with default config voice failed: %v", err)
	}
	if mock.lastVoice != "Ngọc Huyền" {
		t.Fatalf("expected explicit voice 'Ngọc Huyền', got %q (role=%v)", mock.lastVoice, mock.lastRole)
	}

	// 3. HTTP /v1/voices fallback when gRPC is unreachable
	deadGRPC := closedLocalAddr(t)
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/voices" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"engine":"kokoro","voices":[{"id":"af_heart","name":"Heart","languages":["en"],"gender":"female"}],"default_voices":{"en":"af_heart"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer httpSrv.Close()

	httpCfg := &config.Config{ApiUrl: httpSrv.URL, GrpcUrl: deadGRPC}
	httpRes, err := ListVoices(context.Background(), httpCfg, "en")
	if err != nil {
		t.Fatalf("ListVoices via HTTP fallback failed: %v", err)
	}
	if httpRes.Engine != "kokoro" || len(httpRes.Voices) != 1 || httpRes.Voices[0].ID != "af_heart" {
		t.Fatalf("unexpected HTTP ListVoices response: %+v", httpRes)
	}
}
