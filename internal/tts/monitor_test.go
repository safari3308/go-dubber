package tts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/safari3308/go-dubber/internal/config"
)

func TestHealthMonitor_StateTransitions(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := &config.Config{ApiUrl: srv.URL, GrpcUrl: "disabled"}
	var events []HealthStatus
	mon := NewHealthMonitor(func() *config.Config { return cfg }, 50*time.Millisecond, func(st HealthStatus) {
		events = append(events, st)
	})

	st1 := mon.CheckNow(context.Background())
	if !st1.Available {
		t.Fatalf("expected Available=true initially, got %+v", st1)
	}

	// Same state should not emit duplicate change callback
	mon.CheckNow(context.Background())
	if len(events) != 1 {
		t.Errorf("expected 1 change event, got %d", len(events))
	}

	// Flip to unhealthy
	healthy.Store(false)
	st2 := mon.CheckNow(context.Background())
	if st2.Available {
		t.Fatalf("expected Available=false after server down")
	}
	if len(events) != 2 {
		t.Errorf("expected 2 change events after transition, got %d", len(events))
	}
}
