package github

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"covey/internal/reqlog"
	"covey/internal/target"
)

// maxAttachmentBytes caps a single downloaded attachment — a screenshot is
// small, an accidentally linked huge asset should not flood the sandbox.
const maxAttachmentBytes = 25 << 20 // 25 MB

// DownloadAttachmentResult is the answer of the download_attachment action:
// where the image lies in the sandbox and how the agent looks at it.
type DownloadAttachmentResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// attachmentHosts is the allowlist of hosts an attachment may be fetched from.
// It is the point that matters about this action: the URL comes out of an issue
// body, that is out of text a STRANGER wrote. Without the list the action would
// be a request forgery primitive — "download this" pointed at an internal
// address, carried out by the daemon with a valid token. The list holds exactly
// the places GitHub itself stores attachments.
var attachmentHosts = map[string]bool{
	"github.com":                                true, // /user-attachments/assets/<uuid>
	"www.github.com":                            true,
	"objects.githubusercontent.com":             true,
	"raw.githubusercontent.com":                 true,
	"user-images.githubusercontent.com":         true,
	"private-user-images.githubusercontent.com": true,
}

// checkAttachmentURL parses the reference from the issue text and lets only
// https to a known GitHub host through.
func checkAttachmentURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("url %q invalid: %w", raw, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("url %q is not https — only GitHub attachment links are downloaded", raw)
	}
	host := strings.ToLower(u.Hostname())
	if !attachmentHosts[host] {
		return nil, fmt.Errorf("host %q is not a GitHub attachment host — the action downloads attachments from GitHub only, not arbitrary addresses", host)
	}
	if (host == "github.com" || host == "www.github.com") && !strings.HasPrefix(u.Path, "/user-attachments/") {
		return nil, fmt.Errorf("url %q is not an attachment link — expected https://github.com/user-attachments/assets/<id>", raw)
	}
	return u, nil
}

// DownloadAttachmentToSandbox fetches an image attached to an issue/PR into the
// sandbox in brokered fashion — the token stays in the daemon, the file lands
// under <workdir>/uploads/. The agent then reads it with the Read tool (vision)
// and can actually look at the screenshot.
//
// GitHub redirects attachment links to signed storage URLs. Go drops the
// Authorization header on the host change, which is right: the redirect target
// carries its own signature and would refuse a foreign token.
func DownloadAttachmentToSandbox(ctx context.Context, gc *Client, rawURL, workdir string) (DownloadAttachmentResult, error) {
	if workdir == "" {
		return DownloadAttachmentResult{}, fmt.Errorf("download_attachment needs a sandbox (no working directory in the context)")
	}
	u, err := checkAttachmentURL(rawURL)
	if err != nil {
		return DownloadAttachmentResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return DownloadAttachmentResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+gc.Token)
	req.Header.Set("Accept", "*/*")
	resp, err := reqlog.Client("github", 60*time.Second).Do(req)
	if err != nil {
		return DownloadAttachmentResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return DownloadAttachmentResult{}, fmt.Errorf("github GET %s: HTTP %d: %.200s", u.Host+u.Path, resp.StatusCode, body)
	}

	// Storing, name hardening, the limit and collision protection are done by
	// the shared helper (internal/target/sandboxfile.go).
	file, err := target.StoreStream(workdir, "uploads", attachmentName(u, resp),
		resp.Body, maxAttachmentBytes, resp.Header.Get("Content-Type"))
	if err != nil {
		return DownloadAttachmentResult{}, err
	}
	return DownloadAttachmentResult{
		Path:        file.Path,
		Filename:    file.FileName,
		ContentType: file.ContentType,
		Bytes:       file.Bytes,
		Hint:        file.Hint,
	}, nil
}

// attachmentName finds a file name. GitHub's asset links carry a bare UUID in
// the path, so the Content-Disposition header is asked first — it is the only
// place the original name survives.
func attachmentName(u *url.URL, resp *http.Response) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := strings.TrimSpace(params["filename"]); name != "" {
				return name
			}
		}
	}
	if base := path.Base(u.Path); base != "" && base != "/" && base != "." {
		return base
	}
	return "attachment"
}
