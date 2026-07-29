package teams

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Ausgehende Dateien laufen in Teams über den File-Consent-Flow (spec/15):
// Der Agent fragt mit einer Karte um Zustimmung (send_file), der Empfänger
// klickt „Annehmen", Teams liefert per invoke-Activity eine Upload-URL, und
// der Agent schiebt die Bytes dorthin (upload_file). Beide Hälften laufen im
// Action-Proxy, also in der Sandbox — die Datei bleibt im persistenten Home
// des Agenten liegen und muss nirgends zwischengelagert werden.

// SendFileResult ist die Antwort auf send_file: was angefragt wurde und was
// als Nächstes passiert.
type SendFileResult struct {
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
	Hint     string `json:"hint"`
}

// UploadFileResult ist die Antwort auf upload_file.
type UploadFileResult struct {
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
	Hint     string `json:"hint"`
}

// resolveInWorkdir löst einen vom Agenten genannten Pfad innerhalb seines
// Arbeitsverzeichnisses auf. Absolute Pfade und `..` führen nicht hinaus —
// ein Agent soll dem Chat-Partner nicht versehentlich (oder auf Zuruf einer
// manipulierten Quelle) /etc/passwd schicken können.
func resolveInWorkdir(workdir, path string) (string, error) {
	if workdir == "" {
		return "", fmt.Errorf("kein Arbeitsverzeichnis im Kontext (Aktion braucht eine Sandbox)")
	}
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "", fmt.Errorf("pflichtfeld path fehlt")
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
		return "", fmt.Errorf("pfad liegt außerhalb des Arbeitsverzeichnisses: %s", path)
	}
	return full, nil
}

// readForUpload liest die Datei und prüft ihre Größe gegen dasselbe Limit wie
// eingehende Anhänge — fail-closed, bevor irgendetwas an Teams geht.
func readForUpload(workdir, path string) (string, []byte, error) {
	full, err := resolveInWorkdir(workdir, path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", nil, fmt.Errorf("datei nicht lesbar: %w", err)
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("%s ist ein Verzeichnis", path)
	}
	limit := maxAttachmentBytes()
	if info.Size() > limit {
		return "", nil, fmt.Errorf("datei größer als %d MB — abgebrochen", limit>>20)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", nil, err
	}
	return full, data, nil
}

// RequestFileConsent ist die Aktion send_file: Größe der Datei bestimmen und
// die Zustimmungs-Karte posten. Hochgeladen wird erst nach dem Klick des
// Empfängers — der Agent parkt so lange auf blocked.
func RequestFileConsent(ctx context.Context, c *Client, serviceURL, conversationID, path, description, workdir string) (SendFileResult, error) {
	full, data, err := readForUpload(workdir, path)
	if err != nil {
		return SendFileResult{}, err
	}
	name := filepath.Base(full)
	if strings.TrimSpace(description) == "" {
		description = name
	}
	// consentKey wandert durch Teams und kommt in der invoke-Activity zurück —
	// er benennt die Datei, um die es ging.
	if _, err := c.SendFileConsent(ctx, serviceURL, conversationID, name, description, int64(len(data)), name); err != nil {
		return SendFileResult{}, err
	}
	return SendFileResult{
		Filename: name,
		Bytes:    int64(len(data)),
		Hint: fmt.Sprintf(
			"Zustimmungs-Karte für %s ist im Chat. Beende jetzt mit blocked auf teams:conversation:%s — "+
				"klickt der Empfänger „Annehmen\", wirst du mit der Upload-URL geweckt und lädst mit upload_file hoch (path: %s).",
			name, conversationID, path),
	}, nil
}

// UploadConsentedFile ist die Aktion upload_file: die Bytes an die von Teams
// gelieferte Upload-URL schieben und dem Chat die fertige Datei als Karte
// zeigen. Ohne die Abschluss-Karte bliebe im Verlauf nur die Zustimmung stehen.
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
	hint := fmt.Sprintf("%s ist hochgeladen (%d Bytes).", name, len(data))
	// Die Abschluss-Karte ist Kür: schlägt sie fehl, sind die Bytes trotzdem
	// oben — das darf die Aktion nicht als Fehlschlag ausgeben.
	if in.ServiceURL != "" && in.ConversationID != "" {
		if _, err := c.SendFileInfo(ctx, in.ServiceURL, in.ConversationID, name, in.ContentURL, in.UniqueID, in.FileType); err != nil {
			hint += " Die Abschluss-Karte konnte nicht gepostet werden (" + err.Error() + ") — sag dem Empfänger kurz Bescheid, dass die Datei da ist."
		}
	}
	return UploadFileResult{Filename: name, Bytes: int64(len(data)), Hint: hint}, nil
}

// UploadInput bündelt die Werte, die aus der invoke-Activity im Aufgaben-Body
// stehen, plus den Pfad der Datei in der Sandbox.
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
