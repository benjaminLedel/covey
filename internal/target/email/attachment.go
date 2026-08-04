package email

import (
	"fmt"
	"strings"

	"covey/internal/target"
)

// AttachmentResult is the answer of the get_attachment action: where the
// attachment lies in the sandbox and how the agent looks at it.
type AttachmentResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// getAttachmentToSandbox fetches a mail attachment brokered into the sandbox —
// the mailbox credentials stay in the daemon, the file ends up under
// <workdir>/attachments/. The agent then reads it with the Read tool (images by
// vision, everything else as a file). get_message supplies the attachment names.
func getAttachmentToSandbox(cfg Config, mailbox string, uid uint32, name, workdir string) (AttachmentResult, error) {
	if workdir == "" {
		return AttachmentResult{}, fmt.Errorf("get_attachment needs a sandbox (no working directory in the context)")
	}
	if strings.TrimSpace(name) == "" {
		return AttachmentResult{}, fmt.Errorf("name missing")
	}
	limit := maxAttachmentBytes()
	fname, contentType, data, err := getAttachment(cfg, mailbox, uid, name, limit)
	if err != nil {
		return AttachmentResult{}, err
	}

	// Storing the file, hardening the name and collision protection is the job
	// of the shared helper (internal/target/sandboxdatei.go) — teams and gitlab
	// write through the same path, and email and teams even share
	// `attachments/`.
	file, err := target.StoreFile(workdir, "attachments", fname, data, contentType)
	if err != nil {
		return AttachmentResult{}, err
	}
	return AttachmentResult{
		Path:        file.Path,
		Filename:    file.FileName,
		ContentType: file.ContentType,
		Bytes:       file.Bytes,
		Hint:        file.Hint,
	}, nil
}
