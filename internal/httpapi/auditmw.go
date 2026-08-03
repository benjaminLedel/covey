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

// mitAuditSpur hält jede verändernde Anfrage fest.
//
// Bewusst als Middleware und nicht als Zeile in den betroffenen Handlern: Die
// Organisationsgrenze war aus genau diesem Grund an 25 Stellen offen — eine
// Pflicht, an die sich jeder Aufrufer erinnern muss, wird vergessen. Eine
// Audit-Spur, die man vergessen kann, ist besonders tückisch: Ihr Fehlen sieht
// aus wie „es ist nichts passiert".
//
// Festgehalten wird nur, WER WANN WAS ANGEFASST hat — Methode, Pfad, Ergebnis.
// Kein Request-Body: Darin stünden Secret-Werte und Passwörter, und eine Spur,
// die man wegen ihres Inhalts nicht aufbewahren darf, nützt niemandem. Der Pfad
// trägt die IDs, „wer hat Guard-Rail X gelöscht" bleibt also beantwortbar.
func (s *Server) mitAuditSpur(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Audit == nil || !istVeraendernd(r) || !istAuditWuerdig(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Der Principal entsteht erst INNEN (auth-Middleware) — ein Context,
		// den die innere Schicht anlegt, erreicht die äußere nicht mehr.
		// Deshalb ein Halter, den auth befüllt: Die Alternative wäre, das
		// Audit in jede Route einzuhängen, und damit wäre es wieder etwas,
		// das man vergessen kann.
		halter := &akteurHalter{}
		r = r.WithContext(context.WithValue(r.Context(), akteurKey, halter))

		rec := &statusMitschrift{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Erst nach der Antwort schreiben: Der Principal steht erst fest,
		// nachdem die auth-Middleware ihn in den Context gelegt hat, und das
		// Ergebnis (Status) ist Teil der Auskunft — ein abgewiesener Versuch,
		// eine Guard-Rail zu löschen, gehört genauso in die Spur wie ein
		// erfolgreicher.
		p := halter.p
		if p.ID == uuid.Nil {
			return // nicht angemeldet — kein Akteur, kein Eintrag
		}
		actor := p.ID
		eintrag := audit.Eintrag{
			OrgID: p.OrgID, ActorID: &actor, ActorEmail: p.Email, ActorRole: p.Role,
			Method: r.Method, Path: r.URL.Path, Status: rec.status, ClientIP: clientIP(r),
		}
		// Best effort: Die Handlung ist passiert; sie nachträglich scheitern zu
		// lassen, weil das Protokollieren klemmt, macht die Lage nicht besser.
		// Der Fehler gehört aber ins Log — eine stumm ausfallende Audit-Spur
		// ist schlimmer als keine, weil man sich auf sie verlässt.
		if err := s.Audit.Record(r.Context(), eintrag); err != nil {
			s.Log.Error("audit-eintrag nicht geschrieben — die Spur hat eine Lücke",
				"methode", r.Method, "pfad", r.URL.Path, "akteur", p.Email, "err", err)
		}
	})
}

// akteurHalter nimmt den angemeldeten Menschen von der auth-Middleware
// entgegen. Ein einfacher Zeiger genügt: Innerhalb eines Requests schreibt
// genau eine Stelle, und sie tut es, bevor der Handler antwortet.
type akteurHalter struct{ p identity.Principal }

const akteurKey ctxKey = "akteur"

// istVeraendernd: Lesen wird nicht protokolliert. Das ist eine bewusste Grenze
// — jeder Seitenaufruf der Oberfläche wäre sonst ein Eintrag und die Spur
// unlesbar. Wer WAS GESEHEN hat, beantwortet bei Bedarf das Request-Log.
func istVeraendernd(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// istAuditWuerdig blendet aus, was keine Verwaltungshandlung ist.
func istAuditWuerdig(pfad string) bool {
	if !strings.HasPrefix(pfad, "/api/v1/") {
		return false // Webhooks, statische Dateien
	}
	switch {
	// Anmelden/Abmelden hat keinen Akteur im Context (der entsteht erst
	// dadurch) und ist über den Login-Limiter ohnehin beobachtet.
	case strings.HasPrefix(pfad, "/api/v1/auth/"):
		return false
	}
	return true
}

// statusMitschrift merkt sich den Statuscode, den der Handler geschrieben hat.
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
	m.geschrieben = true // impliziter 200
	return m.ResponseWriter.Write(b)
}

// Flush reicht das Leeren durch — die SSE-Endpunkte brauchen es.
func (m *statusMitschrift) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handleAuditLog liefert die jüngsten Verwaltungshandlungen der Organisation.
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if s.Audit == nil {
		writeErr(w, http.StatusServiceUnavailable, "audit-spur ist in dieser Instanz nicht konfiguriert")
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
