package homestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manifest is one snapshot of a home: which file lies where, and which blocks
// it is made of. The manifest itself is stored as a block, so a snapshot is a
// single hash.
type Manifest struct {
	Entries []Entry `json:"entries"`
}

// Entry is one path in the home. Exactly one of Blocks (a regular file),
// Dir or Link is meaningful.
type Entry struct {
	Path string      `json:"path"`
	Mode fs.FileMode `json:"mode"`
	Size int64       `json:"size,omitempty"`
	Dir  bool        `json:"dir,omitempty"`
	// Link is the target of a symlink. Symlinks travel as what they are and
	// are not followed: an agent's home is full of them (node_modules, SDK
	// links), and resolving them would copy gigabytes into the snapshot that
	// the link itself describes in forty bytes.
	Link string `json:"link,omitempty"`
	// Blocks are the file's content, in order. One entry for a whole file,
	// several for a large one (see chunkSize).
	Blocks []string `json:"blocks,omitempty"`
}

// Chunking: whole files up to wholeFileLimit, fixed-size blocks above it.
//
// A home consists overwhelmingly of many small files — package caches,
// node_modules, SDK trees. For those the hash of the whole file collects
// practically the entire dedup benefit: they are identical or they are new,
// and a file that changes at all is usually rewritten wholesale by the tool
// that owns it. Chunking everything would pay a rolling hash over gigabytes of
// small files for a few per cent.
//
// Above the limit fixed-size blocks apply, and the reason they are enough here
// is what large files in a home actually are: append-only JSONL transcripts and
// archives. An append leaves every preceding block byte-identical, because the
// offsets do not shift — which is exactly what content-defined chunking is
// otherwise needed for. It only wins on insertion in the middle of a
// multi-gigabyte file, and that is not a case a home produces.
const (
	wholeFileLimit = 8 << 20 // 8 MiB
	chunkSize      = 4 << 20 // 4 MiB
)

// Hash is the block key: SHA-256, hex. It is not a security boundary but an
// identity — two blocks with the same hash are the same content, and that is
// what makes the sharing between agents safe by construction (spec/16).
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Encode serialises a manifest for storage.
func (m Manifest) Encode() ([]byte, error) { return json.Marshal(m) }

// DecodeManifest reads one back.
func DecodeManifest(raw []byte) (Manifest, error) {
	var m Manifest
	err := json.Unmarshal(raw, &m)
	return m, err
}

// TotalSize is what the home occupies as the agent sees it.
func (m Manifest) TotalSize() int64 {
	var n int64
	for _, e := range m.Entries {
		n += e.Size
	}
	return n
}

// BlockSet is every block this snapshot references — the basis of the garbage
// collection, which may only remove what no remaining snapshot needs.
func (m Manifest) BlockSet() map[string]bool {
	out := map[string]bool{}
	for _, e := range m.Entries {
		for _, b := range e.Blocks {
			out[b] = true
		}
	}
	return out
}

// Excludes decides what is left out of the sync. The role of this list is the
// point: before, its completeness was a prerequisite for correctness and a
// forgotten path meant data loss. Now it is a cost question (spec/16) — which
// is why a considered default is worth having (config.DefaultHomeExcludes).
//
// Three kinds of pattern, because the paths worth leaving out come in three
// shapes:
//
//	repos/scratch    a path from the root of the home, with everything under it
//	__pycache__      a NAME, wherever it sits — scrap does not grow at the top
//	*.pyc            a glob on the file name, at any depth
//
// Only the first kind existed, and it was the one that fit the fewest cases:
// __pycache__ lies deep inside a project, never beside it.
type Excludes []string

func (e Excludes) skip(rel string) bool {
	if len(e) == 0 {
		return false
	}
	base := filepath.Base(rel)
	segmente := strings.Split(filepath.ToSlash(rel), "/")
	for _, pattern := range e {
		pattern = strings.Trim(strings.TrimSpace(pattern), "/")
		if pattern == "" {
			continue
		}
		switch {
		case strings.ContainsAny(pattern, "*?["):
			// Ein Muster auf dem Dateinamen, in jeder Tiefe. Ein kaputtes
			// Muster (filepath.Match meldet einen Fehler) schließt nichts aus:
			// im Zweifel sichern.
			if ok, err := filepath.Match(pattern, base); err == nil && ok {
				return true
			}
		case strings.Contains(pattern, "/"):
			// Ein Pfad ab der Wurzel des Homes, mit allem darunter.
			if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
				return true
			}
		default:
			// Ein Name, wo immer er steht — und alles darunter, weil ein
			// ausgeschlossenes Verzeichnis seinen Inhalt mitnimmt.
			for _, seg := range segmente {
				if seg == pattern {
					return true
				}
			}
		}
	}
	return false
}

// Scan walks a home and produces its manifest, handing every block it comes
// across to put. put is called only for content that is actually new — whether
// that is so is decided by the caller, which is the only one that knows the
// store.
func Scan(root string, excludes Excludes, put func(hash string, data []byte) error) (Manifest, error) {
	var m Manifest
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that disappeared under us is not a reason to lose the
			// whole snapshot: a home is a workplace, and something is always
			// being written in it. What is gone is gone; the rest is worth
			// keeping.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if excludes.skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		switch {
		case d.IsDir():
			m.Entries = append(m.Entries, Entry{Path: rel, Mode: info.Mode().Perm(), Dir: true})
			return nil
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return nil
			}
			m.Entries = append(m.Entries, Entry{Path: rel, Mode: info.Mode().Perm(), Link: target})
			return nil
		case !info.Mode().IsRegular():
			// Sockets, devices, fifos: a home has no business carrying them,
			// and restoring one would be a surprise rather than a service.
			return nil
		}

		blocks, err := blocksOf(path, info.Size(), put)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		m.Entries = append(m.Entries, Entry{
			Path: rel, Mode: info.Mode().Perm(), Size: info.Size(), Blocks: blocks,
		})
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	// A stable order makes two manifests of the same content identical — and
	// therefore the same block. Without it the manifest would be new after
	// every run even when nothing changed.
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	return m, nil
}

func blocksOf(path string, size int64, put func(hash string, data []byte) error) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if size <= wholeFileLimit {
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		h := Hash(data)
		if err := put(h, data); err != nil {
			return nil, err
		}
		return []string{h}, nil
	}

	var out []string
	buf := make([]byte, chunkSize)
	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			chunk := buf[:n]
			h := Hash(chunk)
			if err := put(h, chunk); err != nil {
				return nil, err
			}
			out = append(out, h)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DirUsage is one directory of a home with what it holds.
type DirUsage struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// TopDirs answers "why is this home so big?" without shell access — and
// reveals the candidates for an exclusion. Top level only: it is the level at
// which the answer is actionable, and walking deeper would produce a list
// nobody reads.
func (m Manifest) TopDirs(limit int) []DirUsage {
	byDir := map[string]*DirUsage{}
	for _, e := range m.Entries {
		if e.Dir || e.Size == 0 {
			continue
		}
		top, _, _ := strings.Cut(e.Path, "/")
		d := byDir[top]
		if d == nil {
			d = &DirUsage{Path: top}
			byDir[top] = d
		}
		d.Bytes += e.Size
		d.Files++
	}
	out := make([]DirUsage, 0, len(byDir))
	for _, d := range byDir {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ExclusiveBytes is what only this home holds — the figure that actually says
// something. A 7 GB home of which 200 MB are exclusive is one whose loss costs
// time; one that is exclusive throughout is one whose loss costs work.
//
// Attributed per file and not per block: a block carries no size of its own in
// the manifest, and a file all of whose blocks are shared is shared. The
// approximation errs on the small side for large chunked files that share some
// of their blocks — and understating what is exclusive is the safer direction
// for a figure people use to judge a risk.
func (m Manifest) ExclusiveBytes(shared map[string]bool) int64 {
	var out int64
	for _, e := range m.Entries {
		if e.Dir || len(e.Blocks) == 0 {
			continue
		}
		exclusive := false
		for _, b := range e.Blocks {
			if !shared[b] {
				exclusive = true
				break
			}
		}
		if exclusive {
			out += e.Size
		}
	}
	return out
}
