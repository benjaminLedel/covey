package sandboxfs

import (
	"io"
	"unicode/utf8"
)

// Tree is an agent's home as the interface sees it — the seam between the file
// browser and where the files actually lie.
//
// It exists because a home no longer has to be a directory next to the control
// plane. With a runner on another host it lives there, and the way to it is
// home_op over the runner link; when that host is offline, the last snapshot in
// the home store is what can still be read. Three places, one interface — the
// HTTP handlers know none of the difference.
//
// The reasoning why file access deliberately does NOT go through the daemon
// protocol is unchanged and only becomes clearer here: the home has to be
// readable while the sandbox is asleep, and asleep is the normal state
// (spec/16, "File access").
type Tree interface {
	List(rel string) (Listing, error)
	Read(rel string) (File, error)
	Open(rel string) (io.ReadCloser, FileInfo, error)
	PlanZip(rels []string) (ZipPlan, error)
	WriteZip(w io.Writer, plan ZipPlan) error
	Write(rel string, r io.Reader) (Entry, error)
	Mkdir(rel string) (Entry, error)
	Remove(rel string) error
	Move(fromRel, toRel string) (Entry, error)
	Usage() Usage
}

// FileInfo is what the streaming endpoints need of a file: its size for the
// Content-Length, its name for the download, its modification time for the
// caching headers. Deliberately not os.FileInfo — over the runner link there is
// no os.FileInfo on the other side, and the three fields are all that is used.
type FileInfo struct {
	Name    string
	Size    int64
	ModTime string
}

// ReadOnlyError says that this home can be looked at but not changed. That is
// the state of a home whose runner is offline: it is readable from its last
// snapshot, and writing into a snapshot would produce a state nobody can
// reconcile with the working copy that is coming back.
type ReadOnlyError struct{ Reason string }

func (e *ReadOnlyError) Error() string { return e.Reason }

// Describe turns a file's particulars and its (already read) beginning into
// what the browser shows. It exists so that a home read out of a snapshot
// arrives at the same File as one read from disk: the preview decision, the
// binary test and the truncation are one rule, not two that drift apart.
func Describe(relPath string, size int64, mode, modTime string, data []byte) File {
	out := File{
		Path: relPath, Size: size, Mode: mode, ModTime: modTime,
		Preview: PreviewKind(relPath),
	}
	if out.Preview == PreviewImage || out.Preview == PreviewPDF {
		out.Binary = true
		return out
	}
	if len(data) > MaxViewBytes {
		data = data[:MaxViewBytes]
		out.Truncated = true
	}
	if isBinary(data) {
		out.Binary = true
		out.Preview = PreviewBinary
		return out
	}
	// Truncation happens at byte level; half a UTF-8 character at the end
	// would come through as a replacement character, so drop it.
	if out.Truncated {
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	if out.Preview == "" {
		out.Preview = PreviewText
	}
	out.Content = string(data)
	return out
}

// ZipName is the suggested archive name for a selection.
func ZipName(rels []string) string { return zipName(rels) }

// MaxReadBytes is how much of a file the viewer ever needs — one byte more
// than it shows, so that "truncated" can be decided rather than guessed.
const MaxReadBytes = MaxViewBytes + 1
