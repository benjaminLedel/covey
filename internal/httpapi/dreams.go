package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"covey/internal/dream"
)

// Träume (spec/05). Der Agent räumt sein Gedächtnis auf — nachts von selbst,
// tagsüber auf Knopfdruck. Die Endpunkte hier sind die Naht: starten, zusehen,
// nachlesen, zurücknehmen. Die Arbeit selbst liegt in internal/dream.

// handleStartDream weckt einen Traum. Kehrt sofort zurück; der Traum läuft im
// Hintergrund und wird über die Historie verfolgt. Träumt der Agent schon,
// kommt der laufende zurück statt eines zweiten — jeder Traum kostet einen
// LLM-Aufruf.
// dreamStore gibt den Traum-Store heraus oder beantwortet die Anfrage. Wie bei
// den Skills ist er optional: Eine Instanz ohne konfigurierten Nachtlauf soll
// mit 503 antworten und nicht mit einem Nil-Pointer-Absturz, der die ganze
// Verbindung reißt.
func (s *Server) dreamStore(w http.ResponseWriter) (*dream.Store, bool) {
	if s.Dreams == nil {
		writeErr(w, http.StatusServiceUnavailable, "träume sind in dieser Instanz nicht konfiguriert")
		return nil, false
	}
	return s.Dreams, true
}

func (s *Server) handleStartDream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	p := principalFrom(r)
	cred, oauth, ok := s.resolveOrgClaude(r.Context(), p.OrgID)
	if !ok {
		writeErr(w, http.StatusPreconditionFailed,
			"kein org-weites Claude-Credential hinterlegt — der Agent kann nicht träumen")
		return
	}

	d, err := s.Dreams.Begin(r.Context(), id, "manual")
	if err == dream.ErrAsleep {
		cur, _, cerr := s.Dreams.Current(r.Context(), id)
		if cerr != nil {
			mapErr(w, cerr)
			return
		}
		writeJSON(w, http.StatusOK, cur)
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	// Bewusst nicht an r.Context() gehängt: Der Request ist gleich beendet, der
	// Traum soll es nicht sein — er läuft Minuten. Aber auch nicht an
	// context.Background(): Dann überlebte er ein Herunterfahren als Goroutine,
	// die niemand mehr abbricht und auf die niemand wartet. Der Lebenszyklus
	// des Servers ist genau das richtige Maß dazwischen.
	go func() {
		ctx, cancel := context.WithTimeout(s.baseCtx(), 10*time.Minute)
		defer cancel()
		s.Dreams.Run(ctx, d, dream.Credential{Value: cred, OAuth: oauth})
	}()
	writeJSON(w, http.StatusAccepted, d)
}

// handleListDreams liefert die Traum-Historie: was der Agent in den letzten
// Nächten mit seinem Gedächtnis gemacht hat, Handlung für Handlung.
func (s *Server) handleListDreams(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	list, err := s.Dreams.List(r.Context(), id, 30)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleUndoDreamAction nimmt eine einzelne Handlung zurück.
func (s *Server) handleUndoDreamAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	actionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.Dreams.Undo(r.Context(), actionID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
