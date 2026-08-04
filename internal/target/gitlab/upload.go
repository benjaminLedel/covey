package gitlab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"covey/internal/target"
)

// DownloadUploadResult is the answer of the download_upload action: where the
// image lies in the sandbox and how the agent looks at it.
type DownloadUploadResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// DownloadUploadToSandbox fetches an upload attached to an issue/MR (a
// screenshot) into the sandbox in brokered fashion — the token stays in the
// daemon, the file lands under <workdir>/uploads/. The agent then reads it with
// the Read tool (vision) and can actually look at the screenshot. ref is the
// reference from the issue description: "/uploads/<secret>/<file>.png", the full
// web URL or already "<secret>/<file>".
func DownloadUploadToSandbox(ctx context.Context, gc *Client, projectID int, ref, workdir string) (DownloadUploadResult, error) {
	if workdir == "" {
		return DownloadUploadResult{}, fmt.Errorf("download_upload needs a sandbox (no working directory in the context)")
	}
	name, contentType, body, err := gc.DownloadUpload(ctx, projectID, ref)
	if err != nil {
		return DownloadUploadResult{}, err
	}
	defer body.Close()

	// Storing, name hardening, the limit and collision protection are done by
	// the shared helper (internal/target/sandboxdatei.go) — here in the stream
	// variant, because the upload does not lie in memory beforehand.
	file, err := target.StoreStream(workdir, "uploads", name, body, maxUploadBytes, contentType)
	if err != nil {
		return DownloadUploadResult{}, err
	}
	return DownloadUploadResult{
		Path:        file.Path,
		Filename:    file.FileName,
		ContentType: file.ContentType,
		Bytes:       file.Bytes,
		Hint:        file.Hint,
	}, nil
}

// UploadResultOut is the answer of the upload action: the Markdown reference
// for embedding in a comment_mr body plus the relative URL.
type UploadResultOut struct {
	Markdown string `json:"markdown"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Bytes    int    `json:"bytes"`
	Hint     string `json:"hint"`
}

// UploadFromSandbox uploads a file from the sandbox (e.g. a browser screenshot)
// in brokered fashion to a GitLab project and returns the Markdown reference
// the agent embeds in comment_mr. The path is resolved safely against the
// working directory — no escape via ".." or an absolute path.
func UploadFromSandbox(ctx context.Context, gc *Client, projectID int, path, workdir string) (UploadResultOut, error) {
	if workdir == "" {
		return UploadResultOut{}, fmt.Errorf("upload needs a sandbox (no working directory in the context)")
	}
	if projectID == 0 {
		return UploadResultOut{}, fmt.Errorf("project_id missing")
	}
	local, err := resolveInWorkdir(workdir, path)
	if err != nil {
		return UploadResultOut{}, err
	}
	data, err := os.ReadFile(local)
	if err != nil {
		return UploadResultOut{}, fmt.Errorf("read file: %w", err)
	}
	if len(data) > maxUploadBytes {
		return UploadResultOut{}, fmt.Errorf("file larger than %d MB — aborted", maxUploadBytes>>20)
	}
	res, err := gc.UploadFile(ctx, projectID, filepath.Base(local), data)
	if err != nil {
		return UploadResultOut{}, err
	}
	md := res.Markdown
	if md == "" {
		md = fmt.Sprintf("![%s](%s)", filepath.Base(local), res.URL)
	}
	return UploadResultOut{
		Markdown: md,
		URL:      res.URL,
		Filename: filepath.Base(local),
		Bytes:    len(data),
		Hint:     "Embed this Markdown reference in the comment_mr body so that the screenshot appears directly in the MR.",
	}, nil
}

// resolveInWorkdir resolves a sandbox path safely against the working
// directory — no escape via ".." or an absolute path outside it.
func resolveInWorkdir(workdir, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path missing")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q lies outside the sandbox working directory", p)
	}
	return resolved, nil
}
