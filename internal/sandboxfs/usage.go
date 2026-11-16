package sandboxfs

import (
	"io/fs"
	"os"
	"sort"
	"syscall"
)

// The disk of a sandbox was not measured anywhere. That the home is persistent
// and checkouts pile up in it is a design decision (caches survive, the next run
// starts warm) — but a decision nobody could observe the consequences of. A QA
// agent noted in its own wiki that its 40 GB overlay was running full "through
// old checkouts (~/repos) and scratch folders", and shortly after that a run
// ended with `claude exit: signal: killed`. The information existed; only in the
// agent's head, not in the interface.
//
// Hence the workplace shows the space: how full the file system is, and which
// working copies are eating it.

// Usage is the space situation of an agent's home.
type Usage struct {
	// Exists=false: the home has never been created (agent never woken).
	Exists bool `json:"exists"`
	// TotalBytes/FreeBytes come from the file system the home lies on. In the
	// docker provider that is the sandbox overlay, so exactly the figure that
	// decides whether the next npm install still fits.
	TotalBytes int64 `json:"total_bytes"`
	FreeBytes  int64 `json:"free_bytes"`
	// Checkouts are the working copies under repos/, largest first. Deliberately
	// only this one directory: it is the one that grows without bound, and
	// walking the whole home (node_modules with a hundred thousand files) would
	// make the page slow for no gain.
	Checkouts []CheckoutUsage `json:"checkouts"`
	// CheckoutBytes is their total.
	CheckoutBytes int64 `json:"checkout_bytes"`
}

// CheckoutUsage is one working copy under repos/.
type CheckoutUsage struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	ModTime string `json:"mod_time"`
}

// maxCheckoutsMeasured caps the walk. Whoever has more than this lying around
// has a different problem than the exact figure.
const maxCheckoutsMeasured = 40

// Usage reports the space situation. Best effort throughout: a home that cannot
// be read yields Exists=false rather than an error — the workplace should show
// the file tree even when the gauge fails.
func (f *FS) Usage() Usage {
	u := Usage{Checkouts: []CheckoutUsage{}}
	if st, err := os.Stat(f.root); err != nil || !st.IsDir() {
		return u
	}
	u.Exists = true

	var st syscall.Statfs_t
	if err := syscall.Statfs(f.root, &st); err == nil {
		u.TotalBytes = int64(st.Blocks) * int64(st.Bsize)
		u.FreeBytes = int64(st.Bavail) * int64(st.Bsize)
	}

	// Through the same os.Root as every other operation: repos/ and what lies
	// in it are written by the sandbox, and a link there must not make the
	// runner list or measure anything outside the home. A repos that is a
	// link is no checkout directory and is treated as absent.
	root, err := f.openRoot()
	if err != nil {
		return u
	}
	defer root.Close()
	if li, err := root.Lstat("repos"); err != nil || !li.IsDir() {
		return u
	}
	repos, err := root.OpenRoot("repos")
	if err != nil {
		return u
	}
	defer repos.Close()
	rfs := repos.FS()
	entries, err := fs.ReadDir(rfs, ".")
	if err != nil {
		return u
	}
	for i, e := range entries {
		if !e.IsDir() || i >= maxCheckoutsMeasured {
			continue
		}
		info, err := repos.Lstat(e.Name())
		if err != nil || !info.IsDir() {
			continue
		}
		size := dirSize(rfs, e.Name())
		mod := info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		u.Checkouts = append(u.Checkouts, CheckoutUsage{Name: e.Name(), Bytes: size, ModTime: mod})
		u.CheckoutBytes += size
	}
	sort.Slice(u.Checkouts, func(i, j int) bool { return u.Checkouts[i].Bytes > u.Checkouts[j].Bytes })
	return u
}

// dirSize adds up the file sizes of a tree inside fsys, which is rooted in the
// home: directories are opened through the root, so none resolves outside it.
// Symlinks are not followed (Lstat through WalkDir's DirEntry) — a link into
// the home would otherwise be counted twice, one out of it would count foreign
// bytes.
func dirSize(fsys fs.FS, dir string) int64 {
	var total int64
	_ = fs.WalkDir(fsys, dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best effort: an unreadable subtree is skipped
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
