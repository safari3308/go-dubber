//go:build darwin

package textutil

import (
	"path/filepath"
	"strings"
	"syscall"
)

const darwinMntLocal = 0x00001000

// IsLocalPath reports whether path resides on a local filesystem (e.g. internal SSD)
// rather than a network share (SMB, NFS, AFP, etc.).
func IsLocalPath(p string) bool {
	var st syscall.Statfs_t
	target := p
	if err := syscall.Statfs(target, &st); err != nil {
		// Fall back to parent directory if file doesn't exist yet
		if err2 := syscall.Statfs(filepath.Dir(target), &st); err2 != nil {
			return false
		}
	}

	fsType := cStringFromInt8(st.Fstypename[:])
	switch strings.ToLower(fsType) {
	case "smbfs", "nfs", "afpfs", "webdav", "ftp", "cifs", "osxfuse", "macfuse", "fuse":
		return false
	}

	return (st.Flags & darwinMntLocal) != 0
}

func cStringFromInt8(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
