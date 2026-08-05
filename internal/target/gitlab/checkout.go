package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// repoDirName returns a directory name that is stable across commits per
// (project, ref) — unlike the GitLab archive top level, which contains the SHA
// and changes with every commit (node_modules would then never carry over).
//
// The subPath deliberately does NOT go into the name. It used to, and that was
// the flaw: every partial checkout got a directory of its OWN, so five subPaths
// of one repository lay side by side as five stumps instead of forming one
// working tree. Since the error message for an oversized archive advises
// exactly that route ("use checkout with path"), the documented way out led to
// a checkout nothing could be built or tested in. Observed on a QA agent: 22
// partial checkouts in 14 minutes, afterwards a Laravel project hand-assembled
// in the home because no directory held a runnable one.
//
// Now the subPath is a path INSIDE this directory (see Checkout) — the parts
// grow into one tree.
//
// The ref is shortened to 48 characters; longer refs that agree in that prefix
// share a directory. A collision means one checkout overwrites the other, so
// the short hash of the full ref goes on the end and keeps them apart.
func repoDirName(projectID int, ref string) string {
	r := strings.Trim(refSanitize.ReplaceAllString(strings.TrimSpace(ref), "-"), "-")
	if r == "" {
		return fmt.Sprintf("p%d-default", projectID)
	}
	if len(r) > 48 {
		sum := sha256.Sum256([]byte(ref))
		r = strings.Trim(r[:48], "-") + "-" + hex.EncodeToString(sum[:3])
	}
	return fmt.Sprintf("p%d-%s", projectID, r)
}

// cleanSubPath normalizes the subdirectory the agent asks for and refuses
// anything that would lead out of the checkout. securePath checks the assembled
// path once more afterwards; this is the early, comprehensible error.
func cleanSubPath(subPath string) (string, error) {
	s := strings.Trim(strings.TrimSpace(subPath), "/")
	if s == "" {
		return "", nil
	}
	if filepath.IsAbs(s) {
		return "", fmt.Errorf("path must be relative to the repository root, was %q", subPath)
	}
	s = filepath.Clean(filepath.FromSlash(s))
	if s == ".." || strings.HasPrefix(s, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must not lead out of the repository, was %q", subPath)
	}
	return s, nil
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
	// Path is the repository ROOT in the sandbox — also for a partial checkout,
	// where the fetched subtree lies underneath it. It is the path that goes
	// into dev agent and into commit ("checkout_path"), because file lists there
	// are relative to the repository root.
	Path string `json:"path"`
	Ref  string `json:"ref,omitempty"`
	// SubPath is the requested subdirectory, LocalPath the place it landed
	// (Path/SubPath). Without a subPath the two are Path.
	SubPath   string `json:"sub_path,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Files     int    `json:"files"`
	Hint      string `json:"hint"`
}

// Checkout materialises a project's source code in the sandbox: it downloads
// the repository archive through the API (the brokered token stays in the
// daemon, it never lands in the file system — unlike with a git clone using a
// credential remote) and unpacks it under <workdir>/repos/.
//
// subPath narrows it to a subdirectory (a partial checkout for large repos) and
// lands UNDERNEATH the repository directory, at the place it occupies upstream.
// Several partial checkouts of the same ref therefore grow into one working
// tree instead of standing side by side as stumps. Only the fetched subtree is
// replaced on this route; what was fetched earlier stays. A full checkout
// replaces everything — the agent always works on the current code.
func Checkout(ctx context.Context, gc *Client, projectID int, ref, subPath, workdir string) (CheckoutResult, error) {
	if workdir == "" {
		return CheckoutResult{}, fmt.Errorf("checkout needs a sandbox (no working directory in the context)")
	}
	sub, err := cleanSubPath(subPath)
	if err != nil {
		return CheckoutResult{}, err
	}
	// The directory name arises from the project id and the ref, that is from
	// values the agent supplies. repoDirName lets no path separator through —
	// securePath pins that promise down explicitly on the finished path instead
	// of leaving it implicit two functions away. The archive entries below are
	// contained separately, by os.Root (see extractTarGzInto).
	rootDir, err := securePath(filepath.Join(workdir, "repos"), repoDirName(projectID, ref))
	if err != nil {
		return CheckoutResult{}, err
	}
	destDir := rootDir
	if sub != "" {
		if destDir, err = securePath(rootDir, sub); err != nil {
			return CheckoutResult{}, err
		}
	}

	body, err := gc.DownloadArchive(ctx, projectID, ref, subPath)
	if err != nil {
		return CheckoutResult{}, err
	}
	defer body.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return CheckoutResult{}, err
	}
	// Remove the old source code, leave the caches (preserveDirs) standing. With
	// a partial checkout this affects only the subtree being fetched.
	if err := pruneExceptPreserved(destDir); err != nil {
		return CheckoutResult{}, err
	}
	files, err := extractTarGzInto(body, destDir)
	if err != nil {
		return CheckoutResult{}, err
	}
	// The baseline belongs at the repository root, not into the subtree —
	// otherwise a partial checkout would leave a nested git repository behind
	// and commit would compare against the wrong root.
	initGitBaseline(ctx, rootDir)

	hint := "The source code is local — search and read it directly (Grep/Read/Bash). For the actual change hand the path to dev agent: the sub-agent works IN the project and gets that project's own rules there (CLAUDE.md, .claude/agents, skills) — you yourself do not see them. The directory is a git repo with the upstream state as its baseline commit; the sub-agent reports changed files back. Dependency caches (node_modules and the like) survive across runs, so npm/pip/go install then runs incrementally." +
		target.CheckoutPruneNote(target.PruneOldCheckouts(workdir, rootDir))
	if sub != "" {
		hint = "Partial checkout: \"path\" is the repository ROOT, the fetched subtree lies under it at " +
			filepath.ToSlash(sub) + ". Further partial checkouts of the same ref go into the same tree — fetch " +
			"everything the project needs to build BEFORE you start working, because every checkout redraws the " +
			"baseline commit and would swallow your changes. " + hint
	}
	return CheckoutResult{
		Path:      rootDir,
		Ref:       ref,
		SubPath:   sub,
		LocalPath: destDir,
		Files:     files,
		Hint:      hint,
	}, nil
}

// securePath anchors the checkout DIRECTORY under root: the assembled path must
// stay inside root. It is used for the one path built from agent input outside
// the archive — the destination directory name (see stableCheckoutDir) — and
// there a string comparison is enough, because nothing has been created yet
// that a symlink could point through.
//
// The archive entries themselves deliberately do NOT go through here; they are
// written through os.Root, which the file system enforces (see
// extractTarGzInto for why the difference matters).
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

// extractTarGzInto unpacks a GitLab repository archive into destDir and strips
// the top-level directory (the SHA-bearing shell) in doing so, so that the
// contents lie directly in destDir — the precondition for a stable,
// cache-preserving destination directory. It does NOT clear destDir (the caller
// prunes cache-sparingly).
//
// Every write goes through os.Root, and that is the point rather than a detail.
// A comparison of path STRINGS — the obvious way to write this, and how it was
// written first — checks one thing and then writes another: the check runs on
// the assembled path, the write resolves that path AGAIN through the file
// system and follows any symlink it meets. Between the two sits a window.
//
// The window is not theoretical here. pruneExceptPreserved deliberately keeps
// the dependency caches (node_modules, .venv, vendor …) across checkouts, and
// the agent runs `npm install` in that checkout as a matter of course — with
// whatever postinstall scripts the project's third-party dependencies bring
// along. A link left behind in node_modules is still there when the next
// archive is unpacked over it, and an entry "node_modules/x" then lands
// wherever the link points. The agent's home is a host directory mounted
// writable into the sandbox, so that is a write on the HOST, outside the home.
//
// os.Root closes it: the operating system resolves beneath destDir, and a
// symlink leading outside fails there — when opening, not in a check before it.
// The textual test for ".." and absolute paths stays, but as a clear early
// error rather than as the thing containment rests on. Same reasoning as
// internal/sandboxfs and internal/target/github.
func extractTarGzInto(r io.Reader, destDir string) (files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

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
		parts := strings.SplitN(name, string(filepath.Separator), 2)
		if len(parts) < 2 || parts[1] == "" {
			continue // the shell directory itself
		}
		rel := parts[1]
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return 0, unsafePath(hdr.Name, err)
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxBytes {
				return 0, fmt.Errorf("archive larger than %d MB — fetch the parts you need with checkout {\"path\":\"<subdirectory>\"}; they grow into ONE working tree, so fetch everything the project needs to build before you start. If you only want to read, list_tree and read_file do without a checkout entirely. The limit is set by COVEY_GITLAB_CHECKOUT_MAX_MB", maxBytes>>20)
			}
			if dir := filepath.Dir(rel); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return 0, unsafePath(hdr.Name, err)
				}
			}
			// A link that stays INSIDE the checkout is still followed here —
			// os.Root only refuses the ones leading out. That is deliberate:
			// everything inside the checkout is the agent's own working copy,
			// which it may write by any route it likes; the boundary being
			// defended is the one around it.
			f, err := root.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return 0, unsafePath(hdr.Name, err)
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

// unsafePath turns os.Root's refusal into this package's error. To the caller
// an escape blocked by the kernel is the same case as a textual "..": the entry
// does not belong in this checkout. The original is kept for the log — "not
// permitted" alone says nothing about which archive entry caused it.
func unsafePath(name string, err error) error {
	return fmt.Errorf("unsafe path in the archive: %q: %w", name, err)
}
