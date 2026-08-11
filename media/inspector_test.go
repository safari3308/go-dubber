package media

import (
	"testing"
)

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]string
		expected string
	}{
		{"Nil tags", nil, "unknown"},
		{"Vietnamese tag (vie)", map[string]string{"language": "vie"}, "vi"},
		{"Vietnamese tag (vi)", map[string]string{"lang": "vi"}, "vi"},
		{"Vietnamese in title", map[string]string{"title": "Tiếng Việt Thuyết Minh"}, "vi"},
		{"English tag (eng)", map[string]string{"language": "eng"}, "en"},
		{"English tag (en)", map[string]string{"lang": "en"}, "en"},
		{"English in title", map[string]string{"title": "English Subtitles"}, "en"},
		{"Unknown language", map[string]string{"language": "jpn", "title": "Japanese"}, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLanguage(tt.tags)
			if result != tt.expected {
				t.Errorf("normalizeLanguage(%v) = %q; want %q", tt.tags, result, tt.expected)
			}
		})
	}
}

func TestIsBitmapSubtitleCodec(t *testing.T) {
	tests := []struct {
		codec    string
		expected bool
	}{
		{"dvd_subtitle", true},
		{"dvdsub", true},
		{"hdmv_pgs_subtitle", true},
		{"pgssub", true},
		{"pgs", true},
		{"xsub", true},
		{"dvb_subtitle", true},
		{"arib_caption", true},
		{"subrip", false},
		{"srt", false},
		{"ass", false},
		{"ssa", false},
		{"mov_text", false},
		{"webvtt", false},
		{"text", false},
		{"microdvd", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			result := IsBitmapSubtitleCodec(tt.codec)
			if result != tt.expected {
				t.Errorf("IsBitmapSubtitleCodec(%q) = %v; want %v", tt.codec, result, tt.expected)
			}
		})
	}
}
