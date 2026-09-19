package httpapi

// Was gerade läuft, org-weit.
//
// Der Überblick zeigte bisher, WER arbeitet, und das ist die halbe Auskunft:
// Ein Gesicht mit offenen Augen sagt „an" und nicht „woran". Die andere
// Hälfte liegt schon in der Datenbank — die laufende Aufgabe, ihr Titel, seit
// wann, und der zuletzt aufgezeichnete Schritt. Zusammengesetzt ist das die
// einzige Zeile dieser Oberfläche, die sich ändert, während man sie ansieht.
//
// Bewusst keine Animation um ihrer selbst willen: Was auf einer Seite steht,
// die jemand jeden Morgen öffnet, muss sich von allein ändern, sonst sieht
// man ab Tag zwei daran vorbei.

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// laufend ist eine Aufgabe, die gerade abgearbeitet wird.
type laufend struct {
	TaskID    uuid.UUID `json:"task_id"`
	Title     string    `json:"title"`
	AgentID   uuid.UUID `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	AgentSlug string    `json:"agent_slug"`
	/* Seit wann sie läuft — als Zeitpunkt, nicht als Dauer: Eine Dauer vom
	   Server ist in dem Moment falsch, in dem sie ankommt, und die Oberfläche
	   zählt ohnehin selbst weiter. */
	Since string `json:"since"`
	/* Die Art des letzten aufgezeichneten Schritts (runtime | tool_use |
	   credential | approval | guardrail | lifecycle | action). Nur die Art,
	   nicht der Inhalt: Was ein Agent gerade tut, steht in der Aufzeichnung
	   und gehört dorthin — hier soll eine Zeile stehen, keine Akte. */
	Step string `json:"step,omitempty"`
}

func (s *Server) handleRunning(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rows, err := s.Pool.Query(r.Context(), `
		SELECT t.id, t.title, a.id, a.display_name, a.slug, t.updated_at,
		       coalesce((SELECT e.kind FROM recording_events e
		                  WHERE e.task_id = t.id ORDER BY e.id DESC LIMIT 1), '')
		FROM backlog_tasks t
		JOIN agents a ON a.id = t.agent_id
		WHERE t.org_id = $1 AND t.state = 'in_progress'
		ORDER BY t.updated_at DESC
		LIMIT 20`, p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer rows.Close()

	out := []laufend{}
	for rows.Next() {
		var l laufend
		var seit time.Time
		if err := rows.Scan(&l.TaskID, &l.Title, &l.AgentID, &l.AgentName, &l.AgentSlug, &seit, &l.Step); err != nil {
			mapErr(w, err)
			return
		}
		l.Since = seit.Format(time.RFC3339)
		out = append(out, l)
	}
	if rows.Err() != nil {
		mapErr(w, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
