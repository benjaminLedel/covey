// Package sandboxfs opens an agent's persistent home as a file tree: browse,
// read, upload, modify, delete. It is the agent's workplace (spec/02) — compute
// is ephemeral, the home survives. Because it lives on the host and is merely
// mounted into the sandbox, access works regardless of whether the sandbox is
// currently running; a sleeping agent is not a blind spot.
//
// The entire surface is path handling on foreign input, which is why the whole
// package is built around one rule: **no path leads out of the root.** Paths are
// normalised (`..` falls away) and every file operation runs through an
// os.Root — the operating system resolves beneath the home, and a symlink
// pointing outward fails there.
//
// The containment deliberately does NOT sit in a check of our own any more. A
// check is a separate step, and between checking and opening lies a window in
// which a directory can be swapped for a symlink. That is not theoretical here:
// the home lies on the host, is mounted into the sandbox writable, and the agent
// has a shell in it (dev exec) — it can create links in its own home and swap
// them in a loop. os.Root has no such window; the check happens in the kernel at
// the moment of opening.
//
// One consequence worth knowing: os.Root follows no ABSOLUTE symlinks, even
// when their target would lie inside the home. That is without consequence
// here — what the sandbox links absolutely points at /home/agent/…, a path that
// does not exist on the host at all, so those links were already dead in the
// file browser. Relative links inside the home (the form toolchains create)
// keep working.
package sandboxfs

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits. Deliberately generous, but finite: a home is a workplace, not a file
// server.
const (
	// MaxViewBytes is the excerpt the text view transfers. Larger files arrive
	// truncated — the full content is available through the download.
	MaxViewBytes = 512 << 10 // 512 KiB
	// MaxWriteBytes limits a single uploaded or saved file.
	MaxWriteBytes = 64 << 20 // 64 MiB
	// MaxEntries limits the entries of a listing. A directory with more entries
	// comes back shortened instead of overwhelming the UI.
	MaxEntries = 2000
	// MaxZipFiles/MaxZipBytes limit a bulk download. Both are checked BEFORE the
	// first byte: a stream that breaks off halfway leaves behind a broken
	// archive that does not look unfinished — the limit has to be an error
	// message, not a torso.
	MaxZipFiles = 20000
	MaxZipBytes = 2 << 30 // 2 GiB uncompressed
	// sniffBytes is the excerpt used to tell text from binary.
	sniffBytes = 8000
)

var (
	// ErrNotFound: the path does not exist.
	ErrNotFound = errors.New("path not found")
	// ErrInvalidPath: the path points out of the home or is unusable.
	ErrInvalidPath = errors.New("invalid path")
	// ErrTooLarge: the file exceeds the limit.
	ErrTooLarge = errors.New("file too large")
	// ErrNotDir / ErrIsDir: the operation does not match the target's type.
	ErrNotDir = errors.New("not a directory")
	ErrIsDir  = errors.New("is a directory")
	// ErrExists: the target is taken.
	ErrExists = errors.New("already exists")
	// ErrTooMany: the bulk download covers too many files.
	ErrTooMany = errors.New("too many files")
)

// Preview kinds. Which kind a file has is decided in *one* place — the UI picks
// its rendering from that instead of guessing extensions a second time and
// eventually meaning something different than the server delivering the bytes.
const (
	PreviewText     = "text"     // editable in the editor
	PreviewMarkdown = "markdown" // rendered or as source
	PreviewImage    = "image"    // inline via the preview endpoint
	PreviewPDF      = "pdf"      // inline, embedded
	PreviewCSV      = "csv"      // as a table or as source
	PreviewBinary   = "binary"   // download only
)

// inlineTypes is the allowlist of types that may be delivered *inline* —
// everything else goes out as an attachment only. Deliberately short and
// fail-closed: inline delivery from an agent home means rendering foreign bytes
// on the Covey origin. Images and PDF are worth it, HTML is not.
//
// SVG is included because it executes no script in an <img> context; against a
// direct call of the URL the handler additionally guards with a sandbox CSP
// (files.go).
var inlineTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",
	".pdf":  "application/pdf",
}

// markdownExts/csvExts are the text kinds with a rendering of their own.
var markdownExts = map[string]bool{".md": true, ".markdown": true, ".mdx": true}

var csvExts = map[string]bool{".csv": true, ".tsv": true}

// PreviewKind determines the preview kind from the file name. An empty result =
// not recognisable from the name; the content then decides (text or binary),
// see Read.
func PreviewKind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case markdownExts[ext]:
		return PreviewMarkdown
	case csvExts[ext]:
		return PreviewCSV
	case ext == ".pdf":
		return PreviewPDF
	case inlineTypes[ext] != "":
		return PreviewImage
	}
	return ""
}

// InlineType returns the content type under which a file may be delivered
// inline — empty means: as an attachment only.
func InlineType(name string) string {
	return inlineTypes[strings.ToLower(filepath.Ext(name))]
}

// FS is the file tree of an agent home.
//
// UID/GID are the owner *inside the sandbox* (the user `agent` in the image).
// The control plane runs as root in the deployment; without adjusting the
// ownership, uploaded files would belong to root and the agent could no longer
// modify its own files. -1 = do not set (local development, where Docker
// Desktop maps the ownership anyway).
type FS struct {
	root string
	UID  int
	GID  int
}

// New opens root as a file tree. The directory need not exist — an agent that
// has never been woken has no home yet; the listing is then empty instead of an
// error.
func New(root string, uid, gid int) (*FS, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("sandboxfs: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &FS{root: abs, UID: uid, GID: gid}, nil
}

// Root is the host path of the home — for display and diagnostics.
func (f *FS) Root() string { return f.root }

// Entry is one entry of a listing.
type Entry struct {
	Name string `json:"name"`
	// Path is the path relative to the home, with "/" as the separator.
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	// Symlink carries the target if the entry is a link. A link pointing out of
	// the home is shown but not followed (Outside).
	Symlink string `json:"symlink,omitempty"`
	Outside bool   `json:"outside,omitempty"`
	// Preview is the preview kind by file name (empty = only decidable on open).
	// In the listing it drives the row's icon.
	Preview string `json:"preview,omitempty"`
}

// Listing is a directory listing.
type Listing struct {
	Path string `json:"path"`
	// Exists=false means: the home has never been created (agent never woken).
	Exists    bool    `json:"exists"`
	Truncated bool    `json:"truncated"`
	Entries   []Entry `json:"entries"`
	// ReadOnly says this home is being read from its last snapshot because its
	// runner is not connected. Named in the listing and not only when a write
	// fails: whoever is about to upload something should learn that beforehand,
	// not from an error afterwards.
	ReadOnly bool `json:"read_only,omitempty"`
	// ReadOnlyReason is the sentence the interface shows for it.
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
}

// File is the content of a file for viewing in the browser.
type File struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	ModTime   string `json:"mod_time"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	// Preview tells the UI how to show the file: text, markdown, csv (Content
	// carries the text), image, pdf (Content is empty, the bytes come through
	// the preview endpoint) or binary.
	Preview string `json:"preview"`
	Content string `json:"content"`
}

// List returns the entries of a directory, directories first and alphabetically
// within each group — the ordering a file browser is expected to have.
func (f *FS) List(rel string) (Listing, error) {
	c, err := clean(rel)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: c, Entries: []Entry{}}

	root, err := f.openRoot()
	if errors.Is(err, os.ErrNotExist) {
		// Only the home itself may be missing: it comes into being on first wake.
		if c == "" {
			return out, nil
		}
		return Listing{}, ErrNotFound
	}
	if err != nil {
		return Listing{}, err
	}
	defer root.Close()

	info, err := root.Stat(forRoot(c))
	if errors.Is(err, os.ErrNotExist) {
		if c == "" {
			return out, nil
		}
		return Listing{}, ErrNotFound
	}
	if err != nil {
		return Listing{}, mapEscape(err)
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDir
	}
	out.Exists = true

	dir, err := root.Open(forRoot(c))
	if err != nil {
		return Listing{}, mapEscape(err)
	}
	des, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return Listing{}, err
	}
	sort.Slice(des, func(i, j int) bool {
		if des[i].IsDir() != des[j].IsDir() {
			return des[i].IsDir()
		}
		return strings.ToLower(des[i].Name()) < strings.ToLower(des[j].Name())
	})
	if len(des) > MaxEntries {
		des = des[:MaxEntries]
		out.Truncated = true
	}
	for _, de := range des {
		out.Entries = append(out.Entries, entry(root, c, de.Name()))
	}
	return out, nil
}

// entry builds the entry for a name; symlinks are shown as such (including
// their target and the hint whether they point out of the home).
func entry(root *os.Root, dirRel, name string) Entry {
	rel := path.Join(dirRel, name)
	e := Entry{Name: name, Path: rel}

	li, err := root.Lstat(forRoot(rel))
	if err != nil {
		return e
	}
	if li.Mode()&os.ModeSymlink != 0 {
		if target, err := root.Readlink(forRoot(rel)); err == nil {
			e.Symlink = target
		}
		// Whether the link leads outward no longer needs a check of its own:
		// os.Root refuses to follow it. A Stat that fails while the Lstat
		// succeeded IS the answer.
		si, err := root.Stat(forRoot(rel))
		e.Outside = err != nil
		if err == nil {
			li = si // size/type of the target
		}
	}
	e.IsDir = li.IsDir()
	e.Size = li.Size()
	e.Mode = li.Mode().String()
	e.ModTime = li.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	if !e.IsDir {
		e.Preview = PreviewKind(name)
	}
	return e
}

// Read returns a file for viewing: text up to MaxViewBytes (truncated beyond
// that), binary content as metadata only — carrying bytes through JSON helps
// nobody, that is what Open (download) is for.
func (f *FS) Read(rel string) (File, error) {
	c, err := clean(rel)
	if err != nil {
		return File{}, err
	}
	root, err := f.openRoot()
	if err != nil {
		return File{}, mapEscape(err)
	}
	defer root.Close()

	info, err := root.Stat(forRoot(c))
	if errors.Is(err, os.ErrNotExist) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, mapEscape(err)
	}
	if info.IsDir() {
		return File{}, ErrIsDir
	}
	out := File{
		Path:    c,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		Preview: PreviewKind(info.Name()),
	}
	// Image and PDF are not transferred as text: the UI fetches their bytes
	// through the preview endpoint. A base64 detour through JSON would only
	// triple the volume.
	if out.Preview == PreviewImage || out.Preview == PreviewPDF {
		out.Binary = true
		return out, nil
	}

	fh, err := root.Open(forRoot(c))
	if err != nil {
		return File{}, mapEscape(err)
	}
	defer fh.Close()

	buf := make([]byte, MaxViewBytes+1)
	n, err := io.ReadFull(fh, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return File{}, err
	}
	data := buf[:n]
	if len(data) > MaxViewBytes {
		data = data[:MaxViewBytes]
		out.Truncated = true
	}
	if isBinary(data) {
		out.Binary = true
		out.Preview = PreviewBinary
		return out, nil
	}
	// Truncation happens at byte level; half a UTF-8 character at the end would
	// come through as a replacement character, so drop it.
	if out.Truncated {
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	if out.Preview == "" {
		out.Preview = PreviewText
	}
	out.Content = string(data)
	return out, nil
}

// Open returns the file for download — unchanged, at full length.
func (f *FS) Open(rel string) (io.ReadCloser, FileInfo, error) {
	c, err := clean(rel)
	if err != nil {
		return nil, FileInfo{}, err
	}
	root, err := f.openRoot()
	if err != nil {
		return nil, FileInfo{}, mapEscape(err)
	}
	// The root may be closed right after opening: the returned file descriptor
	// stands on its own, the caller reads through it afterwards.
	defer root.Close()

	info, err := root.Stat(forRoot(c))
	if errors.Is(err, os.ErrNotExist) {
		return nil, FileInfo{}, ErrNotFound
	}
	if err != nil {
		return nil, FileInfo{}, mapEscape(err)
	}
	if info.IsDir() {
		return nil, FileInfo{}, ErrIsDir
	}
	fh, err := root.Open(forRoot(c))
	if err != nil {
		return nil, FileInfo{}, mapEscape(err)
	}
	return fh, FileInfo{
		Name: info.Name(), Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339),
	}, nil
}

// ZipPlan is the fully measured bulk archive: what goes in, how much it is and
// what it should be called. Kept separate from writing so that limits and
// errors (nothing found, too large) can still travel as an HTTP status — once
// the first bytes are out, all that remains is a break mid-download.
type ZipPlan struct {
	// Name is the suggested file name of the archive (without path).
	Name string
	// Files/Bytes is the extent: files and uncompressed total size.
	Files int
	Bytes int64
	// Paths is what was selected. Carried along so that a plan survives the
	// journey to a runner and back: over the link the two passes happen on
	// different machines, and the second one plans afresh from exactly this.
	Paths []string
	items []zipItem
}

// zipItem is one entry in the archive: source relative to the home, name inside
// the archive.
//
// The source is deliberately NOT an absolute host path any more. Planning and
// writing are two passes, and an absolute path carried between them would be
// opened in the second pass without the root — exactly the gap this change is
// about.
type zipItem struct {
	rel  string // relative to the home, "/" as the separator
	name string // path inside the archive, "/" as the separator
	dir  bool
	size int64
	mod  time.Time
}

// PlanZip collects what an archive over these paths would contain. Directories
// go in together with their content, named relative to their parent — whoever
// selects "notes" gets an archive with a folder "notes" inside it and not its
// spilled-out individual parts.
func (f *FS) PlanZip(rels []string) (ZipPlan, error) {
	plan := ZipPlan{Paths: append([]string(nil), rels...)}
	seen := map[string]bool{}

	root, err := f.openRoot()
	if err != nil {
		return ZipPlan{}, mapEscape(err)
	}
	defer root.Close()

	for _, rel := range rels {
		c, err := clean(rel)
		if err != nil {
			return ZipPlan{}, err
		}
		info, err := root.Stat(forRoot(c))
		if errors.Is(err, os.ErrNotExist) {
			return ZipPlan{}, ErrNotFound
		}
		if err != nil {
			return ZipPlan{}, mapEscape(err)
		}
		// The name inside the archive is relative to the parent of the chosen
		// path; for the root itself relative to the root.
		base := path.Dir(c)
		if c == "" || base == "." {
			base = ""
		}
		if err := walkZip(root, c, base, info, seen, &plan); err != nil {
			return ZipPlan{}, err
		}
	}
	if plan.Files == 0 && len(plan.items) == 0 {
		return ZipPlan{}, ErrNotFound
	}
	plan.Name = zipName(rels)
	return plan, nil
}

// walkZip takes a path (file or directory) into the archive recursively.
func walkZip(root *os.Root, rel, base string, info os.FileInfo, seen map[string]bool, plan *ZipPlan) error {
	if seen[rel] {
		return nil // a duplicate selection (folder + a file within it) only once
	}
	seen[rel] = true

	name := rel
	if base != "" {
		name = strings.TrimPrefix(rel, base+"/")
	}
	if name == "" {
		name = path.Base(rel)
	}

	if !info.IsDir() {
		if plan.Files+1 > MaxZipFiles {
			return ErrTooMany
		}
		if plan.Bytes+info.Size() > MaxZipBytes {
			return ErrTooLarge
		}
		plan.items = append(plan.items, zipItem{rel: rel, name: name, size: info.Size(), mod: info.ModTime()})
		plan.Files++
		plan.Bytes += info.Size()
		return nil
	}

	plan.items = append(plan.items, zipItem{rel: rel, name: name + "/", dir: true, mod: info.ModTime()})
	dir, err := root.Open(forRoot(rel))
	if err != nil {
		return mapEscape(err)
	}
	des, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, de := range des {
		childRel := path.Join(rel, de.Name())
		// A symlink pointing out of the home does not belong in the archive —
		// otherwise the download would pack up files of the host as well. The
		// Stat answers that on its own now: os.Root does not follow it, so it
		// fails and the entry drops out.
		childInfo, err := root.Stat(forRoot(childRel))
		if err != nil {
			continue // link outward, broken link or similar — skip, do not abort
		}
		if err := walkZip(root, childRel, base, childInfo, seen, plan); err != nil {
			return err
		}
	}
	return nil
}

// WriteZip writes the planned archive as a stream.
func (f *FS) WriteZip(w io.Writer, plan ZipPlan) error {
	root, err := f.openRoot()
	if err != nil {
		return mapEscape(err)
	}
	defer root.Close()

	zw := zip.NewWriter(w)
	for _, it := range plan.items {
		hdr := &zip.FileHeader{Name: it.name, Modified: it.mod}
		if it.dir {
			hdr.SetMode(0o755 | os.ModeDir)
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			continue
		}
		hdr.Method = zip.Deflate
		hdr.SetMode(0o644)
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := root.Open(forRoot(it.rel))
		if err != nil {
			// Vanished between planning and writing — the agent keeps working.
			// No reason to drop the whole archive. A path that has become a
			// link outward in the meantime lands here too, and is skipped for
			// the same reason.
			continue
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// zipName derives the archive's file name from the selection: for exactly one
// path its name, otherwise a collective name.
func zipName(rels []string) string {
	if len(rels) == 1 {
		clean := strings.TrimPrefix(path.Clean("/"+rels[0]), "/")
		if clean != "" && clean != "." {
			return path.Base(clean) + ".zip"
		}
		return "home.zip"
	}
	return "files.zip"
}

// Write creates a file or replaces it. Missing parent directories come into
// being with it — an upload to `project/new/` should not fail just because
// `new` does not exist yet.
func (f *FS) Write(rel string, r io.Reader) (Entry, error) {
	c, err := clean(rel)
	if err != nil {
		return Entry{}, err
	}
	if c == "" {
		return Entry{}, ErrInvalidPath
	}
	root, err := f.openRoot()
	if errors.Is(err, os.ErrNotExist) {
		// The home comes into being with the first upload — an agent that has
		// never run should still be able to receive a file.
		if err := os.MkdirAll(f.root, 0o755); err != nil {
			return Entry{}, err
		}
		root, err = f.openRoot()
	}
	if err != nil {
		return Entry{}, mapEscape(err)
	}
	defer root.Close()

	if info, err := root.Stat(forRoot(c)); err == nil && info.IsDir() {
		return Entry{}, ErrIsDir
	}
	elternRel := path.Dir(c)
	if elternRel == "." {
		elternRel = ""
	}
	if err := f.mkdirAll(root, elternRel); err != nil {
		return Entry{}, err
	}

	// Write to a neighbouring file first, then rename: otherwise an aborted
	// upload leaves half a file under the right name — and the agent keeps
	// working with it.
	tmpRel, tmp, err := createTemp(root, elternRel)
	if err != nil {
		return Entry{}, err
	}
	defer root.Remove(forRoot(tmpRel)) // no-op after a successful rename

	n, err := io.Copy(tmp, io.LimitReader(r, MaxWriteBytes+1))
	if err != nil {
		tmp.Close()
		return Entry{}, err
	}
	if n > MaxWriteBytes {
		tmp.Close()
		return Entry{}, ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := root.Chmod(forRoot(tmpRel), 0o644); err != nil {
		return Entry{}, err
	}
	f.chown(root, tmpRel)
	if err := root.Rename(forRoot(tmpRel), forRoot(c)); err != nil {
		return Entry{}, mapEscape(err)
	}
	return entry(root, path.Dir(c), path.Base(c)), nil
}

// createTemp lays a temporary file next to the target — inside the root, so
// that even the intermediate step cannot leave the home. os.CreateTemp is no
// use here: it takes a directory path, not a root.
func createTemp(root *os.Root, dirRel string) (string, *os.File, error) {
	for versuch := 0; versuch < 100; versuch++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", nil, err
		}
		rel := path.Join(dirRel, ".covey-upload-"+hex.EncodeToString(b[:]))
		// O_EXCL: whoever gets the name has it — a collision is a repetition,
		// never a silently taken-over foreign file.
		fh, err := root.OpenFile(forRoot(rel), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return rel, fh, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, mapEscape(err)
		}
	}
	return "", nil, fmt.Errorf("sandboxfs: no free temporary name")
}

// Mkdir creates a directory (including missing parents).
func (f *FS) Mkdir(rel string) (Entry, error) {
	c, err := clean(rel)
	if err != nil {
		return Entry{}, err
	}
	if c == "" {
		return Entry{}, ErrInvalidPath
	}
	root, err := f.openRoot()
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(f.root, 0o755); err != nil {
			return Entry{}, err
		}
		root, err = f.openRoot()
	}
	if err != nil {
		return Entry{}, mapEscape(err)
	}
	defer root.Close()

	if _, err := root.Lstat(forRoot(c)); err == nil {
		return Entry{}, ErrExists
	}
	if err := f.mkdirAll(root, c); err != nil {
		return Entry{}, err
	}
	return entry(root, path.Dir(c), path.Base(c)), nil
}

// Remove deletes a file or a directory including its content. The root itself
// is off limits: one does not delete an agent's home from the file browser.
func (f *FS) Remove(rel string) error {
	c, err := clean(rel)
	if err != nil {
		return err
	}
	if c == "" {
		return ErrInvalidPath
	}
	root, err := f.openRoot()
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return mapEscape(err)
	}
	defer root.Close()

	// Lstat, not Stat: a symlink pointing outward is itself an entry of this
	// home and may be removed. What must not happen is following it — and
	// RemoveAll does not, it works within the root.
	if _, err := root.Lstat(forRoot(c)); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return root.RemoveAll(forRoot(c))
}

// Move renames or moves. A taken target is not overwritten — a move that
// silently deletes something else is a trap.
func (f *FS) Move(fromRel, toRel string) (Entry, error) {
	von, err := clean(fromRel)
	if err != nil {
		return Entry{}, err
	}
	nach, err := clean(toRel)
	if err != nil {
		return Entry{}, err
	}
	if von == "" || nach == "" {
		return Entry{}, ErrInvalidPath
	}
	root, err := f.openRoot()
	if errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, mapEscape(err)
	}
	defer root.Close()

	if _, err := root.Lstat(forRoot(von)); errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if _, err := root.Lstat(forRoot(nach)); err == nil {
		return Entry{}, ErrExists
	}
	zielEltern := path.Dir(nach)
	if zielEltern == "." {
		zielEltern = ""
	}
	if err := f.mkdirAll(root, zielEltern); err != nil {
		return Entry{}, err
	}
	if err := root.Rename(forRoot(von), forRoot(nach)); err != nil {
		return Entry{}, mapEscape(err)
	}
	return entry(root, path.Dir(nach), path.Base(nach)), nil
}

// mkdirAll creates the chain and adjusts the ownership on every newly created
// directory (MkdirAll alone would give it to the control plane).
func (f *FS) mkdirAll(root *os.Root, rel string) error {
	if rel == "" {
		return nil // the home itself is already there — the root is open
	}
	// Which parts are missing has to be established BEFORE creating; afterwards
	// everything exists and the ownership would be adjusted on foreign
	// directories too.
	var fehlend []string
	for p := rel; p != "" && p != "."; p = elternteil(p) {
		if _, err := root.Stat(forRoot(p)); err == nil {
			break
		}
		fehlend = append(fehlend, p)
	}
	if err := root.MkdirAll(forRoot(rel), 0o755); err != nil {
		return mapEscape(err)
	}
	for _, p := range fehlend {
		f.chown(root, p)
	}
	return nil
}

// elternteil is path.Dir without its "." for the top level — that way the loop
// in mkdirAll ends at the home instead of one step below it.
func elternteil(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// chown adjusts the sandbox ownership — best effort: as an ordinary user (local
// development) it fails, and there it does not matter either.
func (f *FS) chown(root *os.Root, rel string) {
	if f.UID < 0 || f.GID < 0 {
		return
	}
	_ = root.Chown(forRoot(rel), f.UID, f.GID)
}

// clean normalises a path coming from the client into a relative path below the
// home ("" = the home itself).
//
// This is only normalisation, no longer a security check — containment is
// enforced by os.Root when opening (see openRoot). That separation is the whole
// point of the change: as long as the check was a separate step, there was a
// window between checking and opening, and in that window a symlink can be
// swapped in. The agent can do exactly that: its home lies on the host and is
// mounted into the sandbox writable, and it has a shell in there.
func clean(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if strings.ContainsRune(rel, 0) {
		return "", ErrInvalidPath
	}
	// Taking the detour via "/" makes every `..` fall away, a leading one too.
	c := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	if c == "." {
		return "", nil
	}
	return c, nil
}

// openRoot opens the home as an os.Root. Every path handed to a method of the
// returned root is resolved BENEATH it by the operating system — a symlink
// pointing outward fails, and it fails at the moment of opening rather than in
// a check beforehand. That closes the race the old ensureInside had.
//
// The home need not exist: an agent that has never been woken has none yet.
// Callers turn that into an empty listing resp. ErrNotFound.
func (f *FS) openRoot() (*os.Root, error) {
	return os.OpenRoot(f.root)
}

// rootRelPath is what os.Root expects for the home itself.
const selbst = "."

// forRoot maps the cleaned relative path onto the form os.Root reads.
func forRoot(c string) string {
	if c == "" {
		return selbst
	}
	return filepath.FromSlash(c)
}

// mapEscape turns the refusal of os.Root into our error. os.Root reports an
// escape as a path error; for the caller it is the same case as a textual
// `..` — the path does not belong to this home.
func mapEscape(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return ErrInvalidPath
}

// isBinary decides from a sample whether the content works as text: a NUL byte
// or broken UTF-8 means binary. Deliberately coarse — the only question is
// whether the editor may show the file.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sniff := data
	if len(sniff) > sniffBytes {
		sniff = sniff[:sniffBytes]
	}
	for _, b := range sniff {
		if b == 0 {
			return true
		}
	}
	// At the edge of the excerpt a character may be cut in half — take back up
	// to three bytes before that counts as "binary".
	for i := 0; i < 3 && len(sniff) > 0 && !utf8.Valid(sniff); i++ {
		sniff = sniff[:len(sniff)-1]
	}
	return !utf8.Valid(sniff)
}
