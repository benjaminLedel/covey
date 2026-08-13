package github

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

	"github.com/benjaminLedel/covey-plugin-sdk/target"
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

var nameSanitize = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// stableCheckoutDir returns a directory name that is stable across commits per
// (repo, ref) — unlike GitHub's archive top level, which contains the SHA and
// changes with every commit (node_modules would then never carry over).
func stableCheckoutDir(repo, ref string) string {
	slug := func(s string) string {
		s = strings.Trim(nameSanitize.ReplaceAllString(strings.TrimSpace(s), "-"), "-")
		if len(s) > 48 {
			s = strings.Trim(s[:48], "-")
		}
		return s
	}
	name := slug(repo)
	if name == "" {
		name = "repo"
	}
	if r := slug(ref); r != "" {
		name += "-" + r
	} else {
		name += "-default"
	}
	return name
}

// checkoutMaxBytes caps the unpacked total size of a checkout — protection of
// the sandbox against huge repos and zip bombs. Default 512 MB, overridable via
// COVEY_GITHUB_CHECKOUT_MAX_MB (the daemon's process env).
func checkoutMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_GITHUB_CHECKOUT_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 512 << 20
}

// CheckoutResult is the answer of the checkout action to the agent: where the
// code lies and how it goes on working with it.
type CheckoutResult struct {
	Path  string `json:"path"`
	Repo  string `json:"repo"`
	Ref   string `json:"ref,omitempty"`
	Files int    `json:"files"`
	Hint  string `json:"hint"`
}

// Checkout materialises a repository's source in the sandbox: it downloads the
// archive through the API (the brokered token stays in the daemon, it never
// lands in the file system — unlike with a git clone using a credential remote)
// and unpacks it under <workdir>/repos/. An existing state of the same
// repository/ref is replaced — the agent always works on the current code.
//
// Unlike GitLab, GitHub's archive endpoint knows no sub-path: the tarball
// always carries the whole repository. A repo too large for the sandbox is
// therefore not narrowed here but read selectively through list_tree/read_file;
// the error message says so.
func Checkout(ctx context.Context, gc *Client, repo, ref, workdir string) (CheckoutResult, error) {
	if workdir == "" {
		return CheckoutResult{}, fmt.Errorf("checkout needs a sandbox (no working directory in the context)")
	}
	if _, _, err := splitRepo(repo); err != nil {
		return CheckoutResult{}, err
	}
	body, err := gc.DownloadTarball(ctx, repo, ref)
	if err != nil {
		return CheckoutResult{}, err
	}
	defer body.Close()

	// The directory name arises from values the agent supplies.
	// stableCheckoutDir lets no path separator through — securePath pins that
	// promise down explicitly on the finished path instead of leaving it
	// implicit two functions away. Everything below (unpacking, the git
	// baseline) relies on it.
	destDir, err := securePath(filepath.Join(workdir, "repos"), stableCheckoutDir(repo, ref))
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
		Path:  destDir,
		Repo:  repo,
		Ref:   ref,
		Files: files,
		Hint: "The source code is local — search and read it directly (Grep/Read/Bash). For the actual change hand the path to dev agent: the sub-agent works IN the project and gets that project's own rules there (CLAUDE.md, .claude/agents, skills), which you yourself do not see. The directory is a git repo with the upstream state as its baseline commit; the sub-agent reports changed files back. Dependency caches (node_modules and the like) survive across runs, so npm/pip/go install then runs incrementally." +
			target.CheckoutPruneNote(target.PruneOldCheckouts(workdir, destDir)),
	}, nil
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
		// The identity as a flag instead of via git config: we neither touch
		// the sandbox user's global configuration nor do we need a real one.
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
	// and out of a later `git status` without touching the project's
	// .gitignore.
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

// extractTarGzInto unpacks a GitHub repository archive into destDir and strips
// the top-level directory (GitHub: <owner>-<repo>-<sha>) in doing so, so that
// the contents lie directly in destDir — the precondition for a stable,
// cache-preserving destination directory.
//
// Every write goes through os.Root, and that is the point rather than a detail.
// A comparison of path STRINGS — the obvious way to write this, and how it was
// written first — checks one thing and then writes another: the check runs on
// the assembled path, the write resolves that path AGAIN through the file
// system and follows any symlink it meets on the way. Between the two sits a
// window.
//
// The window is not theoretical here. pruneExceptPreserved deliberately keeps
// the dependency caches (node_modules, .venv, vendor …) across checkouts, and
// the agent runs `npm install` in that checkout as a matter of course — with
// whatever postinstall scripts the project's third-party dependencies bring
// along. A link left behind in node_modules is still there when the next
// archive is unpacked over it, and an entry "node_modules/x" then lands
// wherever the link points. The agent's home is a host directory mounted into
// the sandbox, so that is a write on the HOST, outside the home.
//
// os.Root closes it: the operating system resolves beneath destDir, and a
// symlink leading outside fails there — when opening, not in a check before it.
// The textual test for ".." and absolute paths stays, but as a clear early
// error rather than as the thing containment rests on.
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
		// Strip the top-level directory (owner-repo-sha).
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
				return 0, fmt.Errorf("archive larger than %d MB — GitHub's archive endpoint cannot narrow to a subdirectory, so navigate with list_tree and read selectively via read_file; the limit is set by COVEY_GITHUB_CHECKOUT_MAX_MB", maxBytes>>20)
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
			// Symlinks & the rest: unnecessary for reading code, risky as an
			// escape vector — deliberately skipped.
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
