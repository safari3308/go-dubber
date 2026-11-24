package tts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// BrowserTTSStatus describes whether an active browser tab has loaded KokoroJS for English fallback.
type BrowserTTSStatus struct {
	Ready  bool   `json:"ready"`
	Device string `json:"device,omitempty"` // "webgpu" or "wasm"
	Voice  string `json:"voice,omitempty"`  // e.g. "af_heart"
}

type browserTTSResult struct {
	wav []byte
	err error
}

// BrowserTTSRequest is broadcast over SSE to the browser tab running KokoroJS.
type BrowserTTSRequest struct {
	ReqID string  `json:"req_id"`
	Text  string  `json:"text"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
}

// BrowserTTSBridge coordinates English TTS synthesis via a browser tab running kokoro-js
// when no remote TTS backend is available.
type BrowserTTSBridge struct {
	mu       sync.Mutex
	status   BrowserTTSStatus
	pending  map[string]chan browserTTSResult
	dispatch func(req BrowserTTSRequest)
}

// NewBrowserTTSBridge creates a bridge wired to an SSE dispatch function.
func NewBrowserTTSBridge(dispatch func(req BrowserTTSRequest)) *BrowserTTSBridge {
	return &BrowserTTSBridge{
		pending:  make(map[string]chan browserTTSResult),
		dispatch: dispatch,
	}
}

// SetStatus updates whether the browser KokoroJS worker is loaded and ready.
func (b *BrowserTTSBridge) SetStatus(st BrowserTTSStatus) {
	b.mu.Lock()
	b.status = st
	// If browser worker went offline, unblock any pending requests cleanly.
	if !st.Ready {
		for id, ch := range b.pending {
			select {
			case ch <- browserTTSResult{err: fmt.Errorf("browser KokoroJS worker disconnected")}:
			default:
			}
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
}

// Status returns the current browser KokoroJS status.
func (b *BrowserTTSBridge) Status() BrowserTTSStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// IsReady returns true when a browser tab has KokoroJS loaded and ready.
func (b *BrowserTTSBridge) IsReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status.Ready
}

// Synthesize sends a single English text line to the browser KokoroJS worker over SSE and awaits the WAV bytes.
func (b *BrowserTTSBridge) Synthesize(ctx context.Context, text string, speed float64) ([]byte, error) {
	b.mu.Lock()
	if !b.status.Ready {
		b.mu.Unlock()
		return nil, fmt.Errorf("browser KokoroJS is not enabled/ready")
	}
	voice := b.status.Voice
	if voice == "" {
		voice = "af_heart"
	}
	reqID := newBrowserReqID()
	ch := make(chan browserTTSResult, 1)
	b.pending[reqID] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, reqID)
		b.mu.Unlock()
	}()

	if speed <= 0 {
		speed = 1.1
	}

	if b.dispatch != nil {
		b.dispatch(BrowserTTSRequest{
			ReqID: reqID,
			Text:  text,
			Voice: voice,
			Speed: speed,
		})
	}

	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("browser KokoroJS synthesis timed out for req %s", reqID)
	case res := <-ch:
		return res.wav, res.err
	}
}

// Complete fulfills a pending synthesis request with WAV bytes or an error message from the browser.
func (b *BrowserTTSBridge) Complete(reqID string, wav []byte, errMsg string) bool {
	b.mu.Lock()
	ch, ok := b.pending[reqID]
	if ok {
		delete(b.pending, reqID)
	}
	b.mu.Unlock()

	if !ok {
		return false
	}

	var res browserTTSResult
	if errMsg != "" {
		res.err = fmt.Errorf("browser KokoroJS error: %s", errMsg)
	} else {
		res.wav = wav
	}
	ch <- res
	return true
}

func newBrowserReqID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "btts-" + hex.EncodeToString(buf)
}
