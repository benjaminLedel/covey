//go:build windows

package homestore

import "io/fs"

// inodeOf: no inode to speak of here; size and mtime carry the bet alone.
func inodeOf(fs.FileInfo) uint64 { return 0 }
