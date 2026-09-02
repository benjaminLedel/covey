//go:build !windows

package homestore

import (
	"io/fs"
	"syscall"
)

// inodeOf is the file's inode: the third figure of the cache's bet. A file
// replaced by another of the same size and mtime — a checkout, an extracted
// archive — changes it.
func inodeOf(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
