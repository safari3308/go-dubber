//go:build !darwin && !linux

package textutil

import "strings"

// IsLocalPath reports whether path appears to be a local path (not a UNC network share).
func IsLocalPath(p string) bool {
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`) {
		return false
	}
	return true
}
