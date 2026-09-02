package runner

import (
	"archive/zip"
	"context"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/sandboxfs"
)

// snapshotTree is an agent's home as of its last sync, read straight out of the
// block store. It is what makes a home readable while its runner is offline —
// and that is precisely the moment somebody needs it: browsing the work of an
// agent whose host is down.
//
// Read-only, and not out of caution. A snapshot is a state that was; writing
// into it would produce a second state beside the working copy that is coming
// back, and nothing could then say which of the two is the home.
type snapshotTree struct {
	blobs homestore.BlobStore
	orgID uuid.UUID
	// byPath is the manifest as a lookup, dirs included.
	byPath map[string]homestore.Entry
}

func newSnapshotTree(blobs homestore.BlobStore, orgID uuid.UUID, m homestore.Manifest) sandboxfs.Tree {
	t := &snapshotTree{blobs: blobs, orgID: orgID, byPath: make(map[string]homestore.Entry, len(m.Entries))}
	for _, e := range m.Entries {
		t.byPath[e.Path] = e
	}
	return t
}

var errReadOnly = &sandboxfs.ReadOnlyError{
	Reason: "this home is being read from its last snapshot because its runner is not connected — " +
		"changes would be lost as soon as the runner comes back",
}

func (t *snapshotTree) Write(string, io.Reader) (sandboxfs.Entry, error) {
	return sandboxfs.Entry{}, errReadOnly
}
func (t *snapshotTree) Mkdir(string) (sandboxfs.Entry, error) { return sandboxfs.Entry{}, errReadOnly }
func (t *snapshotTree) Remove(string) error                   { return errReadOnly }
func (t *snapshotTree) Move(string, string) (sandboxfs.Entry, error) {
	return sandboxfs.Entry{}, errReadOnly
}

func (t *snapshotTree) entry(rel string) (homestore.Entry, sandboxfs.Entry, bool) {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	e, ok := t.byPath[rel]
	if !ok {
		return homestore.Entry{}, sandboxfs.Entry{}, false
	}
	return e, t.toEntry(e), true
}

func (t *snapshotTree) toEntry(e homestore.Entry) sandboxfs.Entry {
	return sandboxfs.Entry{
		Name:    path.Base(e.Path),
		Path:    e.Path,
		IsDir:   e.Dir,
		Size:    e.Size,
		Mode:    e.Mode.String(),
		Symlink: e.Link,
		Preview: sandboxfs.PreviewKind(path.Base(e.Path)),
	}
}

func (t *snapshotTree) List(rel string) (sandboxfs.Listing, error) {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	out := sandboxfs.Listing{Path: rel, Exists: true, ReadOnly: true, ReadOnlyReason: errReadOnly.Reason}
	for p, e := range t.byPath {
		if path.Dir(p) == "." && rel == "" {
			out.Entries = append(out.Entries, t.toEntry(e))
			continue
		}
		if rel != "" && path.Dir(p) == rel {
			out.Entries = append(out.Entries, t.toEntry(e))
		}
	}
	if len(out.Entries) == 0 && rel != "" {
		if _, _, ok := t.entry(rel); !ok {
			return sandboxfs.Listing{Path: rel}, sandboxfs.ErrNotFound
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].IsDir != out.Entries[j].IsDir {
			return out.Entries[i].IsDir
		}
		return out.Entries[i].Name < out.Entries[j].Name
	})
	return out, nil
}

func (t *snapshotTree) Read(rel string) (sandboxfs.File, error) {
	raw, e, err := t.content(rel, sandboxfs.MaxReadBytes+1)
	if err != nil {
		return sandboxfs.File{}, err
	}
	return sandboxfs.Describe(e.Path, e.Size, e.Mode.String(), "", raw), nil
}

func (t *snapshotTree) Open(rel string) (io.ReadCloser, sandboxfs.FileInfo, error) {
	e, _, ok := t.entry(rel)
	if !ok {
		return nil, sandboxfs.FileInfo{}, sandboxfs.ErrNotFound
	}
	if e.Dir {
		return nil, sandboxfs.FileInfo{}, sandboxfs.ErrIsDir
	}
	return &blockReader{tree: t, blocks: e.Blocks}, sandboxfs.FileInfo{
		Name: path.Base(e.Path), Size: e.Size,
	}, nil
}

// content reads a file out of its blocks, up to limit bytes.
func (t *snapshotTree) content(rel string, limit int64) ([]byte, homestore.Entry, error) {
	e, _, ok := t.entry(rel)
	if !ok {
		return nil, homestore.Entry{}, sandboxfs.ErrNotFound
	}
	if e.Dir {
		return nil, e, sandboxfs.ErrIsDir
	}
	rc := &blockReader{tree: t, blocks: e.Blocks}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, limit))
	return raw, e, err
}

func (t *snapshotTree) PlanZip(rels []string) (sandboxfs.ZipPlan, error) {
	plan := sandboxfs.ZipPlan{Paths: append([]string(nil), rels...)}
	seen := map[string]bool{}
	for _, rel := range rels {
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if _, _, ok := t.entry(rel); !ok {
			return sandboxfs.ZipPlan{}, sandboxfs.ErrNotFound
		}
		for p, e := range t.byPath {
			if p != rel && !strings.HasPrefix(p, rel+"/") {
				continue
			}
			if seen[p] || e.Dir {
				continue
			}
			seen[p] = true
			plan.Files++
			plan.Bytes += e.Size
			if plan.Files > sandboxfs.MaxZipFiles {
				return sandboxfs.ZipPlan{}, sandboxfs.ErrTooMany
			}
			if plan.Bytes > sandboxfs.MaxZipBytes {
				return sandboxfs.ZipPlan{}, sandboxfs.ErrTooLarge
			}
		}
	}
	plan.Name = sandboxfs.ZipName(rels)
	return plan, nil
}

func (t *snapshotTree) WriteZip(w io.Writer, plan sandboxfs.ZipPlan) error {
	zw := zip.NewWriter(w)
	var paths []string
	seen := map[string]bool{}
	for _, rel := range plan.Paths {
		rel = strings.Trim(path.Clean("/"+rel), "/")
		for p, e := range t.byPath {
			if e.Dir || (p != rel && !strings.HasPrefix(p, rel+"/")) || seen[p] {
				continue
			}
			// Once, however many selected paths cover it — a selection of
			// "a" and "a/b" wrote a/b twice (#165), as PlanZip already knew.
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		e := t.byPath[p]
		hdr := &zip.FileHeader{Name: p, Modified: time.Time{}, Method: zip.Deflate}
		hdr.SetMode(e.Mode)
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		rc := &blockReader{tree: t, blocks: e.Blocks}
		_, err = io.Copy(dst, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// Usage over a snapshot answers what it can: how big the home is. Free space is
// a property of a disk, and a snapshot lies on none — reporting a figure from
// the control plane's own disk would be a number about the wrong machine.
func (t *snapshotTree) Usage() sandboxfs.Usage {
	out := sandboxfs.Usage{Exists: true}
	byCheckout := map[string]int64{}
	for p, e := range t.byPath {
		if e.Dir {
			continue
		}
		if rest, ok := strings.CutPrefix(p, "repos/"); ok {
			name, _, _ := strings.Cut(rest, "/")
			byCheckout[name] += e.Size
		}
	}
	for name, bytes := range byCheckout {
		out.Checkouts = append(out.Checkouts, sandboxfs.CheckoutUsage{Name: name, Bytes: bytes})
		out.CheckoutBytes += bytes
	}
	sort.Slice(out.Checkouts, func(i, j int) bool { return out.Checkouts[i].Bytes > out.Checkouts[j].Bytes })
	return out
}

// blockReader assembles a file out of its blocks, one at a time — a 4 GB
// archive must not have to fit in memory to be downloadable.
type blockReader struct {
	tree   *snapshotTree
	blocks []string
	cur    io.ReadCloser
}

func (r *blockReader) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			if len(r.blocks) == 0 {
				return 0, io.EOF
			}
			rc, err := r.tree.blobs.Get(context.Background(), r.tree.orgID, r.blocks[0])
			if err != nil {
				return 0, err
			}
			r.blocks = r.blocks[1:]
			r.cur = rc
		}
		n, err := r.cur.Read(p)
		if n > 0 {
			return n, nil
		}
		if err == io.EOF {
			r.cur.Close()
			r.cur = nil
			continue
		}
		if err != nil {
			return 0, err
		}
	}
}

func (r *blockReader) Close() error {
	if r.cur != nil {
		return r.cur.Close()
	}
	return nil
}
