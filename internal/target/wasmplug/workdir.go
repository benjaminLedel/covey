package wasmplug

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

// The second thing a module may ask the host for, after a request: a file out
// of the agent's workspace.
//
// It exists because of what a plugin is for. Judging declared dependencies
// means reading the lock file the project actually has; the same shape covers
// every plugin that answers a question about a checkout rather than about a
// remote system. Without it, that whole class of plugin can only be compiled
// into the binary — which is the thing the catalogue exists to avoid.
//
// The confinement is not a check on the path string, it is os.Root: the host
// opens the workspace once and resolves inside it, so "..", an absolute path
// and a symlink pointing out of the tree all fail at the syscall rather than at
// a comparison somebody has to get right. What the module gets is a file it
// named inside a directory it never learns the location of.

const (
	// maxFileSize caps one read. A package-lock.json of a large monorepo runs
	// into the low double-digit megabytes; past this it is not a lock file any
	// more, and the module has 64 MiB of memory to hold whatever it gets.
	maxFileSize = 16 << 20
	// maxReads caps how many files one invocation may look at. A module that
	// tries three lock-file names is normal; one that walks a tree is not doing
	// what it declared.
	maxReads = 64
)

// FileReader serves a module's read_file. nil at the call site means there is
// no workspace — the honest answer for anything the control plane invokes.
type FileReader func(ctx context.Context, req ReadFileRequest) ReadFileResponse

// workdirReader hands out the files of dir, and nothing else.
func workdirReader(dir string) FileReader {
	return func(_ context.Context, req ReadFileRequest) ReadFileResponse {
		name := strings.TrimSpace(req.Path)
		if name == "" {
			return ReadFileResponse{Error: "read_file: no path given"}
		}
		// path.Clean before the open so that a plugin's "./foo" and "a/../foo"
		// are one file in the message the operator reads, not three.
		name = path.Clean(name)

		root, err := os.OpenRoot(dir)
		if err != nil {
			return ReadFileResponse{Error: "read_file: no workspace available"}
		}
		defer root.Close()

		f, err := root.Open(name)
		if err != nil {
			// Deliberately not the underlying error: it carries the absolute
			// path of the workspace, and the module has no business learning
			// where on the host it lives.
			if os.IsNotExist(err) {
				return ReadFileResponse{Error: fmt.Sprintf("read_file: %s does not exist", name)}
			}
			return ReadFileResponse{Error: fmt.Sprintf("read_file: %s is not readable inside the workspace", name)}
		}
		defer f.Close()

		if st, err := f.Stat(); err == nil {
			if st.IsDir() {
				return ReadFileResponse{Error: fmt.Sprintf("read_file: %s is a directory", name)}
			}
			if st.Size() > maxFileSize {
				return ReadFileResponse{Error: fmt.Sprintf(
					"read_file: %s is %d MiB — more than the %d MiB a plugin may read at once",
					name, st.Size()>>20, maxFileSize>>20)}
			}
		}
		// LimitReader as well as the Stat check: a named pipe or a file that
		// grows between the two reports no size worth trusting.
		raw, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
		if err != nil {
			return ReadFileResponse{Error: fmt.Sprintf("read_file: %s could not be read", name)}
		}
		if len(raw) > maxFileSize {
			return ReadFileResponse{Error: fmt.Sprintf(
				"read_file: %s is larger than the %d MiB a plugin may read at once", name, maxFileSize>>20)}
		}
		if !utf8.Valid(raw) {
			return ReadFileResponse{Error: fmt.Sprintf("read_file: %s is not text", name)}
		}
		return ReadFileResponse{Text: string(raw)}
	}
}
