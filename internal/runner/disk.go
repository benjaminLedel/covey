package runner

import (
	"path/filepath"
	"syscall"
)

// diskSpace is the file system the working copies lie on. Exactly that one and
// not the host's root: with a runner the homes often sit on their own volume,
// and the figure that matters is whether the next home still fits — not
// whether /var does.
func diskSpace(path string) (total, free int64) {
	// Up to the first directory that exists. The working directory comes into
	// being with the first sandbox, and before that the honest answer is the
	// figure of the volume it will lie on — not zero, which reads like a disk
	// nobody can write to.
	for path != "" && path != "/" && path != "." {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err == nil {
			return int64(fs.Blocks) * int64(fs.Bsize), int64(fs.Bavail) * int64(fs.Bsize)
		}
		path = filepath.Dir(path)
	}
	return 0, 0
}
