package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"covey/internal/target"
)

// preserveDirs survive a repeated checkout (they are not wiped away together
// with the source code): dependency and build caches that would otherwise have
// to be rebuilt on every run. That way a follow-up checkout on the same ref is
// incremental (npm/pip/go find their cache) instead of cold.
var preserveDirs = map[string]bool{
	"node_modules": true, ".venv": true, "venv": true, "vendor": true,
	"target": true, ".gradle": true, ".next": true, ".cache": true,
	".pnpm-store": true, ".yarn": true,
}

var refSanitize = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// stableCheckoutDir returns a directory name that is stable across commits per
// (project, ref, subPath) — unlike the GitLab archive top level, which contains
// the SHA and changes with every commit (node_modules would then never carry
// over).
func stableCheckoutDir(projectID int, ref, subPath string) string {
	slug := func(s string) string {
		s = strings.Trim(refSanitize.ReplaceAllString(strings.TrimSpace(s), "-"), "-")
		if len(s) > 48 {
			s = strings.Trim(s[:48], "-")
		}
		return s
	}
	r := slug(ref)
	if r == "" {
		r = "default"
	}
	name := fmt.Sprintf("p%d-%s", projectID, r)
	if sp := slug(subPath); sp != "" {
		name += "-" + sp
	}
	return name
}

// pruneExceptPreserved empties a directory but leaves the cache directories
// from preserveDirs standing — that way the source code is replaced and the
// cache stays.
func pruneExceptPreserved(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() && preserveDirs[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// checkoutMaxBytes caps the unpacked total size of a checkout — protection of
// the sandbox against huge repos and zip bombs. Default 512 MB, overridable
// via COVEY_GITLAB_CHECKOUT_MAX_MB (the daemon's process env).
func checkoutMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_GITLAB_CHECKOUT_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 512 << 20
}

// CheckoutResult is the answer of the checkout action to the agent: where the
// code lies and how it goes on working with it.
type CheckoutResult struct {
	Path    string `json:"path"`
	Ref     string `json:"ref,omitempty"`
	SubPath string `json:"sub_path,omitempty"`
	Files   int    `json:"files"`
	Hint    string `json:"hint"`
}

// Checkout materialises a project's source code in the sandbox: it downloads
// the repository archive through the API (the brokered token stays in the
// daemon, it never lands in the file system — unlike with a git clone using a
// credential remote) and unpacks it under <workdir>/repos/. subPath narrows it
// to a subdirectory (a partial checkout for large repos). An existing state of
// the same archive is replaced — the agent always works on the current code.
func Checkout(ctx context.Context, gc *Client, projectID int, ref, subPath, workdir string) (CheckoutResult, error) {
	if workdir == "" {
		return CheckoutResult{}, fmt.Errorf("checkout needs a sandbox (no working directory in the context)")
	}
	body, err := gc.DownloadArchive(ctx, projectID, ref, subPath)
	if err != nil {
		return CheckoutResult{}, err
	}
	defer body.Close()

	// The directory name arises from the project id and the ref, that is from
	// values the agent supplies. stableCheckoutDir lets no path separator
	// through — securePath pins that promise down explicitly on the finished
	// path instead of leaving it implicit two functions away. Everything below
	// (unpacking, the git baseline) relies on it.
	destDir, err := securePath(filepath.Join(workdir, "repos"), stableCheckoutDir(projectID, ref, subPath))
	if err != nil {
		return CheckoutResult{}, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return CheckoutResult{}, err
	}
	// Remove the old source code, leave the caches (preserveDirs) standing.
	if err := pruneExceptPreserved(destDir); err != nil {
		return CheckoutResult{}, err
	}
	files, err := extractTarGzInto(body, destDir)
	if err != nil {
		return CheckoutResult{}, err
	}
	initGitBaseline(ctx, destDir)
	return CheckoutResult{
		Path:    destDir,
		Ref:     ref,
		SubPath: subPath,
		Files:   files,
		Hint:    "The source code is local — search and read it directly (Grep/Read/Bash). For the actual change hand the path to dev agent: the sub-agent works IN the project and gets that project's own rules there (CLAUDE.md, .claude/agents, skills) — you yourself do not see them. The directory is a git repo with the upstream state as its baseline commit; the sub-agent reports changed files back. Dependency caches (node_modules and the like) survive across runs, so npm/pip/go install then runs incrementally.",
	}, nil
}

// securePath anchors an archive entry under root: the assembled destination
// path must stay inside root. The prefix test for ".." on the name already
// catches the regular case beforehand — this check is the promise on the
// FINISHED path and therefore the one that matters (zip slip, CWE-22).
func securePath(root, name string) (string, error) {
	dest := filepath.Clean(filepath.Join(root, name))
	if dest != filepath.Clean(root) && !strings.HasPrefix(dest, filepath.Clean(root)+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path in the archive: %q", name)
	}
	return dest, nil
}

// initGitBaseline creates a git repository with exactly one commit in the fresh
// checkout: the upstream state just unpacked. The archive itself brings no .git
// along (it is a tarball, not a clone), and from that follow two problems this
// commit solves:
//
//   - Tools and scripts of the project that call git would otherwise fail.
//   - After the work in the checkout there would be no way to say WHAT was
//     changed — but the commit action needs exactly that file list.
//
// The baseline is drawn anew on every checkout: `.git` deliberately is NOT in
// preserveDirs, so pruneExceptPreserved clears it away before unpacking. Only
// that way does it match the fresh upstream state exactly, and `git status`
// afterwards shows nothing but one's own work.
//
// Everything here is best effort — if git is missing or a step fails, the
// checkout stays valid; the agent only loses the convenience.
func initGitBaseline(ctx context.Context, dir string) {
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		// The identity as a flag instead of via git config: we neither touch the
		// sandbox user's global configuration nor do we need a real one.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Covey", "GIT_AUTHOR_EMAIL=covey@localhost",
			"GIT_COMMITTER_NAME=Covey", "GIT_COMMITTER_EMAIL=covey@localhost")
		return cmd.Run()
	}
	if err := git("init", "-q"); err != nil {
		return
	}
	// Dependency and build caches (preserveDirs) survive the checkout and are
	// not part of the work. Through info/exclude they stay out of the baseline
	// and out of a later `git status` without touching the project's .gitignore.
	var excl strings.Builder
	for name := range preserveDirs {
		excl.WriteString("/" + name + "\n")
	}
	_ = os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(excl.String()), 0o644)
	if err := git("add", "-A"); err != nil {
		return
	}
	if err := git("commit", "-q", "--allow-empty", "-m", "covey baseline"); err != nil {
		return
	}
	// The tag makes the upstream state referenceable (target.BaselineRef): the
	// sub-run reports its work as the difference to this commit. That way what
	// the sub-agent committed locally counts too — a comparison of two
	// `git status` snapshots would see nothing after that. `-f`, because a
	// repeated checkout into the same directory has to reset the tag.
	_ = git("tag", "-f", target.BaselineRef)
}

// extractTarGz unpacks a GitLab repository archive into destRoot and returns
// the name of the top-level directory (GitLab: <project>-<ref>-<sha>).
// Security: paths are checked against traversal, symlinks are skipped, the
// unpacked total size is capped.
func extractTarGz(r io.Reader, destRoot string) (topDir string, files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", 0, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	maxBytes := checkoutMaxBytes()
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("read archive: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "pax_global_header" {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return "", 0, fmt.Errorf("unsafe path in the archive: %q", hdr.Name)
		}
		if topDir == "" {
			topDir = strings.SplitN(name, string(filepath.Separator), 2)[0]
			// Replace the previous state of the same archive (a fresh checkout).
			old, err := securePath(destRoot, topDir)
			if err != nil {
				return "", 0, err
			}
			if err := os.RemoveAll(old); err != nil {
				return "", 0, err
			}
		}
		dest, err := securePath(destRoot, name)
		if err != nil {
			return "", 0, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", 0, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxBytes {
				return "", 0, fmt.Errorf("archive larger than %d MB — use checkout with \"path\" (a subdirectory) or navigate with list_tree and read selectively via read_file; the limit is set by COVEY_GITLAB_CHECKOUT_MAX_MB", maxBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", 0, err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return "", 0, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxBytes)); err != nil {
				f.Close()
				return "", 0, err
			}
			f.Close()
			files++
		default:
			// Symlinks & the rest: unnecessary for reading code, risky as an
			// escape vector — deliberately skipped.
		}
	}
	if topDir == "" {
		return "", 0, fmt.Errorf("empty archive")
	}
	return topDir, files, nil
}

// extractTarGzInto unpacks a GitLab repository archive into destDir and strips
// the top-level directory (the SHA-bearing shell) in doing so, so that the
// contents lie directly in destDir — the precondition for a stable,
// cache-preserving destination directory. Unlike extractTarGz it does NOT clear
// destDir (the caller prunes cache-sparingly). Security as in extractTarGz:
// traversal protection, symlinks skipped, total size capped.
func extractTarGzInto(r io.Reader, destDir string) (files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	maxBytes := checkoutMaxBytes()
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read archive: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "pax_global_header" {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return 0, fmt.Errorf("unsafe path in the archive: %q", hdr.Name)
		}
		// Strip the top-level directory (projname-ref-sha).
		rel := strings.SplitN(name, string(filepath.Separator), 2)
		if len(rel) < 2 || rel[1] == "" {
			continue // the shell directory itself
		}
		dest, err := securePath(destDir, rel[1])
		if err != nil {
			return 0, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return 0, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxBytes {
				return 0, fmt.Errorf("archive larger than %d MB — use checkout with \"path\" (a subdirectory) or navigate with list_tree and read selectively via read_file; the limit is set by COVEY_GITLAB_CHECKOUT_MAX_MB", maxBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return 0, err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return 0, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxBytes)); err != nil {
				f.Close()
				return 0, err
			}
			f.Close()
			files++
		default:
			// Symlinks & the rest deliberately skipped.
		}
	}
	return files, nil
}
