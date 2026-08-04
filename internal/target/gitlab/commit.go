package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Size limits for the commit action: a single file and the sum of all files of
// one commit — the commits API carries contents in the JSON body, huge commits
// do not belong on this route.
const (
	maxCommitFileBytes  = 4 << 20  // 4 MB per file
	maxCommitTotalBytes = 16 << 20 // 16 MB per commit
)

// CommitResult is the answer of the commit action: what was pushed and how
// things continue (opening a merge request).
type CommitResult struct {
	Branch        string   `json:"branch"`
	BranchCreated bool     `json:"branch_created"`
	Commit        Commit   `json:"commit"`
	Files         []string `json:"files"`
	Deleted       []string `json:"deleted,omitempty"`
	Hint          string   `json:"hint"`
}

// CommitFromCheckout pushes locally edited files from the sandbox checkout as
// one commit onto a feature branch — through the commits API, so that the
// brokered token stays in the daemon (no git remote with credentials in the
// sandbox). If the branch does not exist yet, it is branched off the start
// branch (default: the project's default branch). Direct commits onto the
// default branch are forbidden fail-closed — the route into the main branch
// leads exclusively through a merge request.
func CommitFromCheckout(ctx context.Context, gc *Client, projectID int, branch, startBranch, message, checkoutPath string, files, deleted []string, workdir string) (CommitResult, error) {
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

	proj, err := gc.GetProject(ctx, projectID)
	if err != nil {
		return CommitResult{}, err
	}
	if proj.DefaultBranch != "" && branch == proj.DefaultBranch {
		return CommitResult{}, fmt.Errorf("a direct commit onto the default branch %q is not allowed — work on a feature branch and open a merge request (create_merge_request)", branch)
	}

	// The checkout path must lie inside the sandbox — the action reads files
	// from the daemon's file system and may only see what the checkout
	// materialised there.
	root, err := filepath.Abs(filepath.Clean(checkoutPath))
	if err != nil || checkoutPath == "" {
		return CommitResult{}, fmt.Errorf("checkout_path missing or invalid — use the path from the checkout result")
	}
	absWork, err := filepath.Abs(workdir)
	if err != nil {
		return CommitResult{}, err
	}
	if root != absWork && !strings.HasPrefix(root, absWork+string(filepath.Separator)) {
		return CommitResult{}, fmt.Errorf("checkout_path %q lies outside the sandbox", checkoutPath)
	}

	// Branch already there? Then we commit on top of it (no start_branch);
	// otherwise the commits API branches it off the start branch.
	if startBranch = strings.TrimSpace(startBranch); startBranch == "" {
		startBranch = proj.DefaultBranch
	}
	branchExists := false
	if bs, err := gc.ListBranches(ctx, projectID, branch); err == nil {
		for _, b := range bs {
			if b.Name == branch {
				branchExists = true
				break
			}
		}
	}
	baseRef := startBranch
	if branchExists {
		baseRef = branch
	}

	var actions []CommitAction
	var total int64
	for _, f := range files {
		rel, err := repoRelPath(f)
		if err != nil {
			return CommitResult{}, err
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return CommitResult{}, fmt.Errorf("read file %q in the checkout: %w", rel, err)
		}
		if len(data) > maxCommitFileBytes {
			return CommitResult{}, fmt.Errorf("file %q is larger than %d MB — such files do not belong on this commit route", rel, maxCommitFileBytes>>20)
		}
		if total += int64(len(data)); total > maxCommitTotalBytes {
			return CommitResult{}, fmt.Errorf("commit larger than %d MB — split the change into several commits", maxCommitTotalBytes>>20)
		}
		act := "create"
		if exists, err := gc.FileExists(ctx, projectID, rel, baseRef); err == nil && exists {
			act = "update"
		}
		actions = append(actions, CommitAction{
			Action:   act,
			FilePath: filepath.ToSlash(rel),
			Content:  base64.StdEncoding.EncodeToString(data),
			Encoding: "base64",
		})
	}
	for _, f := range deleted {
		rel, err := repoRelPath(f)
		if err != nil {
			return CommitResult{}, err
		}
		actions = append(actions, CommitAction{Action: "delete", FilePath: filepath.ToSlash(rel)})
	}

	start := ""
	if !branchExists {
		start = startBranch
	}
	commit, err := gc.CommitFiles(ctx, projectID, branch, start, message, actions)
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{
		Branch:        branch,
		BranchCreated: !branchExists,
		Commit:        commit,
		Files:         files,
		Deleted:       deleted,
		Hint:          fmt.Sprintf("Pushed onto branch %q. Now open the merge request: create_merge_request {\"project_id\":%d,\"source_branch\":%q,...} — the reviewer is your manager.", branch, projectID, branch),
	}, nil
}

// repoRelPath checks a file path supplied by the agent: repo-relative, no
// absolute path and no traversal upwards — it is used both locally (reading
// from the checkout) and remotely (file_path in the commit).
func repoRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	clean := filepath.Clean(filepath.FromSlash(p))
	if p == "" || clean == "." || filepath.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid file path %q — a repo-relative path is expected", p)
	}
	return clean, nil
}
