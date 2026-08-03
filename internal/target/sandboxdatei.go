package target

// Dateien, die ein Zielsystem in die Sandbox des Agenten legt: Mail-Anhänge,
// Teams-Anhänge, GitLab-Uploads. Drei Plugins taten dasselbe in drei Kopien —
// Zielordner anlegen, Basename festnageln, schreiben, Hinweistext bauen —, und
// alle drei überschrieben dabei still eine gleichnamige Datei.
//
// Das ist kein Schönheitsfehler: Zwei Mails mit je `rechnung.pdf` landeten
// nacheinander auf demselben Pfad. Ein Agent, der sich den Pfad gemerkt hat,
// las danach das falsche Dokument — ohne dass irgendwo etwas schieflief.
// email und teams schreiben zudem in dasselbe `attachments/` derselben Sandbox,
// die Kollision ist also nicht einmal auf ein Zielsystem beschränkt.
//
// Hier steht das einmal, kollisionsfrei, mit einem Hinweistext für alle.

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SandboxDatei ist eine in der Sandbox abgelegte Datei — das, was die Aktion
// dem Agenten als Ergebnis zurückgibt.
type SandboxDatei struct {
	Pfad        string
	Dateiname   string
	ContentType string
	Bytes       int64
	Hinweis     string
}

// DateiAblegen schreibt data nach <workdir>/<unterordner>/<name>.
//
// Der Name kommt von außen (Absender, Fremdsystem) und wird auf seinen
// Basename festgenagelt — sonst trüge ein Anhang namens `../../.ssh/authorized_keys`
// aus der Sandbox heraus. Ist der Name bereits belegt, wird ein Zähler
// angehängt (`rechnung-2.pdf`); nur bei byte-gleichem Inhalt bleibt es beim
// bestehenden Pfad, damit ein zweiter Abruf desselben Anhangs keine Kopie
// erzeugt.
func DateiAblegen(workdir, unterordner, name string, data []byte, contentType string) (SandboxDatei, error) {
	dir, dateiname, err := zielVorbereiten(workdir, unterordner, name)
	if err != nil {
		return SandboxDatei{}, err
	}
	pfad, err := freierPfad(dir, dateiname, func(vorhanden string) bool {
		info, err := os.Stat(vorhanden)
		if err != nil || info.Size() != int64(len(data)) {
			return false // Größe unterscheidet sich — Inhalt gar nicht erst lesen
		}
		alt, err := os.ReadFile(vorhanden)
		return err == nil && bytes.Equal(alt, data)
	})
	if err != nil {
		return SandboxDatei{}, err
	}
	if err := os.WriteFile(pfad, data, 0o644); err != nil {
		return SandboxDatei{}, err
	}
	return SandboxDatei{
		Pfad:        pfad,
		Dateiname:   filepath.Base(pfad),
		ContentType: contentType,
		Bytes:       int64(len(data)),
		Hinweis:     Hinweis(pfad, contentType),
	}, nil
}

// StromAblegen schreibt aus r, ohne alles im Speicher zu halten, und bricht bei
// Überschreitung von limit ab. Für Quellen, die den Inhalt streamen statt ihn
// vorher zu puffern (GitLab-Uploads).
//
// Anders als DateiAblegen kann diese Variante gleichen Inhalt nicht erkennen —
// dafür müsste sie den Strom erst vollständig lesen. Ein zweiter Abruf legt
// hier also eine zweite Datei an, statt die erste zu überschreiben. Das ist die
// richtige Reihenfolge der Übel: eine Kopie zu viel ist harmlos, eine still
// ersetzte Datei nicht.
func StromAblegen(workdir, unterordner, name string, r io.Reader, limit int64, contentType string) (SandboxDatei, error) {
	dir, dateiname, err := zielVorbereiten(workdir, unterordner, name)
	if err != nil {
		return SandboxDatei{}, err
	}
	pfad, err := freierPfad(dir, dateiname, nil)
	if err != nil {
		return SandboxDatei{}, err
	}
	f, err := os.OpenFile(pfad, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return SandboxDatei{}, err
	}
	// +1 Byte über dem Limit lesen, um Überschreitung sicher zu erkennen.
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	f.Close()
	if err != nil {
		os.Remove(pfad)
		return SandboxDatei{}, err
	}
	if n > limit {
		os.Remove(pfad)
		return SandboxDatei{}, fmt.Errorf("datei größer als %d MB — abgebrochen", limit>>20)
	}
	return SandboxDatei{
		Pfad:        pfad,
		Dateiname:   filepath.Base(pfad),
		ContentType: contentType,
		Bytes:       n,
		Hinweis:     Hinweis(pfad, contentType),
	}, nil
}

// Hinweis ist der Satz, der dem Agenten sagt, was er mit der Datei tun kann.
// Bei Bildern der Verweis auf das Read-Tool (Vision), sonst der allgemeine.
func Hinweis(pfad, contentType string) string {
	if strings.HasPrefix(contentType, "image/") {
		return fmt.Sprintf("Bild liegt lokal unter %s — sieh es dir mit dem Read-Tool an (Vision).", pfad)
	}
	var ct string
	if contentType != "" {
		ct = " (Content-Type " + contentType + ")"
	}
	return fmt.Sprintf("Datei liegt lokal unter %s%s. Ist es ein Bild, sieh es mit dem Read-Tool an (Vision); sonst öffne es passend.", pfad, ct)
}

// zielVorbereiten legt den Zielordner an und härtet den Dateinamen.
func zielVorbereiten(workdir, unterordner, name string) (dir, dateiname string, err error) {
	if workdir == "" {
		return "", "", fmt.Errorf("kein Arbeitsverzeichnis — die Aktion braucht eine Sandbox")
	}
	dateiname = filepath.Base(strings.TrimSpace(name))
	// filepath.Base liefert für leere und reine Trenner-Eingaben "." bzw. "/".
	if dateiname == "" || dateiname == "." || dateiname == ".." || dateiname == string(filepath.Separator) {
		dateiname = "anhang"
	}
	dir = filepath.Join(workdir, unterordner)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	return dir, dateiname, nil
}

// freierPfad sucht einen Namen, der keine fremde Datei überschreibt.
// istGleich darf nil sein; dann gilt jeder belegte Name als fremd.
func freierPfad(dir, name string, istGleich func(pfad string) bool) (string, error) {
	ext := filepath.Ext(name)
	stamm := strings.TrimSuffix(name, ext)
	pfad := filepath.Join(dir, name)
	for i := 2; ; i++ {
		info, err := os.Stat(pfad)
		if os.IsNotExist(err) {
			return pfad, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() && istGleich != nil && istGleich(pfad) {
			return pfad, nil // schon da, byte-gleich — kein zweites Exemplar
		}
		// Eine Obergrenze, damit ein Fehler im Aufrufer nicht das Dateisystem
		// füllt. Wer 1000 gleichnamige Anhänge in einer Sandbox hat, hat ein
		// anderes Problem.
		if i > 999 {
			return "", fmt.Errorf("zu viele gleichnamige Dateien %q in %s", name, dir)
		}
		pfad = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stamm, i, ext))
	}
}

// MaxBytesAusEnv liest ein Größenlimit in MB aus einer ENV-Variablen.
//
// Werte über maxMB werden auf maxMB geklemmt statt still auf den Default
// zurückzufallen: Wer 2048 einträgt, will offensichtlich viel und nicht die
// Voreinstellung — ein 80-fach kleineres Limit ohne ein Wort wäre die
// unfreundlichste aller Antworten. Unlesbares oder Nicht-Positives bleibt beim
// Default (fail-closed; ein absurd großer Wert liefe beim Umrechnen in Bytes
// über und hebelte die Größenprüfung gerade aus). Beide Fälle sagen es im Log.
func MaxBytesAusEnv(name string, standardMB, maxMB int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return standardMB << 20
	}
	mb, err := strconv.ParseInt(v, 10, 64)
	if err != nil || mb <= 0 {
		slog.Warn("größenlimit unlesbar — Voreinstellung bleibt",
			"env", name, "wert", v, "gültig", fmt.Sprintf("1-%d", maxMB), "verwendet_mb", standardMB)
		return standardMB << 20
	}
	if mb > maxMB {
		slog.Warn("größenlimit über dem Maximum — geklemmt",
			"env", name, "wert", mb, "verwendet_mb", maxMB)
		mb = maxMB
	}
	return mb << 20
}
