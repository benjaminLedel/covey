package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"covey/internal/llm"
	"covey/internal/notes"
)

// The notetaker (#336, spec/14): a person's own notes, voice notes and
// meetings. Every route acts on the signed-in seat and nothing else — there is
// no id of another person to put into a URL here, and no role that reaches
// further. Speech is recognised on the device; what arrives is text.

func (s *Server) noteStore() *notes.Store { return notes.NewStore(s.Pool) }

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
	n, err := s.noteStore().Update(r.Context(), principalFrom(r).ID, id, in.Title, in.Body)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.noteStore().Delete(r.Context(), principalFrom(r).ID, id); err != nil {
		noteErr(w, err)
		return
	}
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
