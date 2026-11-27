package tts

import (
	"context"
	"sync"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
)

// HealthStatus represents the latest TTS service health check result.
type HealthStatus struct {
	Available   bool      `json:"available"`
	URL         string    `json:"url"`
	GrpcURL     string    `json:"grpc_url,omitempty"`
	Transport   string    `json:"transport,omitempty"` // "grpc" or "http"
	Engine      string    `json:"engine,omitempty"`
	Model       string    `json:"model,omitempty"`
	SampleRate  int       `json:"sample_rate,omitempty"`
	LatencyMs   int64     `json:"latency_ms"`
	LastChecked time.Time `json:"last_checked"`
	Error       string    `json:"error,omitempty"`
}

// HealthMonitor periodically probes the configured TTS server and notifies listeners on status changes.
type HealthMonitor struct {
	getCfg   func() *config.Config
	interval time.Duration
	onChange func(HealthStatus)

	mu          sync.RWMutex
	status      HealthStatus
	initialized bool
}

// NewHealthMonitor creates a new background TTS health monitor.
func NewHealthMonitor(getCfg func() *config.Config, interval time.Duration, onChange func(HealthStatus)) *HealthMonitor {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &HealthMonitor{
		getCfg:   getCfg,
		interval: interval,
		onChange: onChange,
	}
}

// Status returns the last cached HealthStatus.
func (m *HealthMonitor) Status() HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// CheckNow immediately probes the TTS server and updates cached state.
func (m *HealthMonitor) CheckNow(ctx context.Context) HealthStatus {
	cfg := m.getCfg()
	start := time.Now()
	transport, _, err := CheckServerHealthWithTransportContext(ctx, cfg)
	latency := time.Since(start).Milliseconds()

	grpcURL := ResolveGrpcAddr(cfg)
	next := HealthStatus{
		Available:   err == nil,
		URL:         cfg.ApiUrl,
		GrpcURL:     grpcURL,
		Transport:   transport,
		LatencyMs:   latency,
		LastChecked: time.Now().UTC(),
	}
	if err != nil {
		next.Error = err.Error()
	} else {
		m.mu.RLock()
		prev := m.status
		wasInit := m.initialized
		m.mu.RUnlock()

		needsInfo := !wasInit || !prev.Available || prev.URL != next.URL || prev.GrpcURL != next.GrpcURL || prev.Transport != next.Transport || prev.SampleRate == 0
		if needsInfo {
			if info, infoErr := GetServerInfo(ctx, cfg); infoErr == nil && info != nil {
				next.Engine = info.Engine
				next.Model = info.Model
				next.SampleRate = info.SampleRate
			} else {
				next.SampleRate = DefaultSampleRate
			}
		} else {
			next.Engine = prev.Engine
			next.Model = prev.Model
			next.SampleRate = prev.SampleRate
		}
	}

	m.mu.Lock()
	changed := !m.initialized ||
		m.status.Available != next.Available ||
		m.status.URL != next.URL ||
		m.status.GrpcURL != next.GrpcURL ||
		m.status.Transport != next.Transport ||
		m.status.Engine != next.Engine ||
		m.status.Model != next.Model ||
		m.status.SampleRate != next.SampleRate
	m.status = next
	m.initialized = true
	m.mu.Unlock()

	if changed && m.onChange != nil {
		m.onChange(next)
	}
	return next
}

// Start runs the periodic health check loop in the background until ctx is cancelled.
func (m *HealthMonitor) Start(ctx context.Context) {
	m.CheckNow(ctx)
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.CheckNow(ctx)
			}
		}
	}()
}
