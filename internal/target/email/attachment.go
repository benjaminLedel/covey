package email

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentResult ist die Antwort der get_attachment-Aktion: wo der Anhang in
// der Sandbox liegt und wie der Agent ihn ansieht.
type AttachmentResult struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int64  `json:"bytes"`
	Hint        string `json:"hint"`
}

// getAttachmentToSandbox holt einen Mail-Anhang gebrokert in die Sandbox — die
// Postfach-Credentials bleiben im Daemon, die Datei landet unter
// <workdir>/attachments/. Der Agent liest sie danach mit dem Read-Tool (Bilder
// per Vision, sonst als Datei). Die Namen der Anhänge liefert get_message.
func getAttachmentToSandbox(cfg Config, mailbox string, uid uint32, name, workdir string) (AttachmentResult, error) {
	if workdir == "" {
		return AttachmentResult{}, fmt.Errorf("get_attachment braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	if strings.TrimSpace(name) == "" {
		return AttachmentResult{}, fmt.Errorf("name fehlt")
	}
	limit := maxAttachmentBytes()
	fname, contentType, data, err := getAttachment(cfg, mailbox, uid, name, limit)
	if err != nil {
		return AttachmentResult{}, err
	}

	// Dateinamen auf den Basename festnageln — kein Pfad-Traversal aus dem
	// vom Absender gesetzten Anhang-Namen.
	filename := filepath.Base(strings.TrimSpace(fname))
	if filename == "." || filename == ".." || filename == string(filepath.Separator) {
		filename = "anhang"
	}

	destDir := filepath.Join(workdir, "attachments")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return AttachmentResult{}, err
	}
	dest := filepath.Join(destDir, filename)
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return AttachmentResult{}, err
	}

	// Content-Type nur nennen, wenn die Mail einen brauchbaren mitgibt.
	var ct string
	if contentType != "" {
		ct = " (Content-Type " + contentType + ")"
	}
	hint := fmt.Sprintf("Datei liegt lokal unter %s%s. Ist es ein Bild, sieh es mit dem Read-Tool an (Vision); sonst öffne es passend.", dest, ct)
	if strings.HasPrefix(contentType, "image/") {
		hint = fmt.Sprintf("Bild liegt lokal unter %s — sieh es dir mit dem Read-Tool an (Vision).", dest)
	}
	return AttachmentResult{
		Path:        dest,
		Filename:    filename,
		ContentType: contentType,
		Bytes:       int64(len(data)),
		Hint:        hint,
	}, nil
}
