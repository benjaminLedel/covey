package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

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

// zone reads the person's time zone: ?tz= (an IANA name) or, where the
// device does not know its zone's name, ?offset= (minutes east of UTC);
// UTC without either. The name is how a review records its zone (#369).
func zone(r *http.Request) (*time.Location, error) {
	loc, _, err := zoneOf(r)
	return loc, err
}

func zoneOf(r *http.Request) (*time.Location, string, error) {
	if tz := r.URL.Query().Get("tz"); tz != "" {
		loc, err := time.LoadLocation(tz)
		return loc, "tz:" + tz, err
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		return parseZone("offset:" + o)
	}
	return time.UTC, "tz:UTC", nil
}

// parseZone reads a zone as a review records it: "tz:<name>" or
// "offset:<minutes>".
func parseZone(z string) (*time.Location, string, error) {
	if name, ok := strings.CutPrefix(z, "tz:"); ok {
		loc, err := time.LoadLocation(name)
		return loc, z, err
	}
	if o, ok := strings.CutPrefix(z, "offset:"); ok {
		m, err := strconv.Atoi(o)
		if err != nil || m < -14*60 || m > 14*60 {
			return nil, "", errors.New("offset must be minutes between -840 and 840")
		}
		return time.FixedZone("", m*60), z, nil
	}
	return time.UTC, "tz:UTC", nil
}

// dayRange reads ?day=YYYY-MM-DD in the person's zone: the person's
// calendar day, not the server's.
func dayRange(r *http.Request) (from, to time.Time, loc *time.Location, err error) {
	if loc, _, err = zoneOf(r); err != nil {
		return
	}
	day, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("day"), loc)
	if err != nil {
		return
	}
	return day, day.AddDate(0, 0, 1), loc, nil
}

// handleActivityDays lists the caller's days with activity, newest first,
// each with its review note where there is one (#368).
func (s *Server) handleActivityDays(w http.ResponseWriter, r *http.Request) {
	loc, err := zone(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "tz must be an IANA time zone, offset minutes east of UTC")
		return
	}
	p := principalFrom(r)
	days, err := s.activityStore().Days(r.Context(), p.ID, loc)
	if err != nil {
		mapErr(w, err)
		return
	}
	reviews, err := s.noteStore().Reviews(r.Context(), p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	type day struct {
		activity.Day
		Review *uuid.UUID `json:"review,omitempty"`
		// Stale: the day has activity after what its review covers (#369).
		Stale bool `json:"stale,omitempty"`
	}
	out := make([]day, 0, len(days))
	for _, d := range days {
		x := day{Day: d}
		if r, ok := reviews[d.Day]; ok {
			x.Review = &r.ID
			x.Stale = d.Last.After(r.Through)
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
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
	s.followReviews(p.ID)
	writeJSON(w, http.StatusCreated, map[string]int{"added": n})
}

// handleListActivity answers with one day of the caller's sessions.
func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := dayRange(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "day=YYYY-MM-DD is required; tz must be an IANA time zone, offset minutes east of UTC")
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
		writeErr(w, http.StatusBadRequest, "day=YYYY-MM-DD is required; tz must be an IANA time zone, offset minutes east of UTC")
		return
	}
	_, zoneName, _ := zoneOf(r)
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
	// One review per day: written again, it replaces the note of its day
	// (#368), and records what it was written from (#369).
	n, err := s.noteStore().SetReview(r.Context(), p.OrgID, p.ID, from, in.Title, body,
		notes.ReviewMeta{Lang: lang, Zone: zoneName, Through: lastEnd(sessions)})
	if err != nil {
		noteErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func lastEnd(sessions []activity.Session) time.Time {
	var last time.Time
	for _, x := range sessions {
		if x.EndedAt.After(last) {
			last = x.EndedAt
		}
	}
	return last
}

func (s *Server) reviewRefresh() time.Duration {
	if s.Config != nil {
		return s.Config.ReviewRefresh
	}
	return 0
}

// refreshing holds the reviews being written again, so two uploads in a row
// do not write the same one twice.
var refreshing sync.Map

// followReviews writes the seat's recent reviews again, in the background,
// where their day has activity after what they cover (#369): at most once
// per ReviewRefresh per review, for the last two days, with each review's
// own language and zone and the title it has. A failure leaves the review
// as it was.
func (s *Server) followReviews(humanID uuid.UUID) {
	every := s.reviewRefresh()
	if every <= 0 || s.Secrets == nil {
		return
	}
	ctx := s.BaseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		refs, err := s.noteStore().ReviewsSince(ctx, humanID, time.Now().AddDate(0, 0, -2))
		if err != nil {
			return
		}
		for _, ref := range refs {
			if time.Since(ref.Updated) < every {
				continue
			}
			if _, busy := refreshing.LoadOrStore(ref.ID, true); busy {
				continue
			}
			err := s.refreshReview(ctx, humanID, ref)
			refreshing.Delete(ref.ID)
			if err != nil && s.Log != nil {
				s.Log.Info("daily review not followed", "note", ref.ID, "err", err)
			}
		}
	}()
}

func (s *Server) refreshReview(ctx context.Context, humanID uuid.UUID, ref notes.ReviewRef) error {
	loc, _, err := parseZone(ref.Zone)
	if err != nil {
		return err
	}
	from, err := time.ParseInLocation("2006-01-02", ref.Day, loc)
	if err != nil {
		return err
	}
	sessions, err := s.activityStore().Between(ctx, humanID, from, from.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	last := lastEnd(sessions)
	if len(sessions) == 0 || !last.After(ref.Through) {
		return nil
	}
	provider, err := llm.Resolve(ctx, s.Secrets, ref.OrgID)
	if err != nil {
		return err
	}
	lang := ref.Lang
	if lang == "" {
		lang = "en"
	}
	body, err := activity.Review(ctx, provider, from, sessions, lang, loc)
	if err != nil {
		return err
	}
	// An empty title keeps the one the note has — the person may have
	// renamed it.
	_, err = s.noteStore().SetReview(ctx, ref.OrgID, humanID, from, "", body,
		notes.ReviewMeta{Lang: lang, Zone: ref.Zone, Through: last})
	if err == nil && s.Log != nil {
		s.Log.Info("daily review followed the activity", "note", ref.ID, "day", ref.Day)
	}
	return err
}

// suggestionDays is how far back the suggestions look (#370): the log's
// retention, at most.
const suggestionDays = 14

// handleGetSuggestions answers with the caller's latest agent suggestions,
// or none.
func (s *Server) handleGetSuggestions(w http.ResponseWriter, r *http.Request) {
	x, ok, err := s.activityStore().LoadSuggestions(r.Context(), principalFrom(r).ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"suggestions": nil})
		return
	}
	writeJSON(w, http.StatusOK, x)
}

// handleSuggest computes the caller's agent suggestions from the last two
// weeks of activity (#370) — one control-plane turn — and keeps them.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	loc, err := zone(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "tz must be an IANA time zone, offset minutes east of UTC")
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang == "" || len(lang) > 10 {
		lang = "en"
	}
	now := time.Now()
	sessions, err := s.activityStore().Between(r.Context(), p.ID, now.AddDate(0, 0, -suggestionDays), now.Add(time.Minute))
	if err != nil {
		mapErr(w, err)
		return
	}
	if len(sessions) == 0 {
		writeErr(w, http.StatusNotFound, "no activity recorded in the last 14 days")
		return
	}
	provider, err := llm.Resolve(r.Context(), s.Secrets, p.OrgID)
	if errors.Is(err, llm.ErrNoCredential) {
		writeErr(w, http.StatusConflict, "suggestions need a control-plane credential for this organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	list, err := activity.Suggest(r.Context(), provider, sessions, lang, loc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the model did not suggest: "+err.Error())
		return
	}
	days := map[string]bool{}
	for _, x := range sessions {
		days[x.StartedAt.In(loc).Format("2006-01-02")] = true
	}
	out, err := s.activityStore().SaveSuggestions(r.Context(), p.OrgID, p.ID,
		activity.Suggestions{Days: len(days), Sessions: len(sessions), Suggestions: list})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
