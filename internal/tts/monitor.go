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
	err := CheckServerHealthContext(ctx, cfg)
	latency := time.Since(start).Milliseconds()

	next := HealthStatus{
		Available:   err == nil,
		URL:         cfg.ApiUrl,
		LatencyMs:   latency,
		LastChecked: time.Now().UTC(),
	}
	if err != nil {
		next.Error = err.Error()
	}

	m.mu.Lock()
	changed := !m.initialized || m.status.Available != next.Available || m.status.URL != next.URL
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
