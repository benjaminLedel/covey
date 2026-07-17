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

// findAgentBySlug löst den Webhook-Slug auf (MVP: eine Organisation).
func (s *Server) findAgentBySlug(r *http.Request, slug string) (agents.Agent, error) {
	var orgID uuid.UUID
	if err := s.Pool.QueryRow(r.Context(),
		"SELECT org_id FROM agents WHERE slug=$1 LIMIT 1", slug).Scan(&orgID); err != nil {
		return agents.Agent{}, err
	}
	return s.Registry.GetBySlug(r.Context(), orgID, slug)
}
