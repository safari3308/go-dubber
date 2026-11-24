package textutil

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var chunkPattern = regexp.MustCompile(`(\d+|\D+)`)

// NaturalLess implements natural alphanumeric comparison for filenames (e.g. S01E02 before S01E10).
func NaturalLess(s1, s2 string) bool {
	chunks1 := chunkPattern.FindAllString(strings.ToLower(s1), -1)
	chunks2 := chunkPattern.FindAllString(strings.ToLower(s2), -1)

	n := len(chunks1)
	if len(chunks2) < n {
		n = len(chunks2)
	}

	for i := 0; i < n; i++ {
		c1, c2 := chunks1[i], chunks2[i]
		if c1 == c2 {
			continue
		}

		v1, err1 := strconv.ParseUint(c1, 10, 64)
		v2, err2 := strconv.ParseUint(c2, 10, 64)

		if err1 == nil && err2 == nil {
			if v1 != v2 {
				return v1 < v2
			}
			if len(c1) != len(c2) {
				return len(c1) < len(c2)
			}
		} else {
			return c1 < c2
		}
	}

	return len(chunks1) < len(chunks2)
}

// IsSupportedVideoExtension reports whether ext is a supported video file extension.
func IsSupportedVideoExtension(ext string) bool {
	ext = strings.ToLower(ext)
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".m4v", ".webm", ".flv", ".wmv", ".ts", ".mts", ".m2ts":
		return true
	default:
		return false
	}
}

// DiscoverVideoFiles scans dir recursively, filters out temp files, and returns naturally sorted video paths.
func DiscoverVideoFiles(dir string) ([]string, error) {
	var videos []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}

		base := filepath.Base(path)
		if strings.HasPrefix(base, "temp_") || strings.HasPrefix(base, ".upload_") || strings.HasPrefix(base, ".bak_") {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if IsSupportedVideoExtension(ext) {
			videos = append(videos, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(videos, func(i, j int) bool {
		return NaturalLess(filepath.Base(videos[i]), filepath.Base(videos[j]))
	})

	return videos, nil
}
