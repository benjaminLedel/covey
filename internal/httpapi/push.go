package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"covey/internal/push"
)

// Push notifications (#379): a device registers its token, an organisation
// decides whether a notification may carry the first line of what was said,
// and an instance holding the app's APNs key may relay for others.

// handleRegisterDevice keeps the device's token for the signed-in person.
func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	var in struct {
		Token       string `json:"token"`
		Platform    string `json:"platform"`
		Environment string `json:"environment"`
		Lang        string `json:"lang"`
		// Sound: bot | schar | glas | system | none (#381); empty is bot.
		Sound string `json:"sound"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" || len(in.Token) > 200 || strings.ContainsAny(in.Token, "/?# ") ||
		(in.Platform != "ios" && in.Platform != "macos") ||
		(in.Environment != "production" && in.Environment != "development") {
		writeErr(w, http.StatusBadRequest, "expected token, platform ios|macos and environment production|development")
		return
	}
	if in.Sound == "" {
		in.Sound = "bot"
	}
	if !slices.Contains(append([]string{"system", "none"}, push.Sounds...), in.Sound) {
		writeErr(w, http.StatusBadRequest, "sound is one of bot, schar, glas, system, none")
		return
	}
	lang := strings.ToLower(strings.TrimSpace(in.Lang))
	if len(lang) > 2 {
		lang = lang[:2]
	}
	if err := push.Register(r.Context(), s.Pool, p.ID, in.Token, in.Platform, in.Environment, lang, in.Sound); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterDevice(w http.ResponseWriter, r *http.Request) {
	if err := push.Unregister(r.Context(), s.Pool, principalFrom(r).ID, r.PathValue("token")); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetPush(w http.ResponseWriter, r *http.Request) {
	var preview bool
	if err := s.Pool.QueryRow(r.Context(), `SELECT push_preview FROM organizations WHERE id=$1`,
		principalFrom(r).OrgID).Scan(&preview); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"preview": preview})
}

// handleSetPush switches the preview: with it a notification carries the
// first line of what was said, which then passes through Apple and, where
// used, the relay.
func (s *Server) handleSetPush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Preview *bool `json:"preview"`
	}
	if err := readJSON(r, &in); err != nil || in.Preview == nil {
		writeErr(w, http.StatusBadRequest, "expected {\"preview\": true|false}")
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `UPDATE organizations SET push_preview=$2 WHERE id=$1`,
		principalFrom(r).OrgID, *in.Preview); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"preview": *in.Preview})
}

// handlePushRelay delivers a notification for an instance without the app's
// key. Only an instance that holds the key and was told to relay answers;
// what it takes is the notification's fixed fields, bounded, and rate
// limited per address.
func (s *Server) handlePushRelay(w http.ResponseWriter, r *http.Request) {
	if s.PushRelay == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !s.relayLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many notifications from this address")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var m push.Message
	if err := readJSON(r, &m); err != nil || !m.Valid() {
		writeErr(w, http.StatusBadRequest, "expected token, environment, title (and body, badge, agent_id) within bounds")
		return
	}
	switch err := s.PushRelay.Send(r.Context(), m); {
	case err == push.ErrGone:
		writeErr(w, http.StatusGone, "device token no longer valid")
	case err != nil:
		writeErr(w, http.StatusBadGateway, "not delivered")
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// newRelayLimiter: an instance sends a notification per entry and device;
// six hundred an hour per address is a busy organisation, not a flood.
func newRelayLimiter() *webhookLimiter {
	return &webhookLimiter{hits: make(map[string][]time.Time), maxHits: 600, window: time.Hour}
}
