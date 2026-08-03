package email

import (
	"fmt"
	"strings"

	"covey/internal/target"
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

	// Ablegen, Namenshärtung und Kollisionsschutz macht der gemeinsame Helfer
	// (internal/target/sandboxdatei.go) — teams und gitlab schreiben über
	// denselben Weg, und `attachments/` teilen sich email und teams sogar.
	datei, err := target.DateiAblegen(workdir, "attachments", fname, data, contentType)
	if err != nil {
		return AttachmentResult{}, err
	}
	return AttachmentResult{
		Path:        datei.Pfad,
		Filename:    datei.Dateiname,
		ContentType: datei.ContentType,
		Bytes:       datei.Bytes,
		Hint:        datei.Hinweis,
	}, nil
}
