package teams

import (
	"context"
	"fmt"
	"strings"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// DownloadResult is the answer of the download_attachment action: where the
// file sits in the sandbox and how the agent looks at it.
type DownloadResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// DownloadAttachmentToSandbox brokers a Teams attachment into the sandbox —
// the connector token stays in the daemon, the file lands under
// <workdir>/attachments/. The agent then reads it with the read tool (images
// via vision, otherwise as a file). name is optional; if absent, the basename
// is derived from the URL.
func DownloadAttachmentToSandbox(ctx context.Context, c *Client, downloadURL, name, workdir string) (DownloadResult, error) {
	if workdir == "" {
		return DownloadResult{}, fmt.Errorf("download_attachment needs a sandbox (no working directory in the context)")
	}
	limit := maxAttachmentBytes()
	contentType, data, err := c.DownloadAttachment(ctx, downloadURL, limit)
	if err != nil {
		return DownloadResult{}, err
	}
	if int64(len(data)) > limit {
		return DownloadResult{}, fmt.Errorf("attachment larger than %d MB — aborted", limit>>20)
	}

	filename := strings.TrimSpace(name)
	if filename == "" {
		filename = Attachment{ContentURL: downloadURL}.Filename()
	}

	// Storing, name hardening and collision protection are done by the shared
	// helper (internal/target/sandboxdatei.go). Important right here: teams and
	// email share `attachments/` inside the same sandbox.
	file, err := target.StoreFile(workdir, "attachments", filename, data, contentType)
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{
		Path:        file.Path,
		Filename:    file.FileName,
		ContentType: file.ContentType,
		Bytes:       file.Bytes,
		Hint:        file.Hint,
	}, nil
}
