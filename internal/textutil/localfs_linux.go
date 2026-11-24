//go:build linux

package textutil

import (
	"path/filepath"
	"syscall"
)

// IsLocalPath reports whether path resides on a local filesystem rather than NFS/SMB/CIFS/FUSE.
func IsLocalPath(p string) bool {
	var st syscall.Statfs_t
	target := p
	if err := syscall.Statfs(target, &st); err != nil {
		if err2 := syscall.Statfs(filepath.Dir(target), &st); err2 != nil {
			return false
		}
	}

	switch uint64(st.Type) {
	case 0x6969, // NFS_SUPER_MAGIC
		0x517B,     // SMB_SUPER_MAGIC
		0xFF534D42, // CIFS_MAGIC_NUMBER
		0xFE534D42, // SMB2_MAGIC_NUMBER
		0x65735546, // FUSE_SUPER_MAGIC (sshfs, rclone, etc.)
		0x5346414F, // AFS_SUPER_MAGIC
		0x01021997: // V9FS_MAGIC
		return false
	default:
		return true
	}
}
