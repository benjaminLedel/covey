package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"covey/internal/activity"
	"covey/internal/llm"
	"covey/internal/notes"
)

// The activity log (#363, internal/activity): what the macOS app recorded in
// the background once the person switched it on. The routes carry no seat —
// they read and write the caller's own log only, like the notes: nobody,
// org_admin included, reads another person's activity.

func (s *Server) activityStore() *activity.Store { return activity.NewStore(s.Pool) }

func (s *Server) activityRetention() time.Duration {
	if s.Config != nil {
		return s.Config.ActivityRetention
	}
	return 14 * 24 * time.Hour
}

// dayRange reads ?day=YYYY-MM-DD and ?tz= (an IANA zone, default UTC): the
// person's calendar day, not the server's.
func dayRange(r *http.Request) (from, to time.Time, loc *time.Location, err error) {
	loc = time.UTC
	if tz := r.URL.Query().Get("tz"); tz != "" {
		if loc, err = time.LoadLocation(tz); err != nil {
			return
		}
	}
	day, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("day"), loc)
	if err != nil {
		return
	}
	return day, day.AddDate(0, 0, 1), loc, nil
}

// handleAddActivity takes a batch of sessions from the app.
func (s *Server) handleAddActivity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	var in struct {
		Sessions []activity.Session `json:"sessions"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	n, err := s.activityStore().Add(r.Context(), p.OrgID, p.ID, in.Sessions, s.activityRetention())
	if errors.Is(err, activity.ErrInvalid) {
		writeErr(w, http.StatusBadRequest, "1 to 500 sessions, each with a start not after its end, at most a day long, texts of at most 500 and an excerpt of at most 1000 characters")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"added": n})
}

// handleListActivity answers with one day of the caller's sessions.
func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := dayRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "day=YYYY-MM-DD is required; tz must be an IANA time zone")
		return
	}
	out, err := s.activityStore().Between(r.Context(), principalFrom(r).ID, from, to)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleDeleteActivity deletes one day (?day=, ?tz=) or, without a day,
// the caller's whole log.
func (s *Server) handleDeleteActivity(w http.ResponseWriter, r *http.Request) {
	var from, to time.Time
	if r.URL.Query().Get("day") != "" {
		var err error
		if from, to, _, err = dayRange(r); err != nil {
			writeErr(w, http.StatusBadRequest, "day=YYYY-MM-DD; tz must be an IANA time zone")
			return
		}
	}
	n, err := s.activityStore().Delete(r.Context(), principalFrom(r).ID, from, to)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

// handleActivityReview writes the review of a day as a note of the caller:
// one control-plane turn with the organisation's credential; without one,
// 409. The title comes from the app, in its language.
func (s *Server) handleActivityReview(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	from, to, loc, err := dayRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "day=YYYY-MM-DD is required; tz must be an IANA time zone")
		return
	}
	var in struct {
		Lang  string `json:"lang"`
		Title string `json:"title"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	lang := strings.TrimSpace(in.Lang)
	if lang == "" || len(lang) > 10 {
		lang = "en"
	}
	sessions, err := s.activityStore().Between(r.Context(), p.ID, from, to)
	if err != nil {
		mapErr(w, err)
		return
	}
	if len(sessions) == 0 {
		writeErr(w, http.StatusNotFound, "no activity recorded for that day")
		return
	}
	provider, err := llm.Resolve(r.Context(), s.Secrets, p.OrgID)
	if errors.Is(err, llm.ErrNoCredential) {
		writeErr(w, http.StatusConflict, "the review needs a control-plane credential for this organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	body, err := activity.Review(r.Context(), provider, from, sessions, lang, loc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the model did not write a review: "+err.Error())
		return
	}
	n, err := s.noteStore().Create(r.Context(), p.OrgID, p.ID, notes.KindText, in.Title, body, 0)
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}
