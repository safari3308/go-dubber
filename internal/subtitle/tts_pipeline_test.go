package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseASSForTTS_FiltersNoiseAndDrawings(t *testing.T) {
	assContent := `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Sign,,0,0,0,,{\pos(960,200)}Tokyo Station
Dialogue: 0,0:00:02.00,0:00:04.00,Song,,0,0,0,,La la la singing
Dialogue: 0,0:00:03.00,0:00:05.00,Default,SFX,0,0,0,,BOOM!
Dialogue: 0,0:00:04.00,0:00:06.00,Default,TL,0,0,0,,Translator note here
Dialogue: 0,0:00:05.00,0:00:07.00,Default,,0,0,0,,{\pos(100,200)\p1}m 0 0 l 100 0 100 100{\p0}
Dialogue: 0,0:00:08.00,0:00:10.00,Default,,0,0,0,,{\pos(100,200)\p1}m 0 0 l 10 10{\p0}Real spoken line\Nwith newline
Dialogue: 0,0:00:11.00,0:00:12.50,Default,,0,0,0,,[door opens] (sighs)
Dialogue: 0,0:00:11.20,0:00:13.50,Default,,0,0,0,,[tiếng bước chân] Who is there?
`
	tmp := t.TempDir()
	assPath := filepath.Join(tmp, "sample.ass")
	if err := os.WriteFile(assPath, []byte(assContent), 0644); err != nil {
		t.Fatalf("write ass: %v", err)
	}

	reasons := make(map[string]int)
	entries, err := ParseSubtitleForTTS(assPath, ParseOptions{
		Lang:  "en",
		Debug: true,
		Logger: func(reason string, startSec, endSec float64, detail string) {
			reasons[reason]++
		},
	})
	if err != nil {
		t.Fatalf("ParseSubtitleForTTS failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 dialogue entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Text != "Real spoken line with newline" {
		t.Errorf("entry[0].Text = %q; want %q", entries[0].Text, "Real spoken line with newline")
	}
	if entries[1].Text != "Who is there?" {
		t.Errorf("entry[1].Text = %q; want %q", entries[1].Text, "Who is there?")
	}

	if reasons["filtered_style"] != 2 {
		t.Errorf("filtered_style count = %d; want 2", reasons["filtered_style"])
	}
	if reasons["filtered_name"] != 2 {
		t.Errorf("filtered_name count = %d; want 2", reasons["filtered_name"])
	}
	if reasons["empty_after_clean"] != 2 {
		t.Errorf("empty_after_clean count = %d; want 2", reasons["empty_after_clean"])
	}
}

func TestParseSRT_DedupAndAntiOverlap(t *testing.T) {
	// Simulates duplicate/overlapping lines produced by .ass -> .srt conversion,
	// plus an overlapping [sound description] cue and two close consecutive fragments.
	srtContent := `1
00:00:01,000 --> 00:00:03,000
Hello there!

2
00:00:01,200 --> 00:00:03,200
Hello there!

3
00:00:03,400 --> 00:00:05,000
Hello there, my old friend!

4
00:00:06,000 --> 00:00:07,500
[door slams loudly]

5
00:00:06,100 --> 00:00:08,000
Wait for me!

6
00:00:08,100 --> 00:00:09,500
Don't leave yet!

7
00:00:15,000 --> 00:00:20,000
This is a very long first sentence that exceeds the short merge limit so it must not be merged into the next sentence.

8
00:00:18,000 --> 00:00:22,000
And this is a second long overlapping sentence whose boundary must be clamped cleanly.
`
	tmp := t.TempDir()
	srtPath := filepath.Join(tmp, "converted.srt")
	if err := os.WriteFile(srtPath, []byte(srtContent), 0644); err != nil {
		t.Fatalf("write srt: %v", err)
	}

	reasons := make(map[string]int)
	entries, err := ParseSubtitleForTTS(srtPath, ParseOptions{
		Lang:  "en",
		Debug: true,
		Logger: func(reason string, startSec, endSec float64, detail string) {
			reasons[reason]++
		},
	})
	if err != nil {
		t.Fatalf("ParseSubtitleForTTS(srt) failed: %v", err)
	}

	// Expected entries:
	// 0: "Hello there, my old friend!" (1.0 -> 5.0, deduped cues 1, 2, 3)
	// 1: "Wait for me! Don't leave yet!" (6.1 -> 9.5, cue 4 dropped empty, cues 5+6 merged at 100ms gap)
	// 2: long sentence 1 (15.0 -> 18.0, clamped before cue 8 starts at 18.0)
	// 3: long sentence 2 (18.0 -> 22.0)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries after dedup/merge/clamp, got %d: %+v", len(entries), entries)
	}

	if entries[0].Text != "Hello there, my old friend!" || entries[0].StartSec != 1.0 || entries[0].EndSec != 5.0 {
		t.Errorf("entry[0] = %+v; want fuller deduped text [1.0..5.0]", entries[0])
	}
	if entries[1].Text != "Wait for me! Don't leave yet!" {
		t.Errorf("entry[1].Text = %q; want merged close-gap text", entries[1].Text)
	}
	if entries[2].EndSec > entries[3].StartSec {
		t.Errorf("expected non-overlapping boundary between entry[2] (end=%.2f) and entry[3] (start=%.2f)", entries[2].EndSec, entries[3].StartSec)
	}

	if reasons["dedup_overlap"] < 2 {
		t.Errorf("expected at least 2 dedup_overlap logs, got %d", reasons["dedup_overlap"])
	}
	if reasons["merged_close_gap"] != 1 {
		t.Errorf("expected 1 merged_close_gap log, got %d", reasons["merged_close_gap"])
	}
	if reasons["adjusted_overlap"] != 1 {
		t.Errorf("expected 1 adjusted_overlap log, got %d", reasons["adjusted_overlap"])
	}
	if reasons["empty_after_clean"] != 1 {
		t.Errorf("expected 1 empty_after_clean log for [door slams loudly], got %d", reasons["empty_after_clean"])
	}
}

func TestStripVectorDrawings(t *testing.T) {
	in := `{\pos(10,20)\p1}m 0 0 l 50 50 100 0{\p0} Hello {\p2}m 1 1 l 2 2`
	got := strings.TrimSpace(StripVectorDrawings(in))
	if got != "Hello" {
		t.Errorf("StripVectorDrawings = %q; want %q", got, "Hello")
	}
}
