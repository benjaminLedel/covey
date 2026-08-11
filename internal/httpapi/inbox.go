package httpapi

// Der Posteingang: alles, was auf die Entscheidung eines Menschen wartet.
//
// Zwei Dinge landeten hier zusammen, die verschieden sind und dieselbe
// Handbewegung brauchen:
//
//   - Die FREIGABE (spec/06). Eine Guard-Rail hat mitten in einer Aktion
//     angeschlagen, die Aufgabe steht `blocked`, der AGENT WARTET. Freigeben
//     weckt ihn, Ablehnen auch — nur ohne die Aktion.
//   - Der OFFENE PUNKT (spec/21). Ein Review ist fertig, nichts blockiert.
//     Annehmen schreibt eine Config-Version, Ablehnen behält den Grund.
//
// Zusammengelegt wird die ANSICHT, nicht das Objekt: unterschiedliche Rollen
// (Controlling darf Freigaben lesen, Arbeitsakten nicht), unterschiedliche
// Verben, unterschiedliche Dringlichkeit. Deshalb eine Abfrage über beide
// Tabellen für Reihenfolge, Filter und Seiten — und die Entscheidung bleibt
// auf den beiden Endpunkten, die sie schon hatten.
//
// Die Sortierung `urgent` ist der Grund, warum das eine Abfrage sein muss und
// nicht zwei Listen nebeneinander: oben steht, was am längsten wartet, und
// eine blockierte Aufgabe wartet teurer als ein Vorschlag.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/identity"
	"covey/internal/observability"
)

// inboxEntry ist eine Zeile der Liste. Der Kopf ist für beide Sorten gleich,
// damit sortiert und geblättert werden kann; das Sortenspezifische hängt
// unverändert darunter — die Oberfläche kennt beide Typen ohnehin.
type inboxEntry struct {
	Type      string     `json:"type"` // approval | proposal | finding | issue
	ID        uuid.UUID  `json:"id"`
	AgentID   uuid.UUID  `json:"agent_id"`
	AgentSlug string     `json:"agent_slug"`
	AgentName string     `json:"agent_name"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Pending   bool       `json:"pending"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`

	Approval *observability.Approval `json:"approval,omitempty"`
	Item     *improvementView        `json:"item,omitempty"`
}

type inboxPage struct {
	Items []inboxEntry `json:"items"`
	// Total sind alle Zeilen, auf die die Filter passen (ohne limit/offset) —
	// die Zahl, aus der „mehr laden" weiß, ob es noch etwas gibt.
	Total int `json:"total"`
	// Pending zählt die offenen Zeilen unter denselben Filtern OHNE den
	// Statusfilter: der Zähler an der Navigation soll nicht davon abhängen,
	// was gerade angezeigt wird.
	Pending int `json:"pending"`
}

// inboxSorts ist die Weißliste. Eine Sortierung aus dem Query-String direkt in
// ein ORDER BY zu schreiben wäre die Injektion, die man sich sonst über Jahre
// spart.
var inboxSorts = map[string]string{
	// Die Voreinstellung des Entscheidungs-Kopfes: offen vor entschieden,
	// Freigaben vor allem anderen (dort wartet ein Agent), dann das Älteste
	// zuerst — was am längsten liegt, kostet am meisten.
	"urgent": `ORDER BY (status <> 'pending'), (type <> 'approval'), created_at ASC`,
	"newest": `ORDER BY created_at DESC`,
	"oldest": `ORDER BY created_at ASC`,
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	q := r.URL.Query()

	sortKey := strings.TrimSpace(q.Get("sort"))
	order, ok := inboxSorts[sortKey]
	if !ok {
		order = inboxSorts["urgent"]
	}
	typ := strings.TrimSpace(q.Get("type"))
	if typ != "" && typ != "approval" && typ != agents.KindProposal &&
		typ != agents.KindFinding && typ != agents.KindIssue {
		writeErr(w, http.StatusBadRequest, "unknown type "+typ)
		return
	}
	status := strings.TrimSpace(q.Get("status")) // "" | open | decided
	if status != "" && status != "open" && status != "decided" {
		writeErr(w, http.StatusBadRequest, "status has to be open or decided")
		return
	}
	limit := clampInt(q.Get("limit"), 20, 1, 200)
	offset := clampInt(q.Get("offset"), 0, 0, 100000)

	agentIDs, ok := s.inboxAgentFilter(w, r)
	if !ok {
		return
	}

	// Controlling sieht die Arbeitsakten-Seite nicht (spec/21): ein
	// Kostenblatt sagt, was ausgegeben wurde, ein Vorschlag sagt, wie jemand
	// gearbeitet hat. Die Freigaben bleiben ihm.
	seeItems := p.Role != identity.RoleControlling

	args := []any{p.OrgID, seeItems, typ, status, agentIDs}
	const cte = `WITH entries AS (
		SELECT id, 'approval'::text AS type, agent_id, task_id, status,
		       requested_at AS created_at, decided_at, action AS title
		FROM approvals WHERE org_id=$1
		UNION ALL
		SELECT id, kind, agent_id, task_id, status, created_at, decided_at, title
		FROM improvement_items WHERE org_id=$1 AND $2
	), gefiltert AS (
		SELECT * FROM entries
		WHERE ($3='' OR type=$3)
		  AND ($5::uuid[] IS NULL OR agent_id = ANY($5))
	)`

	var page inboxPage
	if err := s.Pool.QueryRow(r.Context(), cte+`
		SELECT count(*) FILTER (WHERE $4='' OR ($4='open') = (status='pending')),
		       count(*) FILTER (WHERE status='pending')
		FROM gefiltert`, args...).Scan(&page.Total, &page.Pending); err != nil {
		mapErr(w, err)
		return
	}

	rows, err := s.Pool.Query(r.Context(), fmt.Sprintf(`%s
		SELECT id, type, agent_id, task_id, status, created_at, decided_at, title
		FROM gefiltert
		WHERE ($4='' OR ($4='open') = (status='pending'))
		%s LIMIT %d OFFSET %d`, cte, order, limit, offset), args...)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer rows.Close()
	page.Items = []inboxEntry{}
	for rows.Next() {
		var e inboxEntry
		if err := rows.Scan(&e.ID, &e.Type, &e.AgentID, &e.TaskID, &e.Status,
			&e.CreatedAt, &e.DecidedAt, &e.Title); err != nil {
			mapErr(w, err)
			return
		}
		e.Pending = e.Status == "pending"
		page.Items = append(page.Items, e)
	}
	if err := rows.Err(); err != nil {
		mapErr(w, err)
		return
	}

	s.hydrateInbox(r, page.Items)
	writeJSON(w, http.StatusOK, page)
}

// inboxAgentFilter wertet `agent` und `mine` aus. nil = keine Einschränkung;
// die leere (nicht-nil) Liste heißt „keiner" und liefert nichts — der
// Unterschied trägt die Sicht dessen, der keinen Agenten besitzt.
func (s *Server) inboxAgentFilter(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	p := principalFrom(r)
	q := r.URL.Query()
	if raw := strings.TrimSpace(q.Get("agent")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent id")
			return nil, false
		}
		// Fremde Organisation: keine Auskunft, auch nicht darüber, dass es den
		// Agenten gibt.
		a, err := s.Registry.Get(r.Context(), id)
		if err != nil || a.OrgID != p.OrgID {
			writeErr(w, http.StatusNotFound, "agent not found")
			return nil, false
		}
		return []uuid.UUID{id}, true
	}
	if q.Get("mine") != "1" {
		return nil, true
	}
	owned, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return nil, false
	}
	ids := []uuid.UUID{}
	for _, a := range owned {
		if a.OwnerID != nil && *a.OwnerID == p.ID {
			ids = append(ids, a.ID)
		}
	}
	return ids, true
}

// hydrateInbox lädt das Sortenspezifische für die Zeilen DIESER Seite nach:
// eine Abfrage je Sorte statt einer je Zeile.
func (s *Server) hydrateInbox(r *http.Request, entries []inboxEntry) {
	ctx := r.Context()
	p := principalFrom(r)

	var approvalIDs, itemIDs []uuid.UUID
	for _, e := range entries {
		if e.Type == "approval" {
			approvalIDs = append(approvalIDs, e.ID)
		} else {
			itemIDs = append(itemIDs, e.ID)
		}
	}

	approvals := map[uuid.UUID]observability.Approval{}
	for _, id := range approvalIDs {
		// Der Store kennt die Einzelabfrage bereits mit Org-Prüfung; bei
		// höchstens 200 Zeilen je Seite ist das die kleinere Änderung als ein
		// zweiter Lesepfad neben ihr.
		if a, err := s.Obs.GetApproval(ctx, p.OrgID, id); err == nil {
			approvals[id] = a
		}
	}

	views := map[uuid.UUID]improvementView{}
	if len(itemIDs) > 0 {
		raw := make([]agents.ImprovementItem, 0, len(itemIDs))
		for _, id := range itemIDs {
			if it, err := s.Registry.GetImprovement(ctx, id); err == nil && it.OrgID == p.OrgID {
				raw = append(raw, it)
			}
		}
		for _, v := range s.improvementViews(r, raw) {
			views[v.ID] = v
		}
	}

	// Die Namen der Kollegen einmal je Agent.
	names := map[uuid.UUID]agents.Agent{}
	for i := range entries {
		e := &entries[i]
		a, ok := names[e.AgentID]
		if !ok {
			if got, err := s.Registry.Get(ctx, e.AgentID); err == nil {
				a, names[e.AgentID] = got, got
			}
		}
		e.AgentSlug, e.AgentName = a.Slug, a.DisplayName
		if appr, ok := approvals[e.ID]; ok {
			cp := appr
			e.Approval = &cp
		}
		if v, ok := views[e.ID]; ok {
			cp := v
			e.Item = &cp
		}
	}
}

func clampInt(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
