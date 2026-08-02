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
