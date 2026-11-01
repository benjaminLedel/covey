package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Reading skills from an archive or a directory tree.
//
// The Agent Skills format is a directory, and that is how skills are passed
// around: a folder in a Git repository, a zip. One archive may hold one skill
// (`my-skill/SKILL.md`, or `SKILL.md` at the top) or many (a repository with
// `skills/*/SKILL.md`). Every directory holding a SKILL.md is a skill; a
// SKILL.md further down inside a skill belongs to that skill's files.
//
// Nothing here writes. The result is a list of candidates with their problems,
// so that a preview can show all of them and an import can refuse the whole
// archive when one does not hold up — half an imported repository is worse
// than none.

const (
	// maxArchiveBytes bounds what is downloaded or uploaded. A repository
	// archive carries far more than its skills; this leaves room for that
	// without letting one request fill the control plane's memory.
	maxArchiveBytes = 64 << 20
	// maxArchiveRead bounds what is decompressed across the whole archive —
	// the defence against a zip bomb, beside the per-file limit.
	maxArchiveRead = 128 << 20
	// maxArchiveSkills bounds the candidates of one archive.
	maxArchiveSkills = 200
)

// MaxArchiveBytes is the upload limit the HTTP layer enforces.
const MaxArchiveBytes = maxArchiveBytes

// Candidate is one skill found in an archive or a directory.
type Candidate struct {
	// Dir is the skill's directory inside the archive ("" = the top level).
	Dir  string
	Spec Spec
	// Problems say why the skill cannot be stored as it is. Empty = it can.
	Problems []string
	// Skipped names entries left out on purpose (symbolic links, OS litter).
	Skipped []string
}

// entry is one file of an archive or directory tree, read lazily.
type entry struct {
	path    string
	size    int64
	symlink bool
	open    func() (io.ReadCloser, error)
}

// FromZip reads the skills of a zip archive. subdir, when set, keeps only the
// skills below that path — it is matched after the archive's single top-level
// directory, the one GitHub and GitLab put around a repository.
func FromZip(data []byte, subdir string) ([]Candidate, error) {
	if len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("%w: archive too large (%d bytes, max. %d)", ErrInvalid, len(data), maxArchiveBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a zip archive: %v", ErrInvalid, err)
	}
	entries := make([]entry, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		f := f
		entries = append(entries, entry{
			path:    f.Name,
			size:    int64(f.UncompressedSize64),
			symlink: f.Mode()&fs.ModeSymlink != 0,
			open:    func() (io.ReadCloser, error) { return f.Open() },
		})
	}
	return fromEntries(entries, subdir)
}

// FromFS reads the skills of a directory tree — what `covey skills validate`
// looks at. Symbolic links are skipped, as in an archive: following one would
// read files from outside the tree.
func FromFS(fsys fs.FS) ([]Candidate, error) {
	var entries []entry
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		p2 := p
		entries = append(entries, entry{
			path:    p2,
			size:    info.Size(),
			symlink: d.Type()&fs.ModeSymlink != 0,
			open:    func() (io.ReadCloser, error) { return fsys.Open(p2) },
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fromEntries(entries, "")
}

func fromEntries(all []entry, subdir string) ([]Candidate, error) {
	subdir = strings.Trim(path.Clean("/"+subdir), "/")
	var entries []entry
	for _, e := range all {
		e.path = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(e.path, `\`, "/")), "/")
		if e.path == "" || litter(e.path) {
			continue
		}
		entries = append(entries, e)
	}
	if subdir != "" {
		strip := commonTop(entries)
		var kept []entry
		for _, e := range entries {
			rel := strings.TrimPrefix(e.path, strip)
			if rel == subdir || strings.HasPrefix(rel, subdir+"/") {
				kept = append(kept, e)
			}
		}
		entries = kept
	}

	// Every directory with a SKILL.md is a skill root; one inside another
	// root is part of the outer skill.
	var roots []string
	for _, e := range entries {
		if path.Base(e.path) == EntryFile {
			roots = append(roots, dirOf(e.path))
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		di, dj := strings.Count(roots[i], "/"), strings.Count(roots[j], "/")
		if roots[i] == "" || roots[j] == "" {
			return roots[i] == ""
		}
		if di != dj {
			return di < dj
		}
		return roots[i] < roots[j]
	})
	var outer []string
	for _, r := range roots {
		nested := false
		for _, o := range outer {
			if o == "" || strings.HasPrefix(r, o+"/") {
				nested = true
				break
			}
		}
		if !nested {
			outer = append(outer, r)
		}
	}
	if len(outer) == 0 {
		where := ""
		if subdir != "" {
			where = " below " + subdir
		}
		return nil, fmt.Errorf("%w: no %s found%s — a skill is a directory holding a %s", ErrInvalid, EntryFile, where, EntryFile)
	}
	if len(outer) > maxArchiveSkills {
		return nil, fmt.Errorf("%w: too many skills in one archive (%d, max. %d)", ErrInvalid, len(outer), maxArchiveSkills)
	}
	sort.Strings(outer)

	budget := int64(maxArchiveRead)
	out := make([]Candidate, 0, len(outer))
	for _, root := range outer {
		c, err := readCandidate(root, entries, &budget)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func readCandidate(root string, entries []entry, budget *int64) (Candidate, error) {
	c := Candidate{Dir: root}
	prefix := ""
	if root != "" {
		prefix = root + "/"
	}
	var files []File
	for _, e := range entries {
		if !strings.HasPrefix(e.path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(e.path, prefix)
		if e.symlink {
			c.Skipped = append(c.Skipped, rel+" (symbolic link)")
			continue
		}
		if len(files) >= maxFiles {
			c.Problems = append(c.Problems, fmt.Sprintf("too many files (more than %d)", maxFiles))
			break
		}
		if e.size > maxFileBytes {
			c.Problems = append(c.Problems, fmt.Sprintf("file %q too large (%d bytes, max. %d)", rel, e.size, maxFileBytes))
			continue
		}
		data, err := readLimited(e, budget)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return Candidate{}, err
			}
			c.Problems = append(c.Problems, fmt.Sprintf("file %q: %v", rel, err))
			continue
		}
		files = append(files, FileOf(rel, data))
	}

	spec, err := Assemble(Front{}, nil, files)
	if err != nil {
		c.Problems = append(c.Problems, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
		spec = Spec{Files: files}
	}
	// The specification: the name must match the directory. covey's name IS
	// the directory, so a mismatch would be resolved silently one way or the
	// other — it is reported instead.
	if root != "" {
		dir := path.Base(root)
		if spec.Name != "" && spec.Name != dir {
			c.Problems = append(c.Problems, fmt.Sprintf("frontmatter name %q does not match directory %q", spec.Name, dir))
		}
		if spec.Name == "" {
			spec.Name = dir
		}
	}
	c.Spec = spec
	c.Problems = append(c.Problems, Check(spec)...)
	c.Problems = dedupe(c.Problems)
	return c, nil
}

func readLimited(e entry, budget *int64) ([]byte, error) {
	rc, err := e.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("too large (max. %d bytes)", maxFileBytes)
	}
	*budget -= int64(len(data))
	if *budget < 0 {
		return nil, fmt.Errorf("%w: archive decompresses to more than %d bytes", ErrInvalid, maxArchiveRead)
	}
	return data, nil
}

// litter is what operating systems and version control leave in archives.
func litter(p string) bool {
	if strings.HasPrefix(p, "__MACOSX/") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".git" || seg == ".DS_Store" || seg == "Thumbs.db" {
			return true
		}
	}
	return false
}

// commonTop returns "top/" when every entry sits below one directory.
func commonTop(entries []entry) string {
	top := ""
	for _, e := range entries {
		i := strings.IndexByte(e.path, '/')
		if i < 0 {
			return ""
		}
		if top == "" {
			top = e.path[:i+1]
		} else if e.path[:i+1] != top {
			return ""
		}
	}
	return top
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
