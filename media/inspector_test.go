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
