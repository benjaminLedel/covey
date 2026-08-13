package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/accounts"
	"covey/internal/settings"
	"covey/internal/waitlist"
)

// The instance administration: accounts, system settings, waitlist codes.
//
// Everything here runs behind s.platformAdmin, not behind s.rbac — the
// difference is the whole point of FR-003 finding F. An org role is handed out
// by the organisation itself; what governs the installation must not be
// reachable from inside one of its tenants.
//
// The tenant handlers (handleListOrgs and friends) live in admin.go, next to
// the user administration they grew up with.

// --- Accounts ---

// handleListAccounts — GET /api/v1/platform/accounts: every login of this
// installation with its seats.
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	list, err := s.Accounts.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []accounts.Listed{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleSetAccountPlatformRole — PATCH /api/v1/platform/accounts/{id}: raise
// or lower the instance level.
//
// The one place in the product where system_admin is handed out. That it sits
// behind platformAdmin is what makes it safe: only somebody who already
// administers the installation can appoint the next one. The first one comes
// from the bootstrap or from `covey system-admin add`.
func (s *Server) handleSetAccountPlatformRole(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		PlatformRole string `json:"platform_role"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Whoever demotes themselves loses the page they are standing on. That is
	// allowed — one may resign — but not while nobody else is left; the store
	// answers that case with ErrLastSystemAdmin.
	if err := s.Accounts.SetPlatformRoleByID(r.Context(), id, in.PlatformRole); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- System settings ---

// handleListSettings — GET /api/v1/platform/settings: every known switch with
// its effective value, plus the default it would fall back to.
//
// The defaults travel along so the UI can show "unchanged" without carrying a
// second copy of the same table. A settings page that hardcodes what "off"
// means is a settings page that lies after the first change to the defaults.
func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.Settings.All(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	type entry struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Default string `json:"default"`
	}
	out := make([]entry, 0, len(values))
	for _, k := range settings.Keys() {
		out = append(out, entry{Key: k, Value: values[k], Default: settings.Defaults[k]})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetSetting — PUT /api/v1/platform/settings/{key}.
func (s *Server) handleSetSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var in struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p := principalFrom(r)
	by := &p.AccountID
	if err := s.Settings.Set(r.Context(), key, in.Value, by); err != nil {
		switch {
		case errors.Is(err, settings.ErrUnknownKey):
			writeErr(w, http.StatusNotFound, err.Error())
		case errors.Is(err, settings.ErrInvalid):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			mapErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": in.Value})
}

// --- Waitlist codes ---

// handleListWaitlistCodes — GET /api/v1/platform/waitlist-codes.
func (s *Server) handleListWaitlistCodes(w http.ResponseWriter, r *http.Request) {
	if s.Waitlist == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	list, err := s.Waitlist.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []waitlist.Code{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCreateWaitlistCode — POST /api/v1/platform/waitlist-codes.
//
// The answer carries the plaintext code, and it is the only time it exists:
// the table holds nothing but its hash. Whoever loses it revokes the code and
// draws a new one.
func (s *Server) handleCreateWaitlistCode(w http.ResponseWriter, r *http.Request) {
	if s.Waitlist == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Label        string `json:"label"`
		MaxUses      int    `json:"max_uses"`
		ExpiresAt    string `json:"expires_at"`
		OrgID        string `json:"org_id"`
		EmailPattern string `json:"email_pattern"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	opt := waitlist.Options{
		Label:        in.Label,
		MaxUses:      in.MaxUses,
		EmailPattern: in.EmailPattern,
	}
	if in.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, in.ExpiresAt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		opt.ExpiresAt = &t
	}
	if in.OrgID != "" {
		id, err := uuid.Parse(in.OrgID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid org_id")
			return
		}
		opt.OrgID = &id
	}
	// The author is the ACCOUNT (migration 0062): whoever administers the
	// installation need not sit in any of its organisations.
	p := principalFrom(r)
	opt.CreatedBy = &p.AccountID
	code, err := s.Waitlist.Create(r.Context(), opt)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"code": code})
}

// handleRevokeWaitlistCode — DELETE /api/v1/platform/waitlist-codes/{hash}.
// Addressed by hash prefix, because the plaintext is gone.
func (s *Server) handleRevokeWaitlistCode(w http.ResponseWriter, r *http.Request) {
	if s.Waitlist == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	prefix := strings.TrimSpace(r.PathValue("hash"))
	if len(prefix) < 8 {
		writeErr(w, http.StatusBadRequest, "hash prefix too short")
		return
	}
	if err := s.Waitlist.Revoke(r.Context(), prefix); err != nil {
		if errors.Is(err, waitlist.ErrUnknown) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
