package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/safari3308/go-dubber/internal/config"
	"github.com/safari3308/go-dubber/internal/subtitle"
)

func TestExtractPCMFromWAV(t *testing.T) {
	dataPayload := []byte("1234567890PCM_DATA")
	wavWithData := append([]byte("RIFF1234WAVEfmt ....data\x12\x00\x00\x00"), dataPayload...)

	pcm := ExtractPCMFromWAV(wavWithData)
	if string(pcm) != string(dataPayload) {
		t.Errorf("ExtractPCMFromWAV = %q; want %q", string(pcm), string(dataPayload))
	}

	fallbackHeader := make([]byte, 50)
	copy(fallbackHeader, []byte("HEADER_NO_DATA_TAG_OVER_44_BYTES_LONG_DUMMY_HEADER"))
	pcmFallback := ExtractPCMFromWAV(fallbackHeader)
	if len(pcmFallback) != 6 {
		t.Errorf("ExtractPCMFromWAV fallback length = %d; want 6", len(pcmFallback))
	}

	if pcmShort := ExtractPCMFromWAV([]byte("short")); pcmShort != nil {
		t.Errorf("ExtractPCMFromWAV short slice = %v; want nil", pcmShort)
	}
}

func TestTrimSilencePCM16(t *testing.T) {
	silentSample := []byte{0, 0}
	nonSilentSample := []byte{0x00, 0x10}

	var pcm []byte
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, silentSample...)
	pcm = append(pcm, nonSilentSample...)
	pcm = append(pcm, silentSample...)

	trimmed := TrimSilencePCM16(pcm, 300)

	if len(trimmed) != 2 {
		t.Fatalf("TrimSilencePCM16 length = %d; want 2", len(trimmed))
	}

	if !bytes.Equal(trimmed, nonSilentSample) {
		t.Errorf("TrimSilencePCM16 content = %v; want %v", trimmed, nonSilentSample)
	}
}

func TestMixPCM16(t *testing.T) {
	canvas := make([]byte, 8)
	canvas[0] = 100
	canvas[1] = 0

	pcm := make([]byte, 4)
	pcm[0] = 200
	pcm[1] = 0

	MixPCM16(canvas, pcm, 0)

	mixedSample := int16(canvas[0]) | (int16(canvas[1]) << 8)
	if mixedSample != 300 {
		t.Errorf("Mixed sample = %d; want 300", mixedSample)
	}
}

func TestCreateWAVHeader(t *testing.T) {
	header := createWAVHeader(100, 24000, 1, 16)

	if len(header) != 44 {
		t.Fatalf("Header length = %d; want 44", len(header))
	}
	if string(header[0:4]) != "RIFF" {
		t.Errorf("Header RIFF tag = %q; want RIFF", string(header[0:4]))
	}
	if string(header[8:12]) != "WAVE" {
		t.Errorf("Header WAVE tag = %q; want WAVE", string(header[8:12]))
	}
	if string(header[36:40]) != "data" {
		t.Errorf("Header data tag = %q; want data", string(header[36:40]))
	}
}

func TestConvertToSRT(t *testing.T) {
	tempDir := t.TempDir()
	assFile := filepath.Join(tempDir, "sample.ass")
	outSrtFile := filepath.Join(tempDir, "converted.srt")

	assContent := `[Script Info]
Title: Sample ASS
ScriptType: v4.00+
Collisions: Normal
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\an8}Hello from external ASS subtitle!
`
	if err := os.WriteFile(assFile, []byte(assContent), 0644); err != nil {
		t.Fatalf("failed to write sample.ass: %v", err)
	}

	if err := ConvertToSRT(assFile, outSrtFile); err != nil {
		t.Fatalf("ConvertToSRT failed: %v", err)
	}

	entries, err := subtitle.ParseSRT(outSrtFile, "en")
	if err != nil {
		t.Fatalf("ParseSRT failed on converted SRT: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry from converted SRT, got %d", len(entries))
	}

	if entries[0].Text != "Hello from external ass subtitle!" {
		t.Errorf("expected clean text 'Hello from external ass subtitle!', got %q", entries[0].Text)
	}
}

func makeToneWAV(sampleRate int, durationSec float64, amplitude int16) []byte {
	numSamples := int(math.Round(float64(sampleRate) * durationSec))
	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		val := int16(float64(amplitude) * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(val))
	}
	hdr := createWAVHeader(len(pcm), sampleRate, 1, 16)
	return append(hdr, pcm...)
}

func TestExtractSampleRateFromWAV(t *testing.T) {
	if sr := ExtractSampleRateFromWAV(createWAVHeader(100, 24000, 1, 16)); sr != 24000 {
		t.Errorf("ExtractSampleRateFromWAV(24000) = %d; want 24000", sr)
	}
	if sr := ExtractSampleRateFromWAV(createWAVHeader(100, 48000, 1, 16)); sr != 48000 {
		t.Errorf("ExtractSampleRateFromWAV(48000) = %d; want 48000", sr)
	}
	if sr := ExtractSampleRateFromWAV([]byte("not a wav")); sr != 0 {
		t.Errorf("ExtractSampleRateFromWAV(invalid) = %d; want 0", sr)
	}
}

func TestResamplePCM16Mono(t *testing.T) {
	// 0.1s at 24kHz = 2400 samples = 4800 bytes
	in24k := make([]byte, 2400*2)
	for i := 0; i < 2400; i++ {
		binary.LittleEndian.PutUint16(in24k[i*2:i*2+2], uint16(int16(1000)))
	}
	up48k := ResamplePCM16Mono(in24k, 24000, 48000)
	if len(up48k) != 4800*2 {
		t.Errorf("24k->48k len = %d; want %d", len(up48k), 4800*2)
	}
	down24k := ResamplePCM16Mono(up48k, 48000, 24000)
	if len(down24k) != 2400*2 {
		t.Errorf("48k->24k len = %d; want %d", len(down24k), 2400*2)
	}
	same := ResamplePCM16Mono(in24k, 24000, 24000)
	if len(same) != len(in24k) {
		t.Errorf("same-rate len = %d; want %d", len(same), len(in24k))
	}
}

func TestProcessDubbingPipeline_DynamicSampleRates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rates []int
		want  int
	}{
		{name: "uniform 24kHz", rates: []int{24000, 24000}, want: 24000},
		{name: "uniform 48kHz", rates: []int{48000, 48000}, want: 48000},
		{name: "mixed 48kHz and 24kHz", rates: []int{48000, 24000}, want: 48000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			srtPath := filepath.Join(tmp, "test.srt")
			outWav := filepath.Join(tmp, "out.wav")
			srt := "1\n00:00:00,500 --> 00:00:01,500\nLine one\n\n2\n00:00:02,000 --> 00:00:03,000\nLine two\n"
			if err := os.WriteFile(srtPath, []byte(srt), 0644); err != nil {
				t.Fatalf("write srt: %v", err)
			}

			cfg := &config.Config{Workers: 1, TTSSpeed: 1.0}
			renderer := func(_ context.Context, _ *config.Config, text, _, _ string) ([]byte, error) {
				sr := tc.rates[0]
				if text == "Line two" {
					sr = tc.rates[1]
				}
				return makeToneWAV(sr, 0.4, 4000), nil
			}

			videoDur := 4.0
			n, err := ProcessDubbingPipelineContext(
				context.Background(),
				cfg,
				srtPath,
				outWav,
				DubbingPipelineOptions{
					ChunksDir:        filepath.Join(tmp, "chunks"),
					ReuseAudioChunks: false,
					KeepChunks:       false,
					TTSRenderer:      renderer,
				},
				"en",
				videoDur,
				nil,
			)
			if err != nil {
				t.Fatalf("ProcessDubbingPipelineContext failed: %v", err)
			}
			if n != 2 {
				t.Fatalf("lines = %d; want 2", n)
			}

			outBytes, err := os.ReadFile(outWav)
			if err != nil {
				t.Fatalf("read outWav: %v", err)
			}
			if gotSR := ExtractSampleRateFromWAV(outBytes); gotSR != tc.want {
				t.Errorf("output WAV sample rate = %d; want %d", gotSR, tc.want)
			}
			expectedPCMBytes := int(videoDur*float64(tc.want)) * 2
			if gotPCM := len(ExtractPCMFromWAV(outBytes)); gotPCM != expectedPCMBytes {
				t.Errorf("output WAV PCM bytes = %d; want %d", gotPCM, expectedPCMBytes)
			}

			// Verify both timeline slots (0.5s and 2.0s) contain non-zero mixed audio.
			pcm := ExtractPCMFromWAV(outBytes)
			for _, sec := range []float64{0.6, 2.1} {
				off := int(sec*float64(tc.want)) * 2
				windowNonZero := false
				for j := off; j < off+200 && j+1 < len(pcm); j += 2 {
					if pcm[j] != 0 || pcm[j+1] != 0 {
						windowNonZero = true
						break
					}
				}
				if !windowNonZero {
					t.Errorf("expected non-zero audio around %.1fs in %d Hz output", sec, tc.want)
				}
			}
		})
	}
}
