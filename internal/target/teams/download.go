package teams

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DownloadResult ist die Antwort der download_attachment-Aktion: wo die Datei in
// der Sandbox liegt und wie der Agent sie ansieht.
type DownloadResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// DownloadAttachmentToSandbox holt einen Teams-Anhang gebrokert in die Sandbox —
// das Connector-Token bleibt im Daemon, die Datei landet unter
// <workdir>/attachments/. Der Agent liest sie danach mit dem Read-Tool (Bilder
// per Vision, sonst als Datei). name ist optional; fehlt er, wird der Basename
// aus der URL abgeleitet.
func DownloadAttachmentToSandbox(ctx context.Context, c *Client, downloadURL, name, workdir string) (DownloadResult, error) {
	if workdir == "" {
		return DownloadResult{}, fmt.Errorf("download_attachment braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	limit := maxAttachmentBytes()
	contentType, data, err := c.DownloadAttachment(ctx, downloadURL, limit)
	if err != nil {
		return DownloadResult{}, err
	}
	if int64(len(data)) > limit {
		return DownloadResult{}, fmt.Errorf("anhang größer als %d MB — abgebrochen", limit>>20)
	}

	filename := strings.TrimSpace(name)
	if filename == "" {
		filename = Attachment{ContentURL: downloadURL}.Filename()
	}
	// Dateinamen auf den Basename festnageln — kein Pfad-Traversal aus name/URL.
	filename = filepath.Base(filename)

	destDir := filepath.Join(workdir, "attachments")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return DownloadResult{}, err
	}
	dest := filepath.Join(destDir, filename)
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return DownloadResult{}, err
	}

	hint := fmt.Sprintf("Datei liegt lokal unter %s (Content-Type %s). Ist es ein Bild, sieh es mit dem Read-Tool an (Vision); sonst öffne es passend.", dest, contentType)
	if strings.HasPrefix(contentType, "image/") {
		hint = fmt.Sprintf("Bild liegt lokal unter %s — sieh es dir mit dem Read-Tool an (Vision).", dest)
	}
	return DownloadResult{
		Path:        dest,
		Filename:    filename,
		ContentType: contentType,
		Bytes:       int64(len(data)),
		Hint:        hint,
	}, nil
}
