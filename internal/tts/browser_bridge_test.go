package tts

import (
	"context"
	"testing"
)

func TestBrowserTTSBridge_SynthesizeAndComplete(t *testing.T) {
	mockWav := []byte("RIFF....WAVEfmt data_mock_kokoro_js_audio")

	var bridge *BrowserTTSBridge
	bridge = NewBrowserTTSBridge(func(req BrowserTTSRequest) {
		go func() {
			bridge.Complete(req.ReqID, mockWav, "")
		}()
	})

	bridge.SetStatus(BrowserTTSStatus{Ready: true, Device: "webgpu", Voice: "af_heart"})
	if !bridge.IsReady() {
		t.Fatalf("expected bridge.IsReady() = true")
	}

	got, err := bridge.Synthesize(context.Background(), "Hello from browser Kokoro!", 1.1)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if string(got) != string(mockWav) {
		t.Errorf("Synthesize returned %q; want %q", string(got), string(mockWav))
	}
}
