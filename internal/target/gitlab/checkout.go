package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxCheckoutBytes begrenzt die entpackte Gesamtgröße eines Checkouts —
// Schutz der Sandbox vor Riesen-Repos und Zip-Bomben.
const maxCheckoutBytes = 512 << 20 // 512 MB

// CheckoutResult ist die Antwort der checkout-Aktion an den Agenten: wo der
// Code liegt und wie er damit weiterarbeitet.
type CheckoutResult struct {
	Path  string `json:"path"`
	Ref   string `json:"ref,omitempty"`
	Files int    `json:"files"`
	Hint  string `json:"hint"`
}

// Checkout materialisiert den Quellcode eines Projekts in der Sandbox: lädt
// das Repository-Archiv über die API (das gebrokerte Token bleibt im Daemon,
// es landet nie im Dateisystem — anders als bei einem git clone mit
// Credential-Remote) und entpackt es unter <workdir>/repos/. Ein vorhandener
// Stand desselben Archivs wird ersetzt — der Agent arbeitet immer auf dem
// aktuellen Code.
func Checkout(ctx context.Context, gc *Client, projectID int, ref, workdir string) (CheckoutResult, error) {
	if workdir == "" {
		return CheckoutResult{}, fmt.Errorf("checkout braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	body, err := gc.DownloadArchive(ctx, projectID, ref)
	if err != nil {
		return CheckoutResult{}, err
	}
	defer body.Close()

	destRoot := filepath.Join(workdir, "repos")
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return CheckoutResult{}, err
	}
	topDir, files, err := extractTarGz(body, destRoot)
	if err != nil {
		return CheckoutResult{}, err
	}
	return CheckoutResult{
		Path:  filepath.Join(destRoot, topDir),
		Ref:   ref,
		Files: files,
		Hint:  "Quellcode liegt lokal — durchsuche und lies ihn direkt (Grep/Read/Bash), um die Frage am Code zu prüfen.",
	}, nil
}

// extractTarGz entpackt ein GitLab-Repository-Archiv nach destRoot und liefert
// den Namen des Top-Level-Verzeichnisses (GitLab: <projekt>-<ref>-<sha>).
// Sicherheit: Pfade werden gegen Traversal geprüft, Symlinks übersprungen,
// die entpackte Gesamtgröße ist begrenzt.
func extractTarGz(r io.Reader, destRoot string) (topDir string, files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", 0, fmt.Errorf("archiv lesen: %w", err)
	}
	defer gz.Close()

	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("archiv lesen: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "pax_global_header" {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return "", 0, fmt.Errorf("unsicherer pfad im archiv: %q", hdr.Name)
		}
		if topDir == "" {
			topDir = strings.SplitN(name, string(filepath.Separator), 2)[0]
			// Vorherigen Stand desselben Archivs ersetzen (frischer Checkout).
			if err := os.RemoveAll(filepath.Join(destRoot, topDir)); err != nil {
				return "", 0, err
			}
		}
		dest := filepath.Join(destRoot, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", 0, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxCheckoutBytes {
				return "", 0, fmt.Errorf("archiv größer als %d MB — Repo zu groß für einen Sandbox-Checkout", maxCheckoutBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", 0, err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return "", 0, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxCheckoutBytes)); err != nil {
				f.Close()
				return "", 0, err
			}
			f.Close()
			files++
		default:
			// Symlinks & Sonstiges: für die Code-Lektüre unnötig, als
			// Ausbruchsvektor riskant — bewusst überspringen.
		}
	}
	if topDir == "" {
		return "", 0, fmt.Errorf("leeres archiv")
	}
	return topDir, files, nil
}
