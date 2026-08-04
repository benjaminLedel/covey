package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"covey/internal/audit"
	"covey/internal/identity"
)

// mitAuditSpur records every mutating request.
//
// Deliberately a middleware and not a line in the affected handlers: the
// organisation boundary was open in 25 places for exactly this reason — an
// obligation every caller has to remember gets forgotten. An audit trail one can
// forget is particularly treacherous: its absence looks like "nothing happened".
//
// Only WHO TOUCHED WHAT WHEN is recorded — method, path, result. No request
// body: it would contain secret values and passwords, and a trail one is not
// allowed to keep because of its content is of no use to anyone. The path
// carries the IDs, so "who deleted guard rail X" remains answerable.
func (s *Server) mitAuditSpur(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Audit == nil || !istVeraendernd(r) || !istAuditWuerdig(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// The principal only comes into being INSIDE (auth middleware) — a
		// context created by the inner layer no longer reaches the outer one.
		// Hence a holder that auth fills in: the alternative would be hooking
		// the audit into every route, and then it would again be something one
		// can forget.
		halter := &akteurHalter{}
		r = r.WithContext(context.WithValue(r.Context(), akteurKey, halter))

		rec := &statusMitschrift{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Write only after the response: the principal is only settled once the
		// auth middleware has put it into the context, and the result (status) is
		// part of the information — a rejected attempt to delete a guard rail
		// belongs in the trail just as much as a successful one.
		p := halter.p
		if p.ID == uuid.Nil {
			return // not signed in — no actor, no entry
		}
		actor := p.ID
		eintrag := audit.Eintrag{
			OrgID: p.OrgID, ActorID: &actor, ActorEmail: p.Email, ActorRole: p.Role,
			Method: r.Method, Path: r.URL.Path, Status: rec.status, ClientIP: clientIP(r),
		}
		// Best effort: the action has happened; letting it fail after the fact
		// because logging is stuck does not improve the situation. The error does
		// belong in the log though — an audit trail that fails silently is worse
		// than none, because people rely on it.
		if err := s.Audit.Record(r.Context(), eintrag); err != nil {
			s.Log.Error("audit entry not written — the trail has a gap",
				"method", r.Method, "path", r.URL.Path, "actor", p.Email, "err", err)
		}
	})
}

// akteurHalter receives the signed-in human from the auth middleware. A plain
// pointer suffices: within one request exactly one place writes, and it does so
// before the handler answers.
type akteurHalter struct{ p identity.Principal }

const akteurKey ctxKey = "akteur"

// istVeraendernd: reads are not logged. That is a deliberate boundary — every
// page view of the interface would otherwise be an entry and the trail
// unreadable. Who SAW WHAT is answered, if needed, by the request log.
func istVeraendernd(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// istAuditWuerdig filters out whatever is not an administrative action.
func istAuditWuerdig(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/") {
		return false // webhooks, static files
	}
	switch {
	// Sign-in/sign-out has no actor in the context (that is only created by it)
	// and is watched by the login limiter anyway.
	case strings.HasPrefix(path, "/api/v1/auth/"):
		return false
	}
	return true
}

// statusMitschrift remembers the status code the handler wrote.
type statusMitschrift struct {
	http.ResponseWriter
	status      int
	geschrieben bool
}

func (m *statusMitschrift) WriteHeader(code int) {
	if !m.geschrieben {
		m.status, m.geschrieben = code, true
	}
	m.ResponseWriter.WriteHeader(code)
}

func (m *statusMitschrift) Write(b []byte) (int, error) {
	m.geschrieben = true // implicit 200
	return m.ResponseWriter.Write(b)
}

// Flush passes the flush through — the SSE endpoints need it.
func (m *statusMitschrift) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handleAuditLog returns the organisation's most recent administrative actions.
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if s.Audit == nil {
		writeErr(w, http.StatusServiceUnavailable, "the audit trail is not configured in this instance")
		return
	}
	p := principalFrom(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	eintraege, err := s.Audit.List(r.Context(), p.OrgID, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, eintraege)
}
