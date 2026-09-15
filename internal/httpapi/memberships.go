package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"covey/internal/identity"
	"covey/internal/org"
)

// One account, several organisations (FR-002 item 7, #262).
//
// Two sides of the same seat table. The person switches between the seats they
// hold; the instance administration hands seats out and takes them back. An
// org_admin still adds people to their own organisation through /users — what
// sits here is for the operator, who need not belong to any of the
// organisations involved.

// handleMemberships — GET /api/v1/auth/memberships: the seats of the signed-in
// account.
func (s *Server) handleMemberships(w http.ResponseWriter, r *http.Request) {
	list, err := s.Identity.Memberships(r.Context(), principalFrom(r).AccountID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []identity.Membership{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleSwitchOrg — POST /api/v1/auth/switch-org: work from another seat.
//
// Session only (the route wraps it in sessionOnly): an API key is minted from
// one seat and carries that seat's role. A key that could move itself would
// carry it into every organisation its owner belongs to.
func (s *Server) handleSwitchOrg(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrgID string `json:"org_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	orgID, err := uuid.Parse(in.OrgID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid org_id")
		return
	}
	cookie, err := r.Cookie("covey_session")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not signed in")
		return
	}
	token := hashToken(cookie.Value)
	if err := s.sessions().SwitchSeat(r.Context(), token, principalFrom(r).AccountID, orgID); err != nil {
		mapErr(w, err)
		return
	}
	// The answer is the principal as the next request will see it, read back
	// through the same query auth uses — not assembled here a second time.
	p, _, err := s.sessions().Principal(r.Context(), token)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleAddOrgMember — POST /api/v1/platform/orgs/{id}/members: give an
// existing account a seat in this organisation.
func (s *Server) handleAddOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Role      string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	accountID, err := uuid.Parse(in.AccountID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid account_id")
		return
	}
	role := identity.NormalizeRole(in.Role)
	if !validRoles[role] {
		writeErr(w, http.StatusBadRequest, "unknown role "+role)
		return
	}
	h, err := s.Org.AddMember(r.Context(), orgID, accountID, role)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

// handleUpdateOrgMember — PATCH /api/v1/platform/orgs/{id}/members/{account}:
// the role of the seat. The last org_admin stays protected (org.UpdateHuman).
func (s *Server) handleUpdateOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID, humanID, ok := s.memberSeat(w, r)
	if !ok {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	role := identity.NormalizeRole(in.Role)
	if !validRoles[role] {
		writeErr(w, http.StatusBadRequest, "unknown role "+role)
		return
	}
	h, err := s.Org.UpdateHuman(r.Context(), orgID, humanID, org.HumanUpdate{Role: &role})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleRemoveOrgMember — DELETE /api/v1/platform/orgs/{id}/members/{account}:
// take the seat back. The account stays; so do its other seats, and a session
// working from this one moves to the next (org.DeleteHuman).
func (s *Server) handleRemoveOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID, humanID, ok := s.memberSeat(w, r)
	if !ok {
		return
	}
	if err := s.Org.DeleteHuman(r.Context(), orgID, humanID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// memberSeat resolves {id} and {account} to the seat between them, answering
// the request itself when that fails.
func (s *Server) memberSeat(w http.ResponseWriter, r *http.Request) (orgID, humanID uuid.UUID, ok bool) {
	orgID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return uuid.Nil, uuid.Nil, false
	}
	accountID, err := uuid.Parse(r.PathValue("account"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid account id")
		return uuid.Nil, uuid.Nil, false
	}
	humanID, err = s.Org.SeatOf(r.Context(), orgID, accountID)
	if err != nil {
		mapErr(w, err)
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, humanID, true
}
