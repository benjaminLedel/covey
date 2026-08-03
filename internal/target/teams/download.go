package teams

import (
	"context"
	"fmt"
	"strings"

	"covey/internal/target"
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

	// Ablegen, Namenshärtung und Kollisionsschutz macht der gemeinsame Helfer
	// (internal/target/sandboxdatei.go). Wichtig gerade hier: `attachments/`
	// teilen sich teams und email in derselben Sandbox.
	datei, err := target.DateiAblegen(workdir, "attachments", filename, data, contentType)
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{
		Path:        datei.Pfad,
		Filename:    datei.Dateiname,
		ContentType: datei.ContentType,
		Bytes:       datei.Bytes,
		Hint:        datei.Hinweis,
	}, nil
}
