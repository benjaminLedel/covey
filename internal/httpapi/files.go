package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"

	"covey/internal/orchestrator"
	"covey/internal/sandboxfs"
)

// Der Arbeitsplatz eines Agenten (spec/02): sein persistentes Home als
// Dateibaum — durchsehen, öffnen, hochladen, ändern, löschen. Es ist die
// Antwort auf „was hat der Agent da eigentlich liegen?", die sonst nur über
// eine Shell auf dem Host zu bekommen war.
//
// Zwei Dinge halten das Feature ehrlich:
//   - Der Zugriff läuft am Daemon vorbei direkt auf das Home-Verzeichnis.
//     Sonst wäre er nur verfügbar, während die Sandbox läuft — und die läuft
//     im Normalfall nicht.
//   - Jede schreibende Änderung landet im Recording (kind "file"). Wer im
//     Arbeitsplatz eines Agenten etwas ablegt, ändert dessen Verhalten; das
//     gehört in dieselbe Spur wie die Aktionen des Agenten selbst.

// handleListFiles: GET /agents/{id}/files?path=…
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	list, err := fs.List(r.URL.Query().Get("path"))
	if err != nil {
		writeFSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleReadFile: GET /agents/{id}/files/content?path=…
func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	f, err := fs.Read(r.URL.Query().Get("path"))
	if err != nil {
		writeFSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleWriteFile: PUT /agents/{id}/files/content — Textdatei anlegen/ersetzen.
func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, http.StatusBadRequest, "path fehlt")
		return
	}
	e, err := fs.Write(in.Path, strings.NewReader(in.Content))
	if err != nil {
		writeFSErr(w, err)
		return
	}
	s.recordFileOp(r, agentID, "write", e.Path, e.Size)
	writeJSON(w, http.StatusOK, e)
}

// handleDownloadFile: GET /agents/{id}/files/download?path=… — die Datei roh,
// in voller Länge. Immer als Anhang: eine Datei aus einem Agenten-Home im
// Browser rendern zu lassen hieße, fremdes HTML/JS auf der Covey-Origin
// auszuführen.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	rc, info, err := fs.Open(r.URL.Query().Get("path"))
	if err != nil {
		writeFSErr(w, err)
		return
	}
	defer rc.Close()

	name := info.Name()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// handleZipFiles: GET /agents/{id}/files/zip?path=…&path=… — mehrere Dateien
// und ganze Ordner in einem Rutsch, als ZIP-Strom.
//
// Der Umfang wird VOR dem ersten Byte ausgemessen: „zu groß" muss ein Status
// sein, kein Archiv, das mitten im Download abbricht. Danach wird gestreamt,
// ohne das Archiv im Speicher oder auf der Platte zwischenzulagern — ein Home
// kann größer sein als der Arbeitsspeicher der Control Plane.
func (s *Server) handleZipFiles(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		writeErr(w, http.StatusBadRequest, "path fehlt")
		return
	}
	plan, err := fs.PlanZip(paths)
	if err != nil {
		writeFSErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": plan.Name}))
	w.WriteHeader(http.StatusOK)
	if err := fs.WriteZip(w, plan); err != nil {
		// Header sind raus — mehr als abbrechen und protokollieren geht nicht.
		// Der Browser meldet den unvollständigen Download.
		s.Log.Warn("zip-download abgebrochen", "pfade", paths, "err", err)
	}
}

// handlePreviewFile: GET /agents/{id}/files/preview?path=… — dieselben Bytes
// wie der Download, aber *inline* darstellbar: Bilder und PDF direkt im
// Browser statt „erst herunterladen, dann im Dateimanager suchen".
//
// Inline heißt: fremde Bytes aus einem Agenten-Home werden auf der
// Covey-Origin gerendert. Drei Riegel halten das eng:
//   - Eine kurze Allowlist von Typen (sandboxfs.InlineType); alles andere
//     bekommt hier ein 415 und geht nur über den Download-Endpunkt raus.
//   - `nosniff`, damit der Browser nicht doch HTML daraus macht.
//   - Eine CSP ohne alles (siehe unten): ein SVG mit Skript führt im <img>
//     ohnehin nichts aus, direkt aufgerufen ist es damit auch entschärft.
func (s *Server) handlePreviewFile(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	rc, info, err := fs.Open(path)
	if err != nil {
		writeFSErr(w, err)
		return
	}
	defer rc.Close()

	ctype := sandboxfs.InlineType(info.Name())
	if ctype == "" {
		writeErr(w, http.StatusUnsupportedMediaType, "dieser dateityp wird nicht inline ausgeliefert")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// `default-src 'none'` verbietet der Antwort jede Nachladung — das gilt für
	// alle Typen. Dazu `sandbox`, das sie in eine Origin ohne Rechte sperrt:
	// nötig für SVG, das direkt aufgerufen sonst als Dokument Skript ausführen
	// dürfte.
	//
	// PDF bekommt das `sandbox` nicht: Chromes eingebauter Viewer ist selbst
	// ein Dokument, das seine Bausteine nachlädt, und meldet in der
	// undurchsichtigen Origin „Fehler beim Laden des PDF-Dokuments" — die
	// Vorschau wäre keine. Der Rest der Absicherung bleibt: `default-src
	// 'none'`, `nosniff` (die Antwort kann nichts anderes als ein PDF werden)
	// und die eigene Sandbox des Viewers, aus der PDF-JavaScript das
	// einbettende Dokument nicht erreicht.
	csp := "default-src 'none'; sandbox"
	if ctype == "application/pdf" {
		csp = "default-src 'none'"
	}
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("inline", map[string]string{"filename": info.Name()}))
	// Nicht cachen: die Datei kann sich unter demselben Pfad jederzeit ändern —
	// der Agent arbeitet ja weiter.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// handleUploadFiles: POST /agents/{id}/files/upload?path=<zielverzeichnis>
// (multipart/form-data, Feld „file", mehrfach erlaubt). Der Body wird
// gestreamt statt gepuffert — ein Upload ins Home kann groß sein.
func (s *Server) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	dir := r.URL.Query().Get("path")

	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "kein multipart-upload")
		return
	}
	uploaded := []sandboxfs.Entry{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload unlesbar: "+err.Error())
			return
		}
		name := partRelPath(part)
		if part.FormName() != "file" || name == "" || name == "." {
			part.Close()
			continue
		}
		e, err := fs.Write(path.Join(dir, name), part)
		part.Close()
		if err != nil {
			writeFSErr(w, err)
			return
		}
		s.recordFileOp(r, agentID, "upload", e.Path, e.Size)
		uploaded = append(uploaded, e)
	}
	if len(uploaded) == 0 {
		writeErr(w, http.StatusBadRequest, "keine datei im upload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": uploaded})
}

// partRelPath liest den Dateinamen eines Upload-Teils als *relativen Pfad*.
//
// Part.FileName() taugt dafür nicht: es gibt nach RFC 7578 §4.2 nur den
// Basisnamen zurück. Diese Vorsicht ist richtig für Server, die den Namen
// ungeprüft in ein Verzeichnis legen — hier steht sie im Weg, denn wer einen
// Ordner in den Browser zieht, will ihn samt Struktur wiederfinden und nicht
// seinen Inhalt ausgeschüttet. Deshalb der Griff zum rohen Header.
//
// Sicher ist das, weil der zusammengesetzte Pfad denselben Weg durch
// sandboxfs.resolve() nimmt wie jeder andere: `..` fällt weg, ein absoluter
// Pfad wird relativ zur Wurzel gelesen, aus dem Home führt nichts hinaus. Ein
// Windows-Pfad („ordner\\datei") wird vorher auf „/" normalisiert.
func partRelPath(part *multipart.Part) string {
	raw := part.FileName() // Rückfall: schon auf den Basisnamen gekürzt
	if disp := part.Header.Get("Content-Disposition"); disp != "" {
		if _, params, err := mime.ParseMediaType(disp); err == nil && params["filename"] != "" {
			raw = params["filename"]
		}
	}
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(raw, "\\", "/")), "/")
}

// handleMkdir: POST /agents/{id}/files/dir — {path}
func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, http.StatusBadRequest, "path fehlt")
		return
	}
	e, err := fs.Mkdir(in.Path)
	if err != nil {
		writeFSErr(w, err)
		return
	}
	s.recordFileOp(r, agentID, "mkdir", e.Path, 0)
	writeJSON(w, http.StatusCreated, e)
}

// handleMoveFile: POST /agents/{id}/files/move — {from,to} (umbenennen/verschieben).
func (s *Server) handleMoveFile(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.From) == "" || strings.TrimSpace(in.To) == "" {
		writeErr(w, http.StatusBadRequest, "from und to sind Pflicht")
		return
	}
	e, err := fs.Move(in.From, in.To)
	if err != nil {
		writeFSErr(w, err)
		return
	}
	s.recordFileOp(r, agentID, "move", e.Path, e.Size)
	writeJSON(w, http.StatusOK, e)
}

// handleDeleteFile: DELETE /agents/{id}/files?path=… (Verzeichnisse samt Inhalt).
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	if err := fs.Remove(p); err != nil {
		writeFSErr(w, err)
		return
	}
	s.recordFileOp(r, agentID, "delete", strings.TrimPrefix(path.Clean("/"+p), "/"), 0)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// agentFS löst den Agenten aus der URL auf, prüft die Organisation und öffnet
// sein Home. Bei jedem Fehlschlag ist die Antwort bereits geschrieben.
func (s *Server) agentFS(w http.ResponseWriter, r *http.Request) (*sandboxfs.FS, uuid.UUID, bool) {
	// Der Agent kommt geprüft aus agentScoped — ID und Organisation sind dort
	// schon abgeglichen.
	id := agentFrom(r).ID
	if s.Orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "dateizugriff nicht verfügbar")
		return nil, uuid.Nil, false
	}
	fs, err := s.Orch.AgentFiles(id)
	if errors.Is(err, orchestrator.ErrNoFileAccess) {
		writeErr(w, http.StatusServiceUnavailable,
			"der konfigurierte Sandbox-Provider hat kein erreichbares Home")
		return nil, uuid.Nil, false
	}
	if err != nil {
		mapErr(w, err)
		return nil, uuid.Nil, false
	}
	return fs, id, true
}

// recordFileOp schreibt die Änderung ins Recording — mit dem Menschen, der sie
// gemacht hat. Best effort: ein misslungener Eintrag darf die Operation nicht
// nachträglich zum Fehlschlag erklären, sie ist längst passiert.
func (s *Server) recordFileOp(r *http.Request, agentID uuid.UUID, op, filePath string, size int64) {
	p := principalFrom(r)
	err := s.Obs.Record(r.Context(), p.OrgID, agentID, nil, "file", map[string]any{
		"op":       op,
		"path":     filePath,
		"size":     size,
		"actor":    p.DisplayName,
		"actor_id": p.ID,
	})
	if err != nil {
		s.Log.Warn("datei-operation nicht ins recording geschrieben",
			"agent", agentID, "op", op, "pfad", filePath, "err", err)
	}
}

// writeFSErr übersetzt die Fehler des Dateibaums in HTTP-Codes. Ein Pfad, der
// aus dem Home zeigt, ist keine 500 — es ist eine schlechte Anfrage.
func writeFSErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sandboxfs.ErrNotFound):
		writeErr(w, http.StatusNotFound, "pfad nicht gefunden")
	case errors.Is(err, sandboxfs.ErrInvalidPath):
		writeErr(w, http.StatusBadRequest, "ungültiger pfad")
	case errors.Is(err, sandboxfs.ErrNotDir):
		writeErr(w, http.StatusBadRequest, "kein verzeichnis")
	case errors.Is(err, sandboxfs.ErrIsDir):
		writeErr(w, http.StatusBadRequest, "ist ein verzeichnis")
	case errors.Is(err, sandboxfs.ErrExists):
		writeErr(w, http.StatusConflict, "existiert bereits")
	case errors.Is(err, sandboxfs.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("zu groß (max. %d MiB je Datei, %d GiB je Archiv)",
				sandboxfs.MaxWriteBytes>>20, sandboxfs.MaxZipBytes>>30))
	case errors.Is(err, sandboxfs.ErrTooMany):
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("zu viele dateien (max. %d je Archiv)", sandboxfs.MaxZipFiles))
	default:
		mapErr(w, err)
	}
}
