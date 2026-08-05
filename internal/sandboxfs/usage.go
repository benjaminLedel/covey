package sandboxfs

import (
	"os"
	"path/filepath"
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

	var fs syscall.Statfs_t
	if err := syscall.Statfs(f.root, &fs); err == nil {
		u.TotalBytes = int64(fs.Blocks) * int64(fs.Bsize)
		u.FreeBytes = int64(fs.Bavail) * int64(fs.Bsize)
	}

	entries, err := os.ReadDir(filepath.Join(f.root, "repos"))
	if err != nil {
		return u
	}
	for i, e := range entries {
		if !e.IsDir() || i >= maxCheckoutsMeasured {
			continue
		}
		dir := filepath.Join(f.root, "repos", e.Name())
		size := dirSize(dir)
		mod := ""
		if info, err := e.Info(); err == nil {
			mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		u.Checkouts = append(u.Checkouts, CheckoutUsage{Name: e.Name(), Bytes: size, ModTime: mod})
		u.CheckoutBytes += size
	}
	sort.Slice(u.Checkouts, func(i, j int) bool { return u.Checkouts[i].Bytes > u.Checkouts[j].Bytes })
	return u
}

// dirSize adds up the file sizes of a tree. Symlinks are not followed (Lstat
// through WalkDir's DirEntry) — a link into the home would otherwise be counted
// twice, one out of it would count foreign bytes.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
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
