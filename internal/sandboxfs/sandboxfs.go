// Package sandboxfs opens an agent's persistent home as a file tree: browse,
// read, upload, modify, delete. It is the agent's workplace (spec/02) — compute
// is ephemeral, the home survives. Because it lives on the host and is merely
// mounted into the sandbox, access works regardless of whether the sandbox is
// currently running; a sleeping agent is not a blind spot.
//
// The entire surface is path handling on foreign input, which is why the whole
// package is built around one rule: **no path leads out of the root.** Every
// path is normalised (`..` falls away) and then checked against the deepest
// existing ancestor — a symlink pointing out of the home is an escape attempt,
// not a shortcut.
package sandboxfs

import (
	"archive/zip"
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
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: clean, Entries: []Entry{}}

	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		// Only the root may be missing: the home comes into being on first wake.
		if clean == "" {
			return out, nil
		}
		return Listing{}, ErrNotFound
	}
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDir
	}
	out.Exists = true

	des, err := os.ReadDir(abs)
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
		out.Entries = append(out.Entries, f.entry(abs, clean, de.Name()))
	}
	return out, nil
}

// entry builds the entry for a name; symlinks are shown as such (including
// their target and the hint whether they point out of the home).
func (f *FS) entry(dirAbs, dirRel, name string) Entry {
	full := filepath.Join(dirAbs, name)
	e := Entry{Name: name, Path: path.Join(dirRel, name)}

	li, err := os.Lstat(full)
	if err != nil {
		return e
	}
	if li.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(full); err == nil {
			e.Symlink = target
		}
		e.Outside = f.ensureInside(full) != nil
		// Look at the target for size/type — unless it points outward.
		if !e.Outside {
			if si, err := os.Stat(full); err == nil {
				li = si
			}
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
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	if info.IsDir() {
		return File{}, ErrIsDir
	}
	out := File{
		Path:    clean,
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

	fh, err := os.Open(abs)
	if err != nil {
		return File{}, err
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
func (f *FS) Open(rel string) (io.ReadCloser, os.FileInfo, error) {
	abs, _, err := f.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, ErrIsDir
	}
	fh, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	return fh, info, nil
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
	items []zipItem
}

// zipItem is one entry in the archive: source on disk, name inside the archive.
type zipItem struct {
	abs  string
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
	var plan ZipPlan
	seen := map[string]bool{}

	for _, rel := range rels {
		abs, clean, err := f.resolve(rel)
		if err != nil {
			return ZipPlan{}, err
		}
		info, err := os.Stat(abs)
		if errors.Is(err, os.ErrNotExist) {
			return ZipPlan{}, ErrNotFound
		}
		if err != nil {
			return ZipPlan{}, err
		}
		// The name inside the archive is relative to the parent of the chosen
		// path; for the root itself relative to the root.
		base := path.Dir(clean)
		if clean == "" || base == "." {
			base = ""
		}
		if err := f.walkZip(abs, clean, base, info, seen, &plan); err != nil {
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
func (f *FS) walkZip(abs, clean, base string, info os.FileInfo, seen map[string]bool, plan *ZipPlan) error {
	if seen[abs] {
		return nil // a duplicate selection (folder + a file within it) only once
	}
	seen[abs] = true

	name := clean
	if base != "" {
		name = strings.TrimPrefix(clean, base+"/")
	}
	if name == "" {
		name = path.Base(clean)
	}

	if !info.IsDir() {
		if plan.Files+1 > MaxZipFiles {
			return ErrTooMany
		}
		if plan.Bytes+info.Size() > MaxZipBytes {
			return ErrTooLarge
		}
		plan.items = append(plan.items, zipItem{abs: abs, name: name, size: info.Size(), mod: info.ModTime()})
		plan.Files++
		plan.Bytes += info.Size()
		return nil
	}

	plan.items = append(plan.items, zipItem{abs: abs, name: name + "/", dir: true, mod: info.ModTime()})
	des, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, de := range des {
		childAbs := filepath.Join(abs, de.Name())
		// A symlink pointing out of the home does not belong in the archive:
		// otherwise the download would pack up files of the host as well.
		if err := f.ensureInside(childAbs); err != nil {
			continue
		}
		childInfo, err := os.Stat(childAbs)
		if err != nil {
			continue // broken link or similar — skip, do not abort
		}
		if err := f.walkZip(childAbs, path.Join(clean, de.Name()), base, childInfo, seen, plan); err != nil {
			return err
		}
	}
	return nil
}

// WriteZip writes the planned archive as a stream.
func (f *FS) WriteZip(w io.Writer, plan ZipPlan) error {
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
		src, err := os.Open(it.abs)
		if err != nil {
			// Vanished between planning and writing — the agent keeps working.
			// No reason to drop the whole archive.
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
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Entry{}, err
	}
	if clean == "" {
		return Entry{}, ErrInvalidPath
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return Entry{}, ErrIsDir
	}
	if err := f.mkdirAll(filepath.Dir(abs)); err != nil {
		return Entry{}, err
	}

	// Write to a neighbouring file first, then rename: otherwise an aborted
	// upload leaves half a file under the right name — and the agent keeps
	// working with it.
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".covey-upload-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

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
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return Entry{}, err
	}
	f.chown(tmpName)
	if err := os.Rename(tmpName, abs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(abs), path.Dir(clean), path.Base(clean)), nil
}

// Mkdir creates a directory (including missing parents).
func (f *FS) Mkdir(rel string) (Entry, error) {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Entry{}, err
	}
	if clean == "" {
		return Entry{}, ErrInvalidPath
	}
	if _, err := os.Lstat(abs); err == nil {
		return Entry{}, ErrExists
	}
	if err := f.mkdirAll(abs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(abs), path.Dir(clean), path.Base(clean)), nil
}

// Remove deletes a file or a directory including its content. The root itself
// is off limits: one does not delete an agent's home from the file browser.
func (f *FS) Remove(rel string) error {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return err
	}
	if clean == "" {
		return ErrInvalidPath
	}
	if _, err := os.Lstat(abs); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return os.RemoveAll(abs)
}

// Move renames or moves. A taken target is not overwritten — a move that
// silently deletes something else is a trap.
func (f *FS) Move(fromRel, toRel string) (Entry, error) {
	fromAbs, fromClean, err := f.resolve(fromRel)
	if err != nil {
		return Entry{}, err
	}
	toAbs, toClean, err := f.resolve(toRel)
	if err != nil {
		return Entry{}, err
	}
	if fromClean == "" || toClean == "" {
		return Entry{}, ErrInvalidPath
	}
	if _, err := os.Lstat(fromAbs); errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return Entry{}, ErrExists
	}
	if err := f.mkdirAll(filepath.Dir(toAbs)); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(toAbs), path.Dir(toClean), path.Base(toClean)), nil
}

// mkdirAll creates the chain and adjusts the ownership on every newly created
// directory (MkdirAll alone would give it to the control plane).
func (f *FS) mkdirAll(abs string) error {
	if !strings.HasPrefix(abs, f.root) {
		return ErrInvalidPath
	}
	var missing []string
	for p := abs; len(p) >= len(f.root); p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		if p == f.root {
			break
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	for _, p := range missing {
		f.chown(p)
	}
	return nil
}

// chown adjusts the sandbox ownership — best effort: as an ordinary user (local
// development) it fails, and there it does not matter either.
func (f *FS) chown(abs string) {
	if f.UID < 0 || f.GID < 0 {
		return
	}
	_ = os.Chown(abs, f.UID, f.GID)
}

// resolve normalises a path coming from the client and makes sure it stays
// inside the home. Returns: absolute host path and the cleaned relative path
// ("" = root).
func (f *FS) resolve(rel string) (abs, clean string, err error) {
	rel = strings.TrimSpace(rel)
	if strings.ContainsRune(rel, 0) {
		return "", "", ErrInvalidPath
	}
	// Taking the detour via "/" makes every `..` fall away, a leading one too.
	clean = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	if clean == "." {
		clean = ""
	}
	abs = f.root
	if clean != "" {
		abs = filepath.Join(f.root, filepath.FromSlash(clean))
	}
	if err := f.ensureInside(abs); err != nil {
		return "", "", err
	}
	return abs, clean, nil
}

// ensureInside checks the path against symlink escapes: the deepest *existing*
// ancestor is resolved and must lie inside the root. Trailing pieces that do
// not exist yet cannot be links, so that is enough.
func (f *FS) ensureInside(abs string) error {
	rootReal, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		// Home not created yet: then there is no link anyone could escape
		// through either — the purely textual comparison suffices.
		if errors.Is(err, os.ErrNotExist) {
			if within(f.root, abs) {
				return nil
			}
			return ErrInvalidPath
		}
		return err
	}
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !within(rootReal, real) {
				return ErrInvalidPath
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return ErrInvalidPath
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ErrInvalidPath
		}
		probe = parent
	}
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
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
