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

// An agent's workplace (spec/02): its persistent home as a file tree — browse,
// open, upload, change, delete. It is the answer to "what does the agent
// actually have lying around there?", which previously was only obtainable via
// a shell on the host.
//
// Two things keep the feature honest:
//   - Access goes past the daemon, straight to the home directory. Otherwise it
//     would only be available while the sandbox is running — and normally it is
//     not.
//   - Every writing change lands in the recording (kind "file"). Whoever puts
//     something into an agent's workplace changes its behaviour; that belongs in
//     the same trail as the agent's own actions.

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

// handleFilesUsage: GET /agents/{id}/files/usage — how full the sandbox is and
// which working copies are eating it. Nothing measured this before, and the
// consequence (checkouts pile up in the persistent home until a run is killed)
// was only visible to the agent itself.
func (s *Server) handleFilesUsage(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, fs.Usage())
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

// handleWriteFile: PUT /agents/{id}/files/content — create/replace a text file.
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
		writeErr(w, http.StatusBadRequest, "path is missing")
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

// handleDownloadFile: GET /agents/{id}/files/download?path=… — the raw file, at
// full length. Always as an attachment: letting a file from an agent home be
// rendered in the browser would mean executing foreign HTML/JS on the covey
// origin.
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

	name := info.Name
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// handleZipFiles: GET /agents/{id}/files/zip?path=…&path=… — several files and
// whole folders in one go, as a ZIP stream.
//
// The size is measured BEFORE the first byte: "too large" must be a status, not
// an archive that breaks off mid-download. After that it is streamed, without
// buffering the archive in memory or on disk — a home can be larger than the
// control plane's RAM.
func (s *Server) handleZipFiles(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		writeErr(w, http.StatusBadRequest, "path is missing")
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
		// Headers are already out — there is nothing to do but abort and log.
		// The browser reports the incomplete download.
		s.Log.Warn("zip download aborted", "paths", paths, "err", err)
	}
}

// handlePreviewFile: GET /agents/{id}/files/preview?path=… — the same bytes as
// the download, but displayable *inline*: images and PDFs straight in the
// browser instead of "download first, then hunt for it in the file manager".
//
// Inline means: foreign bytes from an agent home are rendered on the covey
// origin. Three bars keep that narrow:
//   - A short allowlist of types (sandboxfs.InlineType); everything else gets a
//     415 here and only leaves via the download endpoint.
//   - `nosniff`, so the browser does not turn it into HTML after all.
//   - A CSP without anything (see below): an SVG with a script executes nothing
//     inside an <img> anyway, and called directly it is defused as well.
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

	ctype := sandboxfs.InlineType(info.Name)
	if ctype == "" {
		writeErr(w, http.StatusUnsupportedMediaType, "this file type is not served inline")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// `default-src 'none'` forbids the response any subresource load — that
	// holds for all types. Plus `sandbox`, which locks it into an origin without
	// privileges: needed for SVG, which called directly would otherwise be a
	// document allowed to run script.
	//
	// PDF does not get the `sandbox`: Chrome's built-in viewer is itself a
	// document that loads its own building blocks, and in the opaque origin it
	// reports "error loading PDF document" — that would be no preview at all.
	// The rest of the hardening stays: `default-src 'none'`, `nosniff` (the
	// response cannot become anything but a PDF) and the viewer's own sandbox,
	// out of which PDF JavaScript cannot reach the embedding document.
	csp := "default-src 'none'; sandbox"
	if ctype == "application/pdf" {
		csp = "default-src 'none'"
	}
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("inline", map[string]string{"filename": info.Name}))
	// Do not cache: the file can change under the same path at any time — the
	// agent keeps working, after all.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// handleUploadFiles: POST /agents/{id}/files/upload?path=<target directory>
// (multipart/form-data, field "file", may repeat). The body is streamed instead
// of buffered — an upload into the home can be large.
func (s *Server) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	fs, agentID, ok := s.agentFS(w, r)
	if !ok {
		return
	}
	dir := r.URL.Query().Get("path")

	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "not a multipart upload")
		return
	}
	uploaded := []sandboxfs.Entry{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload unreadable: "+err.Error())
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
		writeErr(w, http.StatusBadRequest, "no file in the upload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": uploaded})
}

// partRelPath reads the file name of an upload part as a *relative path*.
//
// Part.FileName() is no good for that: per RFC 7578 §4.2 it returns only the
// base name. That caution is right for servers that drop the name unchecked
// into a directory — here it is in the way, because whoever drags a folder into
// the browser wants to find it again with its structure and not have its
// contents dumped out. Hence reaching for the raw header.
//
// This is safe because the assembled path takes the same route through
// sandboxfs.resolve() as any other: `..` is dropped, an absolute path is read
// relative to the root, nothing leads out of the home. A Windows path
// ("folder\\file") is normalised to "/" beforehand.
func partRelPath(part *multipart.Part) string {
	raw := part.FileName() // fallback: already trimmed to the base name
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
		writeErr(w, http.StatusBadRequest, "path is missing")
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

// handleMoveFile: POST /agents/{id}/files/move — {from,to} (rename/move).
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
		writeErr(w, http.StatusBadRequest, "from and to are required")
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

// handleDeleteFile: DELETE /agents/{id}/files?path=… (directories including
// their contents).
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

// agentFS resolves the agent from the URL, checks the organisation and opens
// its home. On every failure the response has already been written.
func (s *Server) agentFS(w http.ResponseWriter, r *http.Request) (sandboxfs.Tree, uuid.UUID, bool) {
	// The agent comes checked out of agentScoped — ID and organisation have
	// already been reconciled there.
	id := agentFrom(r).ID
	if s.Orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "file access is not available")
		return nil, uuid.Nil, false
	}
	fs, err := s.Orch.AgentFiles(id)
	if errors.Is(err, orchestrator.ErrNoFileAccess) {
		writeErr(w, http.StatusServiceUnavailable,
			"the configured sandbox provider has no reachable home")
		return nil, uuid.Nil, false
	}
	if err != nil {
		mapErr(w, err)
		return nil, uuid.Nil, false
	}
	return fs, id, true
}

// recordFileOp writes the change into the recording — together with the human
// who made it. Best effort: a failed entry must not retroactively turn the
// operation into a failure, it has long since happened.
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
		s.Log.Warn("file operation not written to the recording",
			"agent", agentID, "op", op, "path", filePath, "err", err)
	}
}

// writeFSErr translates the file tree's errors into HTTP codes. A path pointing
// out of the home is not a 500 — it is a bad request.
func writeFSErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sandboxfs.ErrNotFound):
		writeErr(w, http.StatusNotFound, "path not found")
	case errors.Is(err, sandboxfs.ErrInvalidPath):
		writeErr(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, sandboxfs.ErrNotDir):
		writeErr(w, http.StatusBadRequest, "not a directory")
	case errors.Is(err, sandboxfs.ErrIsDir):
		writeErr(w, http.StatusBadRequest, "is a directory")
	case errors.Is(err, sandboxfs.ErrExists):
		writeErr(w, http.StatusConflict, "already exists")
	case errors.Is(err, sandboxfs.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("too large (max. %d MiB per file, %d GiB per archive)",
				sandboxfs.MaxWriteBytes>>20, sandboxfs.MaxZipBytes>>30))
	case errors.Is(err, sandboxfs.ErrTooMany):
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("too many files (max. %d per archive)", sandboxfs.MaxZipFiles))
	default:
		// A home that is only being read from its last snapshot says so with
		// its own status: 409, because the request is not wrong — it has come
		// at a moment when the home cannot be written to.
		var readOnly *sandboxfs.ReadOnlyError
		if errors.As(err, &readOnly) {
			writeErr(w, http.StatusConflict, readOnly.Reason)
			return
		}
		mapErr(w, err)
	}
}
