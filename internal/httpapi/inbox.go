package httpapi

// The inbox: everything that waits for the decision of a human.
//
// Two things landed here together that are different and need the same hand
// movement:
//
//   - The APPROVAL (spec/06). A guard rail tripped in the middle of an
//     action, the task stands `blocked`, the AGENT WAITS. Approving wakes
//     him, rejecting does too — only without the action.
//   - The OPEN POINT (spec/21). A review is finished, nothing blocks.
//     Accepting writes a config version, rejecting keeps the reason.
//
// What is merged is the VIEW, not the object: different roles (controlling
// may read approvals, work records not), different verbs, different
// urgency. That is why one query runs over both tables for the order, the
// filter and the paging — and the decision stays on the two endpoints that
// already had it.
//
// The sort `urgent` is the reason this has to be one query and not two lists
// side by side: at the top stands what waits the longest, and a blocked task
// waits more expensively than a proposal.

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

// inboxEntry is one row of the list. The head is the same for both kinds so
// that sorting and paging can work over them; the kind-specific part hangs
// unchanged underneath — the surface knows both types anyway.
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
	// Total is all rows that the filters match (without limit/offset) — the
	// number from which "load more" knows whether there is anything left.
	Total int `json:"total"`
	// Pending counts the open rows under the same filters WITHOUT the status
	// filter: the counter at the navigation should not depend on what is
	// currently on screen.
	Pending int `json:"pending"`
}

// inboxSorts is the allowlist. Writing a sort from the query string straight
// into an ORDER BY would be the injection that you otherwise spend years
// not meeting.
var inboxSorts = map[string]string{
	// The default of the decision head: open before decided, approvals before
	// everything else (an agent waits there), then the oldest first — what has
	// lain longest costs the most.
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

	// Controlling does not see the work-record side (spec/21): a cost sheet
	// says what was spent, a proposal says how someone worked. The approvals
	// stay with him.
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

// inboxAgentFilter evaluates `agent` and `mine`. nil = no restriction; the
// empty (non-nil) list means "none" and returns nothing — the difference
// carries the view of the one who owns no agent.
func (s *Server) inboxAgentFilter(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	p := principalFrom(r)
	q := r.URL.Query()
	if raw := strings.TrimSpace(q.Get("agent")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent id")
			return nil, false
		}
		// Foreign organisation: no information, not even that the agent
		// exists.
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

// hydrateInbox loads the kind-specific part for the rows of THIS page: one
// query per kind instead of one per row.
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
		// The store already knows the single query with an org check; with at
		// most 200 rows per page this is the smaller change than a second read
		// path beside it.
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

	// The names of the colleagues, once per agent.
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
