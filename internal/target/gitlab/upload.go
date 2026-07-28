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

// UploadResultOut ist die Antwort der upload-Aktion: die Markdown-Referenz zum
// Einbetten in einen comment_mr-Body plus die relative URL.
type UploadResultOut struct {
	Markdown string `json:"markdown"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Bytes    int    `json:"bytes"`
	Hint     string `json:"hint"`
}

// UploadFromSandbox lädt eine Datei aus der Sandbox (z. B. einen Browser-
// Screenshot) gebrokert an ein GitLab-Projekt und liefert die Markdown-Referenz
// zurück, die der Agent in comment_mr einbettet. Der Pfad wird sicher gegen das
// Arbeitsverzeichnis aufgelöst — kein Ausbruch per ".." oder absolutem Pfad.
func UploadFromSandbox(ctx context.Context, gc *Client, projectID int, path, workdir string) (UploadResultOut, error) {
	if workdir == "" {
		return UploadResultOut{}, fmt.Errorf("upload braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	if projectID == 0 {
		return UploadResultOut{}, fmt.Errorf("project_id fehlt")
	}
	local, err := resolveInWorkdir(workdir, path)
	if err != nil {
		return UploadResultOut{}, err
	}
	data, err := os.ReadFile(local)
	if err != nil {
		return UploadResultOut{}, fmt.Errorf("datei lesen: %w", err)
	}
	if len(data) > maxUploadBytes {
		return UploadResultOut{}, fmt.Errorf("datei größer als %d MB — abgebrochen", maxUploadBytes>>20)
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
		Hint:     "Diese Markdown-Referenz in den comment_mr-Body einbetten, damit der Screenshot direkt im MR erscheint.",
	}, nil
}

// resolveInWorkdir löst einen Sandbox-Pfad sicher gegen das Arbeitsverzeichnis
// auf — kein Ausbruch per ".." oder absolutem Pfad außerhalb.
func resolveInWorkdir(workdir, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path fehlt")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("pfad %q liegt ausserhalb des Sandbox-Arbeitsverzeichnisses", p)
	}
	return resolved, nil
}
