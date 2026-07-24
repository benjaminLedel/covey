package gitlab

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DownloadUploadResult ist die Antwort der download_upload-Aktion: wo das Bild
// in der Sandbox liegt und wie der Agent es ansieht.
type DownloadUploadResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// DownloadUploadToSandbox holt einen an ein Issue/MR angehängten Upload
// (Screenshot) gebrokert in die Sandbox — das Token bleibt im Daemon, die Datei
// landet unter <workdir>/uploads/. Der Agent liest sie danach mit dem Read-Tool
// (Vision) und kann den Screenshot tatsächlich ansehen. ref ist die Referenz aus
// der Issue-Beschreibung: "/uploads/<secret>/<datei>.png", die volle Web-URL oder
// schon "<secret>/<datei>".
func DownloadUploadToSandbox(ctx context.Context, gc *Client, projectID int, ref, workdir string) (DownloadUploadResult, error) {
	if workdir == "" {
		return DownloadUploadResult{}, fmt.Errorf("download_upload braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	name, contentType, body, err := gc.DownloadUpload(ctx, projectID, ref)
	if err != nil {
		return DownloadUploadResult{}, err
	}
	defer body.Close()

	destDir := filepath.Join(workdir, "uploads")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return DownloadUploadResult{}, err
	}
	// Dateinamen auf den Basename festnageln — kein Pfad-Traversal aus der Referenz.
	dest := filepath.Join(destDir, filepath.Base(name))
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return DownloadUploadResult{}, err
	}
	// +1 Byte über dem Limit lesen, um Überschreitung sicher zu erkennen.
	n, err := io.Copy(f, io.LimitReader(body, maxUploadBytes+1))
	f.Close()
	if err != nil {
		os.Remove(dest)
		return DownloadUploadResult{}, err
	}
	if n > maxUploadBytes {
		os.Remove(dest)
		return DownloadUploadResult{}, fmt.Errorf("upload größer als %d MB — abgebrochen", maxUploadBytes>>20)
	}

	hint := fmt.Sprintf("Bild liegt lokal unter %s — sieh es dir mit dem Read-Tool an (Vision), um den Screenshot auszuwerten.", dest)
	if !strings.HasPrefix(contentType, "image/") && contentType != "" {
		hint = fmt.Sprintf("Datei liegt lokal unter %s (Content-Type %s). Ist es ein Bild, sieh es mit Read an; sonst öffne es passend.", dest, contentType)
	}
	return DownloadUploadResult{
		Path:        dest,
		Filename:    filepath.Base(name),
		ContentType: contentType,
		Bytes:       n,
		Hint:        hint,
	}, nil
}
