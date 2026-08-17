package homestore

import (
	"path/filepath"
	"syscall"
)

// Space is the file system the blocks lie on: how big it is, and how much of it
// is still free.
//
// Exactly that file system and not the host's root. The block store often sits
// on a volume of its own, and the question worth answering is whether the next
// home still fits — not whether `/var` does.
//
// Deliberately a second implementation beside the runner's: in the remote case
// those are two different machines, and a single figure would have to belong to
// one of them. Fifteen lines of Statfs are the cheaper answer than a shared
// helper that would need to be told which disk it is talking about.
//
// Only the directory backend has this. With an object store the blocks are not
// on a disk of ours at all, and the honest answer is "no figure" rather than
// the free space of a machine that no longer holds them.
func (d *Dir) Space() (total, free int64) {
	// Up to the first directory that exists. The block directory comes into
	// being with the first sync; before that the figure of the volume it will
	// lie on is the honest answer, not zero — which reads like a full disk.
	path := d.root
	for path != "" && path != "/" && path != "." {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err == nil {
			return int64(fs.Blocks) * int64(fs.Bsize), int64(fs.Bavail) * int64(fs.Bsize)
		}
		path = filepath.Dir(path)
	}
	return 0, 0
}
