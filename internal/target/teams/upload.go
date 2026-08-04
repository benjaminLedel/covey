package teams

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// In Teams, outgoing files go through the file consent flow (spec/15): the
// agent asks for consent with a card (send_file), the recipient clicks
// "accept", Teams delivers an upload URL in an invoke activity, and the agent
// pushes the bytes there (upload_file). Both halves run in the action proxy,
// i.e. in the sandbox — the file stays in the agent's persistent home and
// needs no intermediate storage anywhere.

// SendFileResult is the answer to send_file: what was requested and what
// happens next.
type SendFileResult struct {
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
	Hint     string `json:"hint"`
}

// UploadFileResult is the answer to upload_file.
type UploadFileResult struct {
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
	Hint     string `json:"hint"`
}

// resolveInWorkdir resolves a path named by the agent inside its working
// directory. Absolute paths and `..` do not lead out of it — an agent must not
// be able to send /etc/passwd to its chat partner by accident (or at the
// prompting of a manipulated source).
func resolveInWorkdir(workdir, path string) (string, error) {
	if workdir == "" {
		return "", fmt.Errorf("no working directory in the context (the action needs a sandbox)")
	}
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "", fmt.Errorf("required field path missing")
	}
	base, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	full := clean
	if !filepath.IsAbs(full) {
		full = filepath.Join(base, clean)
	}
	full = filepath.Clean(full)
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path lies outside the working directory: %s", path)
	}
	return full, nil
}

// readForUpload reads the file and checks its size against the same limit as
// incoming attachments — fail-closed, before anything goes to Teams.
func readForUpload(workdir, path string) (string, []byte, error) {
	full, err := resolveInWorkdir(workdir, path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", nil, fmt.Errorf("file not readable: %w", err)
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("%s is a directory", path)
	}
	limit := maxAttachmentBytes()
	if info.Size() > limit {
		return "", nil, fmt.Errorf("file larger than %d MB — aborted", limit>>20)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", nil, err
	}
	return full, data, nil
}

// RequestFileConsent is the send_file action: determine the file size and post
// the consent card. The upload only happens after the recipient's click — until
// then the agent parks on blocked.
func RequestFileConsent(ctx context.Context, c *Client, serviceURL, conversationID, path, description, workdir string) (SendFileResult, error) {
	full, data, err := readForUpload(workdir, path)
	if err != nil {
		return SendFileResult{}, err
	}
	name := filepath.Base(full)
	if strings.TrimSpace(description) == "" {
		description = name
	}
	// consentKey travels unchanged through Teams and comes back in the invoke
	// activity. We put the *requested path* in it, not the file name: only then
	// can the wake prompt later assemble the ready-made upload_file call — even
	// if the file sits in a subfolder, where the basename alone points nowhere.
	if _, err := c.SendFileConsent(ctx, serviceURL, conversationID, name, description, int64(len(data)), strings.TrimSpace(path)); err != nil {
		return SendFileResult{}, err
	}
	return SendFileResult{
		Filename: name,
		Bytes:    int64(len(data)),
		Hint: fmt.Sprintf(
			"The consent card for %s is in the chat. End now with blocked on teams:conversation:%s — "+
				"once the recipient clicks \"accept\", you are woken with the ready-made upload_file call.",
			name, conversationID),
	}, nil
}

// UploadConsentedFile is the upload_file action: push the bytes to the upload
// URL Teams delivered and show the finished file in the chat as a card.
// Without the completion card, only the consent would remain in the history.
func UploadConsentedFile(ctx context.Context, c *Client, in UploadInput, workdir string) (UploadFileResult, error) {
	full, data, err := readForUpload(workdir, in.Path)
	if err != nil {
		return UploadFileResult{}, err
	}
	if err := c.UploadFile(ctx, in.UploadURL, data); err != nil {
		return UploadFileResult{}, err
	}
	name := in.Name
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(full)
	}
	hint := fmt.Sprintf("%s is uploaded (%d bytes).", name, len(data))
	// The completion card is a bonus: if it fails, the bytes are up there
	// anyway — the action must not report that as a failure.
	if in.ServiceURL != "" && in.ConversationID != "" {
		if _, err := c.SendFileInfo(ctx, in.ServiceURL, in.ConversationID, name, in.ContentURL, in.UniqueID, in.FileType); err != nil {
			hint += " The completion card could not be posted (" + err.Error() + ") — tell the recipient briefly that the file has arrived."
		}
	}
	return UploadFileResult{Filename: name, Bytes: int64(len(data)), Hint: hint}, nil
}

// UploadInput bundles the values from the invoke activity that stand in the
// task body, plus the path of the file in the sandbox.
type UploadInput struct {
	UploadURL      string
	Path           string
	ServiceURL     string
	ConversationID string
	ContentURL     string
	UniqueID       string
	FileType       string
	Name           string
}
