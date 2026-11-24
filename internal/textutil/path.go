package textutil

import (
	"os"
	"path/filepath"
	"strings"
)

// SanitizePath normalizes user-pasted paths, including macOS Finder "Copy as Pathname"
// single/double-quoted strings, shell-escaped spaces, and leading "~/" home prefixes.
func SanitizePath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}

	// Strip matching surrounding single or double quotes (e.g. '/Users/.../video.mkv')
	if len(p) >= 2 {
		if (p[0] == '\'' && p[len(p)-1] == '\'') || (p[0] == '"' && p[len(p)-1] == '"') {
			p = p[1 : len(p)-1]
			p = strings.TrimSpace(p)
		}
	}

	// Expand leading ~/ to user home directory
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}

	// If the path contains POSIX shell backslash escapes (e.g. "\ ", "\(", "\'") and
	// doesn't exist verbatim, unescape them and check if the unescaped path exists.
	if strings.Contains(p, `\`) {
		if _, err := os.Stat(p); err != nil {
			unescaped := unescapeShellPath(p)
			if _, err2 := os.Stat(unescaped); err2 == nil {
				p = unescaped
			}
		}
	}

	return filepath.Clean(p)
}

func unescapeShellPath(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		b.WriteByte(c)
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String()
}
