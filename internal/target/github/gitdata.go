package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The Git Data API — GitHub's low-level layer over the object database. It is
// what the commit action is built on, and the reason is the shape of the
// alternative: the contents API writes ONE file per commit, so a change across
// five files would arrive as five commits and every intermediate state would be
// a broken tree in the history. Git Data lets us assemble blobs, one tree and
// one commit and move the branch exactly once.
//
// The route from a checkout to a pushed commit:
//
//	 1. refSHA        — where does the branch stand (or the start branch)?
//	 2. commitTreeSHA — which tree does that commit hang off?
//	 3. createBlob    — one per changed file
//	 4. createTree    — base_tree + the changed entries (deletion = sha:null)
//	 5. createCommit  — message, tree, parent
//	 6. updateRef     — move the branch (or create it)
//
// The whole route runs in the daemon with the brokered token; the sandbox never
// sees a git remote carrying credentials.

// blobMode is the file mode of an ordinary file in a git tree. Executables
// (100755) and symlinks (120000) are deliberately not written: the commit
// action carries source changes, and a mode the agent could set would be a
// permission decision nobody reviewed.
const blobMode = "100644"

// refSHA returns the commit a branch points at. found=false means the branch
// does not exist (HTTP 404) — that is a normal case, not an error: the first
// commit onto a feature branch creates it.
func (c *Client) refSHA(ctx context.Context, repo, branch string) (sha string, found bool, err error) {
	p, err := repoPath(repo, "/git/ref/heads/"+escapePath(branch))
	if err != nil {
		return "", false, err
	}
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return "", false, nil
		}
		return "", false, err
	}
	return out.Object.SHA, out.Object.SHA != "", nil
}

// commitTreeSHA returns the tree a commit hangs off — the base for the new tree.
func (c *Client) commitTreeSHA(ctx context.Context, repo, commitSHA string) (string, error) {
	p, err := repoPath(repo, "/git/commits/"+url.PathEscape(commitSHA))
	if err != nil {
		return "", err
	}
	var out struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return "", err
	}
	return out.Tree.SHA, nil
}

// createBlob stores a file's content as a git object and returns its SHA.
// base64, so that binary files (an image in the docs, say) survive.
func (c *Client) createBlob(ctx context.Context, repo string, data []byte) (string, error) {
	p, err := repoPath(repo, "/git/blobs")
	if err != nil {
		return "", err
	}
	var out struct {
		SHA string `json:"sha"`
	}
	err = c.do(ctx, http.MethodPost, p, map[string]any{
		"content":  base64.StdEncoding.EncodeToString(data),
		"encoding": "base64",
	}, &out)
	return out.SHA, err
}

// treeEntry is one line of the new tree. SHA is a pointer because a DELETION is
// expressed as an explicit JSON null — an omitted field would mean "unchanged"
// instead, and the file would quietly survive the commit.
type treeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  *string `json:"sha"`
}

// createTree writes a new tree on top of baseTree.
func (c *Client) createTree(ctx context.Context, repo, baseTree string, entries []treeEntry) (string, error) {
	p, err := repoPath(repo, "/git/trees")
	if err != nil {
		return "", err
	}
	var out struct {
		SHA string `json:"sha"`
	}
	err = c.do(ctx, http.MethodPost, p, map[string]any{
		"base_tree": baseTree,
		"tree":      entries,
	}, &out)
	return out.SHA, err
}

// createCommit writes the commit object. parent may be empty — then it is a
// root commit, which cannot happen on this route (a checkout always has a
// base), but the API allows it and the caller need not special-case it.
func (c *Client) createCommit(ctx context.Context, repo, message, treeSHA, parent string) (Commit, error) {
	p, err := repoPath(repo, "/git/commits")
	if err != nil {
		return Commit{}, err
	}
	in := map[string]any{"message": message, "tree": treeSHA}
	if parent != "" {
		in["parents"] = []string{parent}
	}
	var out Commit
	err = c.do(ctx, http.MethodPost, p, in, &out)
	return out, err
}

// updateRef moves a branch onto a commit, or creates it if it does not exist
// yet. Deliberately WITHOUT force: a push that would need to overwrite history
// means someone else has pushed in the meantime, and the agent has to see that
// instead of erasing it.
func (c *Client) updateRef(ctx context.Context, repo, branch, sha string, create bool) error {
	if create {
		p, err := repoPath(repo, "/git/refs")
		if err != nil {
			return err
		}
		return c.do(ctx, http.MethodPost, p, map[string]any{
			"ref": "refs/heads/" + branch,
			"sha": sha,
		}, nil)
	}
	p, err := repoPath(repo, "/git/refs/heads/"+escapePath(branch))
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPatch, p, map[string]any{"sha": sha, "force": false}, nil); err != nil {
		if strings.Contains(err.Error(), "HTTP 422") {
			return fmt.Errorf("branch %q has moved on since your checkout — fetch it again with checkout (ref=%s), work your change in afresh and commit again: %w", branch, branch, err)
		}
		return err
	}
	return nil
}
