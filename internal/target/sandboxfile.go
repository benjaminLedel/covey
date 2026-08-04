package target

// Files a target system drops into the agent's sandbox: mail attachments,
// Teams attachments, GitLab uploads. Three plugins did the same thing in three
// copies — create the target folder, pin down the basename, write, build the
// hint text —, and all three silently overwrote a file of the same name.
//
// That is not a cosmetic flaw: two mails with a `rechnung.pdf` each ended up on
// the same path one after the other. An agent that had remembered the path then
// read the wrong document — without anything going visibly wrong anywhere.
// Besides, email and teams write into the same `attachments/` of the same
// sandbox, so the collision is not even confined to one target system.
//
// Here it stands once, collision-free, with one hint text for all.

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SandboxFile is a file stored in the sandbox — what the action returns to the
// agent as its result.
type SandboxFile struct {
	Path        string
	FileName    string
	ContentType string
	Bytes       int64
	Hint        string
}

// StoreFile writes data to <workdir>/<subfolder>/<name>.
//
// The name comes from outside (sender, foreign system) and is pinned down to
// its basename — otherwise an attachment named `../../.ssh/authorized_keys`
// would carry out of the sandbox. If the name is already taken, a counter is
// appended (`rechnung-2.pdf`); only for byte-identical content does the
// existing path stay, so that fetching the same attachment a second time
// creates no copy.
func StoreFile(workdir, subfolder, name string, data []byte, contentType string) (SandboxFile, error) {
	dir, fileName, err := prepareTarget(workdir, subfolder, name)
	if err != nil {
		return SandboxFile{}, err
	}
	path, err := freePath(dir, fileName, func(existing string) bool {
		info, err := os.Stat(existing)
		if err != nil || info.Size() != int64(len(data)) {
			return false // size differs — do not even read the content
		}
		alt, err := os.ReadFile(existing)
		return err == nil && bytes.Equal(alt, data)
	})
	if err != nil {
		return SandboxFile{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return SandboxFile{}, err
	}
	return SandboxFile{
		Path:        path,
		FileName:    filepath.Base(path),
		ContentType: contentType,
		Bytes:       int64(len(data)),
		Hint:        Hint(path, contentType),
	}, nil
}

// StoreStream writes from r without holding everything in memory and aborts
// when limit is exceeded. For sources that stream their content instead of
// buffering it beforehand (GitLab uploads).
//
// Unlike StoreFile this variant cannot detect identical content — for that
// it would have to read the stream in full first. A second fetch therefore
// creates a second file here instead of overwriting the first. That is the
// right order of evils: one copy too many is harmless, a silently replaced file
// is not.
func StoreStream(workdir, subfolder, name string, r io.Reader, limit int64, contentType string) (SandboxFile, error) {
	dir, fileName, err := prepareTarget(workdir, subfolder, name)
	if err != nil {
		return SandboxFile{}, err
	}
	path, err := freePath(dir, fileName, nil)
	if err != nil {
		return SandboxFile{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return SandboxFile{}, err
	}
	// Read one byte past the limit to detect an overrun reliably.
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	f.Close()
	if err != nil {
		os.Remove(path)
		return SandboxFile{}, err
	}
	if n > limit {
		os.Remove(path)
		return SandboxFile{}, fmt.Errorf("file larger than %d MB — aborted", limit>>20)
	}
	return SandboxFile{
		Path:        path,
		FileName:    filepath.Base(path),
		ContentType: contentType,
		Bytes:       n,
		Hint:        Hint(path, contentType),
	}, nil
}

// Hint is the sentence that tells the agent what it can do with the file.
// For images the pointer to the Read tool (vision), otherwise the general one.
func Hint(path, contentType string) string {
	if strings.HasPrefix(contentType, "image/") {
		return fmt.Sprintf("Image is stored locally at %s — look at it with the Read tool (vision).", path)
	}
	var ct string
	if contentType != "" {
		ct = " (content type " + contentType + ")"
	}
	return fmt.Sprintf("File is stored locally at %s%s. If it is an image, look at it with the Read tool (vision); otherwise open it appropriately.", path, ct)
}

// prepareTarget creates the target folder and hardens the file name.
func prepareTarget(workdir, subfolder, name string) (dir, fileName string, err error) {
	if workdir == "" {
		return "", "", fmt.Errorf("no working directory — the action needs a sandbox")
	}
	fileName = filepath.Base(strings.TrimSpace(name))
	// filepath.Base returns "." resp. "/" for empty and separator-only input.
	if fileName == "" || fileName == "." || fileName == ".." || fileName == string(filepath.Separator) {
		fileName = "attachment"
	}
	dir = filepath.Join(workdir, subfolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	return dir, fileName, nil
}

// freePath looks for a name that does not overwrite a foreign file.
// isSame may be nil; then every taken name counts as foreign.
func freePath(dir, name string, isSame func(path string) bool) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() && isSame != nil && isSame(path) {
			return path, nil // already there, byte-identical — no second copy
		}
		// An upper bound so that a bug in the caller does not fill up the file
		// system. Whoever has 1000 identically named attachments in one sandbox
		// has a different problem.
		if i > 999 {
			return "", fmt.Errorf("too many identically named files %q in %s", name, dir)
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
}

// MaxBytesFromEnv reads a size limit in MB from an environment variable.
//
// Values above maxMB are clamped to maxMB instead of silently falling back to
// the default: whoever enters 2048 obviously wants a lot and not the preset —
// an 80 times smaller limit without a word would be the unfriendliest of all
// answers. Unreadable or non-positive input stays at the default (fail-closed;
// an absurdly large value would overflow when converted to bytes and would
// defeat the very size check). Both cases say so in the log.
func MaxBytesFromEnv(name string, defaultMB, maxMB int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return defaultMB << 20
	}
	mb, err := strconv.ParseInt(v, 10, 64)
	if err != nil || mb <= 0 {
		slog.Warn("size limit unreadable — preset kept",
			"env", name, "value", v, "valid", fmt.Sprintf("1-%d", maxMB), "used_mb", defaultMB)
		return defaultMB << 20
	}
	if mb > maxMB {
		slog.Warn("size limit above the maximum — clamped",
			"env", name, "value", mb, "used_mb", maxMB)
		mb = maxMB
	}
	return mb << 20
}
