package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"covey/internal/llm"
	"covey/internal/mediastore"
	mediapg "covey/internal/mediastore/pg"
	"covey/internal/notes"
)

// The notetaker (#336, spec/14): a person's own notes, voice notes and
// meetings. Every route acts on the signed-in seat and nothing else — there is
// no id of another person to put into a URL here, and no role that reaches
// further. Speech is recognised on the device; what arrives is text.

func (s *Server) noteStore() *notes.Store { return notes.NewStore(s.Pool) }

// media is the store for pictures in notes (#344): the configured one, else
// the builtin Postgres store.
func (s *Server) media() mediastore.Store {
	if s.Media != nil {
		return s.Media
	}
	return mediapg.New(s.Pool)
}

// forgetMedia removes the pictures that no note of the seat references any
// more — after a note lost them or was deleted. A failure here leaves an
// orphan, not a broken note, so it is not an error for the caller.
func (s *Server) forgetMedia(ctx context.Context, humanID uuid.UUID, ids []uuid.UUID) {
	st := s.noteStore()
	for _, id := range ids {
		if still, err := st.Referenced(ctx, humanID, id); err == nil && !still {
			_ = s.media().Delete(ctx, humanID, id)
		}
	}
}

// maxNoteMedia bounds one picture. A phone photo is 2–6 MB; this leaves room
// and keeps a video or an archive out.
const maxNoteMedia = 12 << 20

// noteMediaType decides what a picture is from its bytes, never from what the
// client says: the stored type is served back, and a type the client could
// choose would let it serve HTML from this origin. SVG is not accepted for
// the same reason — it can carry script.
func noteMediaType(data []byte) string {
	switch t := http.DetectContentType(data); t {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return t
	}
	// HEIC/HEIF (the iPhone's camera format): an ISO media file whose brand
	// says so. DetectContentType does not know it.
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		switch string(data[8:12]) {
		case "heic", "heix", "hevc", "heim", "heis", "mif1", "msf1":
			return "image/heic"
		}
	}
	return ""
}

// handleUploadNoteMedia stores one picture for the seat's notes and answers
// with the reference the note's Markdown carries.
func (s *Server) handleUploadNoteMedia(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNoteMedia+1<<20)
	file, _, err := r.FormFile("file")
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, "a picture may be at most 12 MB")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected a multipart upload with one field \"file\"")
		return
	}
	defer file.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(file, maxNoteMedia+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "upload unreadable")
		return
	}
	if n > maxNoteMedia {
		writeErr(w, http.StatusRequestEntityTooLarge, "a picture may be at most 12 MB")
		return
	}
	kind := noteMediaType(buf.Bytes())
	if kind == "" {
		writeErr(w, http.StatusUnsupportedMediaType, "only JPEG, PNG, GIF, WebP and HEIC pictures")
		return
	}
	id, err := s.media().Put(r.Context(), p.OrgID, p.ID, kind, buf.Bytes())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":  id.String(),
		"ref": notes.MediaScheme + id.String(),
	})
}

// handleNoteMedia serves a picture to the seat that owns it, and to nobody
// else — another person's picture is not found, not forbidden.
func (s *Server) handleNoteMedia(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	b, err := s.media().Get(r.Context(), principalFrom(r).ID, id)
	if errors.Is(err, mediastore.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", b.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A medium never changes under its id: private, and cached for a day.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(b.Data)))
	_, _ = w.Write(b.Data)
}

// noteErr maps the store's errors; everything else goes to mapErr.
func noteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notes.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, notes.ErrInvalid):
		writeErr(w, http.StatusBadRequest,
			"a note needs text (at most "+strconv.Itoa(notes.MaxBody)+" characters), a title of at most 200, and a kind of text, voice or meeting")
	default:
		mapErr(w, err)
	}
}

// handleListNotes answers with the seat's notes and whether "Summarise" is
// possible here at all — the app offers the button only then, rather than
// letting it fail.
func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	list, err := s.noteStore().List(r.Context(), p.ID, r.URL.Query().Get("q"), 200)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notes":     list,
		"summarize": llm.Available(r.Context(), s.Secrets, p.OrgID),
	})
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	var in struct {
		Kind            string `json:"kind"`
		Title           string `json:"title"`
		Body            string `json:"body"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	n, err := s.noteStore().Create(r.Context(), p.OrgID, p.ID, in.Kind, in.Title, in.Body, in.DurationSeconds)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	n, err := s.noteStore().Get(r.Context(), principalFrom(r).ID, id)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	p := principalFrom(r)
	before, err := s.noteStore().Get(r.Context(), p.ID, id)
	if err != nil {
		noteErr(w, err)
		return
	}
	n, err := s.noteStore().Update(r.Context(), p.ID, id, in.Title, in.Body)
	if err != nil {
		noteErr(w, err)
		return
	}
	// Pictures the text no longer shows go, unless another note shows them.
	s.forgetMedia(r.Context(), p.ID, notes.MediaRefs(before.Body))
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	before, err := s.noteStore().Get(r.Context(), p.ID, id)
	if err != nil {
		noteErr(w, err)
		return
	}
	if err := s.noteStore().Delete(r.Context(), p.ID, id); err != nil {
		noteErr(w, err)
		return
	}
	s.forgetMedia(r.Context(), p.ID, notes.MediaRefs(before.Body))
	w.WriteHeader(http.StatusNoContent)
}

// handleSummarizeNote writes a summary and the action items of a note —
// meant for a meeting, allowed for any note. One control-plane turn with the
// organisation's credential; without one, 409 and the app does not offer it.
func (s *Server) handleSummarizeNote(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	st := s.noteStore()
	n, err := st.Get(r.Context(), p.ID, id)
	if err != nil {
		noteErr(w, err)
		return
	}
	provider, err := llm.Resolve(r.Context(), s.Secrets, p.OrgID)
	if errors.Is(err, llm.ErrNoCredential) {
		writeErr(w, http.StatusConflict, "summaries need a control-plane credential for this organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	summary, err := notes.Summarize(r.Context(), provider, n.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the model did not produce a summary: "+err.Error())
		return
	}
	n, err = st.SetSummary(r.Context(), p.ID, id, summary)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}
