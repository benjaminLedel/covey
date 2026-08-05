package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Size limits for the commit action: a single file and the sum of all files of
// one commit. The blobs travel through the JSON API base64-encoded; huge
// commits do not belong on this route.
const (
	maxCommitFileBytes  = 4 << 20  // 4 MB per file
	maxCommitTotalBytes = 16 << 20 // 16 MB per commit
)

// CommitResult is the answer of the commit action: what was pushed and how
// things continue (opening a pull request).
type CommitResult struct {
	Repo          string   `json:"repo"`
	Branch        string   `json:"branch"`
	BranchCreated bool     `json:"branch_created"`
	Commit        Commit   `json:"commit"`
	Files         []string `json:"files"`
	Deleted       []string `json:"deleted,omitempty"`
	Hint          string   `json:"hint"`
}

// CommitFromCheckout pushes locally edited files from the sandbox checkout as
// ONE commit onto a feature branch — through the Git Data API (see gitdata.go),
// so that the brokered token stays in the daemon (no git remote with
// credentials in the sandbox). If the branch does not exist yet, it is branched
// off the start branch (default: the repository's default branch). Direct
// commits onto the default branch are forbidden fail-closed — the route into
// the main branch leads exclusively through a pull request.
func CommitFromCheckout(ctx context.Context, gc *Client, repo, branch, startBranch, message, checkoutPath string, files, deleted []string, workdir string) (CommitResult, error) {
	if workdir == "" {
		return CommitResult{}, fmt.Errorf("commit needs a sandbox (no working directory in the context)")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.TrimSpace(message) == "" {
		return CommitResult{}, fmt.Errorf("branch or message missing")
	}
	if len(files)+len(deleted) == 0 {
		return CommitResult{}, fmt.Errorf("files (and/or deleted) missing — nothing to commit")
	}

	r, err := gc.GetRepo(ctx, repo)
	if err != nil {
		return CommitResult{}, err
	}
	if r.DefaultBranch != "" && branch == r.DefaultBranch {
		return CommitResult{}, fmt.Errorf("a direct commit onto the default branch %q is not allowed — work on a feature branch and open a pull request (create_pull_request)", branch)
	}

	root, err := checkoutRoot(checkoutPath, workdir)
	if err != nil {
		return CommitResult{}, err
	}

	// Where does the new commit hang? If the branch is already there, on top of
	// it; otherwise it is branched off the start branch.
	if startBranch = strings.TrimSpace(startBranch); startBranch == "" {
		startBranch = r.DefaultBranch
	}
	parent, branchExists, err := gc.refSHA(ctx, repo, branch)
	if err != nil {
		return CommitResult{}, err
	}
	if !branchExists {
		base, found, err := gc.refSHA(ctx, repo, startBranch)
		if err != nil {
			return CommitResult{}, err
		}
		if !found {
			return CommitResult{}, fmt.Errorf("start branch %q does not exist — check list_branches", startBranch)
		}
		parent = base
	}
	baseTree, err := gc.commitTreeSHA(ctx, repo, parent)
	if err != nil {
		return CommitResult{}, err
	}

	entries, err := treeEntriesFromCheckout(ctx, gc, repo, root, files, deleted)
	if err != nil {
		return CommitResult{}, err
	}
	treeSHA, err := gc.createTree(ctx, repo, baseTree, entries)
	if err != nil {
		return CommitResult{}, err
	}
	commit, err := gc.createCommit(ctx, repo, message, treeSHA, parent)
	if err != nil {
		return CommitResult{}, err
	}
	if err := gc.updateRef(ctx, repo, branch, commit.SHA, !branchExists); err != nil {
		return CommitResult{}, err
	}

	return CommitResult{
		Repo:          repo,
		Branch:        branch,
		BranchCreated: !branchExists,
		Commit:        commit,
		Files:         files,
		Deleted:       deleted,
		Hint: fmt.Sprintf("Pushed onto branch %q. Now open the pull request: create_pull_request {\"repo\":%q,\"head\":%q,...} and check afterwards with list_workflow_runs whether the CI goes green.",
			branch, repo, branch),
	}, nil
}

// checkoutRoot resolves the path the agent passed and pins it inside the
// sandbox: the action reads files from the daemon's file system and may only
// see what the checkout materialised there.
func checkoutRoot(checkoutPath, workdir string) (string, error) {
	if strings.TrimSpace(checkoutPath) == "" {
		return "", fmt.Errorf("checkout_path missing — use the path from the checkout result")
	}
	root, err := filepath.Abs(filepath.Clean(checkoutPath))
	if err != nil {
		return "", fmt.Errorf("checkout_path invalid — use the path from the checkout result")
	}
	absWork, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if root != absWork && !strings.HasPrefix(root, absWork+string(filepath.Separator)) {
		return "", fmt.Errorf("checkout_path %q lies outside the sandbox", checkoutPath)
	}
	return root, nil
}

// treeEntriesFromCheckout reads the changed files out of the checkout, stores
// them as blobs and builds the tree entries. A deletion is an explicit null SHA
// (see treeEntry) — an omitted entry would mean "unchanged".
func treeEntriesFromCheckout(ctx context.Context, gc *Client, repo, root string, files, deleted []string) ([]treeEntry, error) {
	var entries []treeEntry
	var total int64
	for _, f := range files {
		rel, err := repoRelPath(f)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("read file %q in the checkout: %w", rel, err)
		}
		if len(data) > maxCommitFileBytes {
			return nil, fmt.Errorf("file %q is larger than %d MB — such files do not belong on this commit route", rel, maxCommitFileBytes>>20)
		}
		if total += int64(len(data)); total > maxCommitTotalBytes {
			return nil, fmt.Errorf("commit larger than %d MB — split the change into several commits", maxCommitTotalBytes>>20)
		}
		sha, err := gc.createBlob(ctx, repo, data)
		if err != nil {
			return nil, err
		}
		blobSHA := sha
		entries = append(entries, treeEntry{
			Path: filepath.ToSlash(rel), Mode: blobMode, Type: "blob", SHA: &blobSHA,
		})
	}
	for _, f := range deleted {
		rel, err := repoRelPath(f)
		if err != nil {
			return nil, err
		}
		entries = append(entries, treeEntry{
			Path: filepath.ToSlash(rel), Mode: blobMode, Type: "blob", SHA: nil,
		})
	}
	return entries, nil
}

// repoRelPath checks a file path supplied by the agent: repo-relative, no
// absolute path and no traversal upwards — it is used both locally (reading
// from the checkout) and remotely (the path in the tree).
func repoRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	clean := filepath.Clean(filepath.FromSlash(p))
	if p == "" || clean == "." || filepath.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid file path %q — a repo-relative path is expected", p)
	}
	return clean, nil
}
