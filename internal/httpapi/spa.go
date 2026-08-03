package httpapi

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
)

func init() {
	// Go kennt .webmanifest nicht ab Werk — sonst liefert der FileServer text/plain.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// platzhalterOrigin steht im vorgerenderten HTML überall dort, wo die Adresse
// dieser Installation hingehört (Canonical, hreflang, og:url, JSON-LD). Der
// Build kennt sie nicht — Covey wird selbst gehostet. Siehe seo.go.
const platzhalterOrigin = "__COVEY_ORIGIN__"

// spaHandler liefert die Website aus. Vier Fälle, in dieser Reihenfolge:
//
//  1. eine vorgerenderte öffentliche Seite (dist/funktion/index.html …),
//  2. eine statische Datei (Assets, Bilder, Manifest),
//  3. ein Pfad der angemeldeten Oberfläche → die SPA-Hülle, nicht indexierbar,
//  4. sonst: eine ehrliche 404.
//
// Punkt 4 ist der Unterschied zu früher. Bis dahin bekam jeder Pfad die
// index.html mit Status 200 — für Besucher unauffällig, für Suchmaschinen ein
// „Soft 404": beliebig viele Adressen, die alle dasselbe zeigen und alle als
// gültige Seite gelten. Ohne vorgerendertes dist/ (alter Build, Tests mit
// handgebautem FS) bleibt es beim alten Verhalten — siehe seoIndex.istAppPfad.
func (s *Server) spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /funktion/ und /funktion wären zwei Adressen mit demselben Inhalt.
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, strings.TrimRight(r.URL.Path, "/"), http.StatusMovedPermanently)
			return
		}

		pfad := strings.Trim(r.URL.Path, "/")

		seite := "index.html"
		if pfad != "" {
			seite = pfad + "/index.html"
		}
		if daten, err := fs.ReadFile(dist, seite); err == nil {
			s.schreibeHTML(w, r, daten, http.StatusOK)
			return
		}

		if pfad != "" {
			if info, err := fs.Stat(dist, pfad); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if s.seo.istAppPfad(r.URL.Path) {
			// Die Oberfläche selbst gehört nicht in den Index: Ohne Sitzung
			// zeigt sie nichts, und ihr Adressraum ist unendlich.
			w.Header().Set("X-Robots-Tag", "noindex")
			daten, err := fs.ReadFile(dist, "app.html")
			if err != nil {
				// Build ohne Vorrendern — dann ist index.html die Hülle.
				daten, err = fs.ReadFile(dist, "index.html")
			}
			if err != nil {
				http.NotFound(w, r)
				return
			}
			s.schreibeHTML(w, r, daten, http.StatusOK)
			return
		}

		datei := "404.html"
		if strings.HasPrefix(pfad, "en/") || pfad == "en" {
			datei = "en/404.html"
		}
		daten, err := fs.ReadFile(dist, datei)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.schreibeHTML(w, r, daten, http.StatusNotFound)
	})
}

// schreibeHTML setzt die Adresse dieser Installation in das vorgerenderte HTML
// ein und liefert es aus.
func (s *Server) schreibeHTML(w http.ResponseWriter, r *http.Request, daten []byte, status int) {
	html := strings.ReplaceAll(string(daten), platzhalterOrigin, s.origin(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

// setzeSchutzHeader gibt der Admin-Oberfläche die Kopfzeilen, die ein Browser
// braucht, um sie zu verteidigen. Sie fehlten bisher vollständig.
//
// Warum das mehr ist als Formalie: Die Oberfläche zeigt Inhalte, die Agenten
// aus fremden Quellen mitbringen — Ticket-Texte, Mails, Wiki-Seiten, Ausgaben
// von Zielsystemen. Sollte davon je etwas als Markup durchschlagen, ist die
// CSP die zweite Linie: Ohne sie könnte ein eingeschleustes Skript
// nachladen und Daten irgendwohin schicken; mit ihr nicht.
func setzeSchutzHeader(w http.ResponseWriter) {
	h := w.Header()
	// script-src 'self': Vite baut ein einziges Modul-Bundle, keine
	// Inline-Skripte — das reicht also ohne 'unsafe-inline'.
	// style-src braucht 'unsafe-inline', weil die Oberfläche durchgehend mit
	// style-Attributen arbeitet (React style={{…}}).
	// connect-src 'self': Die SPA spricht nur mit der eigenen API.
	// frame-ancestors 'none' + X-Frame-Options: kein Einbetten, also kein
	// Clickjacking auf Knöpfe wie „Stoppen" oder „Freigeben".
	// img-src data: für die eingebetteten Icons, blob: für Vorschau-Bilder.
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; "))
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// Keine Referer an Dritte — die URL trägt Agenten- und Aufgaben-IDs.
	h.Set("Referrer-Policy", "no-referrer")
	// Nichts davon braucht die Oberfläche; explizit abschalten, damit ein
	// eingeschleustes Skript es auch nicht bekommt.
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
}

// findWebhookAgent löst den Adress-Teil einer Webhook-URL auf (MVP: eine
// Organisation). Gemeint ist der Slug; ersatzweise wird die Agent-ID
// akzeptiert, weil sie in der UI-URL des Agenten steht und beim Einrichten
// eines Zielsystems leicht statt des Slugs kopiert wird. Beides adressiert
// denselben Agenten eindeutig — der Slug hat Vorrang, falls ein Slug wie eine
// UUID aussieht.
func (s *Server) findWebhookAgent(r *http.Request, ref string) (agents.Agent, error) {
	if a, err := s.Registry.FindBySlug(r.Context(), ref); err == nil {
		return a, nil
	}
	id, err := uuid.Parse(ref)
	if err != nil {
		return agents.Agent{}, agents.ErrNotFound
	}
	return s.Registry.Get(r.Context(), id)
}
