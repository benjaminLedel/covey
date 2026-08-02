package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
)

func init() {
	// Go kennt .webmanifest nicht ab Werk — sonst liefert der FileServer text/plain.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// spaHandler liefert die eingebettete SPA aus; unbekannte Pfade fallen auf
// index.html zurück (client-seitiges Routing, spec/10).
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			r.URL.Path = "/" // Fallback → index.html
		}
		fileServer.ServeHTTP(w, r)
	})
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
