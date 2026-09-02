package httpapi

// Hiring: the act that turns a draft into an employee (spec/20).
//
// It is deliberately its own endpoint and not a flag on the agent update. A
// draft is created by all sorts of things — a template, an import, the People
// department — but there is exactly one way out of that state, and it runs
// through a human. That is also why the platform's own meta actions
// (internal/orchestrator/hiring.go) have no hire op: an agent may draft another
// agent, it may not employ one.

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/identity"
)

// handleRollName rolls an agent name. The generator lives in the binary because
// setup and the People department need it too; the interface asks here so that
// there is one pool and not two that drift apart (spec/20).
func (s *Server) handleRollName(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, agents.RollName(langFrom(r)))
}

func (s *Server) handleHire(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Idempotent: hiring somebody who already works here is not an error, it is a
	// second click. The date stays the day it happened.
	if !a.Draft() {
		writeJSON(w, http.StatusOK, a)
		return
	}
	if err := s.Registry.Hire(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	a, err = s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Whatever was queued against the draft has been waiting for this moment.
	// The heartbeat needs no push — the next tick sees the agent now.
	s.Orch.EnsureRunning(id)
	writeJSON(w, http.StatusOK, a)
}

// draftBlocked answers whether an agent may not be woken because it has not
// been hired yet, and writes the error if so. The wake paths a human triggers
// go through here; the dispatch loop makes the same decision in SQL.
func draftBlocked(w http.ResponseWriter, a agents.Agent) bool {
	if !a.Draft() {
		return false
	}
	writeErr(w, http.StatusConflict, "agent has not been hired yet")
	return true
}

// --- The brief: an agent form that ends in a task ---
//
// The old form asked a person for a role, a remit, target systems and scopes —
// four questions that presuppose knowing what the platform can do. Somebody
// setting covey up for the first time is precisely the person who does not know
// that yet (spec/20).
//
// So the interface asks one question — what should the new colleague do? — and
// sends the answer to the People department as an assignment. Modelled as a
// TASK and not as a synchronous call, because the interesting case is the one
// where the agent asks back: questions, answers and resumption already have a
// home here, and an HTTP request would have to invent one badly.

// handleHiringBrief creates the assignment. It deliberately fails loudly when
// the People department cannot work — the interface then falls back to the
// manual form and says why, instead of parking a task somewhere nobody looks.
func (s *Server) handleHiringBrief(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ctx := r.Context()
	var in struct {
		Description string `json:"description"`
		Department  string `json:"department"`
		Supervisor  string `json:"supervisor"`
		Runtime     string `json:"runtime"`
		Title       string `json:"title"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Description) == "" {
		writeErr(w, http.StatusBadRequest, "description is required")
		return
	}
	people, err := s.Registry.GetBySlug(ctx, p.OrgID, peopleSlug)
	if err != nil {
		writeErr(w, http.StatusPreconditionFailed,
			"this organisation has no People department — set one up under Setup, or fill the form in yourself")
		return
	}
	if people.Killed {
		writeErr(w, http.StatusPreconditionFailed, "the People department is stopped")
		return
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = briefTitle(langFrom(r), in.Description)
	}
	body := s.briefBody(ctx, p, langFrom(r), in.Description, in.Department, in.Supervisor, in.Runtime)

	task, err := s.Backlog.Create(ctx, p.OrgID, people.ID, title, body, "wizard", 3)
	if err != nil {
		mapErr(w, err)
		return
	}
	// A draft does not get woken — the assignment waits for the hiring. Say so
	// rather than leaving the interface waiting for a run that will not start.
	pending := people.Draft()
	if !pending {
		s.Orch.EnsureRunning(people.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"task": task, "agent": people, "waiting_for_hire": pending,
	})
}

// briefTitle: the first line of the description, shortened — a board with ten
// tasks all called "Hire" is a board nobody reads.
func briefTitle(lang, description string) string {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(description), "\n", 2)[0])
	if len(first) > 60 {
		first = strings.TrimSpace(first[:60]) + "…"
	}
	if strings.HasPrefix(strings.ToLower(lang), "de") {
		return "Einstellung: " + first
	}
	return "Hire: " + first
}

// briefBody assembles the assignment: what the human wrote, the company, and
// the frame. The available target systems come from the registry rather than
// from what somebody typed — that is the difference between a brief the agent
// can work from and a wish list.
func (s *Server) briefBody(ctx context.Context, p identity.Principal, lang, description, department, supervisor, runtime string) string {
	de := strings.HasPrefix(strings.ToLower(lang), "de")
	var b strings.Builder

	if de {
		b.WriteString("## Auftrag\n")
	} else {
		b.WriteString("## Assignment\n")
	}
	b.WriteString(strings.TrimSpace(description) + "\n\n")

	if o, err := s.Org.GetOrg(ctx, p.OrgID); err == nil && strings.TrimSpace(o.Description) != "" {
		if de {
			b.WriteString("## Unternehmen\n")
		} else {
			b.WriteString("## The company\n")
		}
		b.WriteString(o.Name + "\n" + strings.TrimSpace(o.Description) + "\n\n")
	}

	if de {
		b.WriteString("## Rahmen\n")
	} else {
		b.WriteString("## Frame\n")
	}
	line := func(deLabel, enLabel, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if de {
			b.WriteString("- " + deLabel + ": " + value + "\n")
		} else {
			b.WriteString("- " + enLabel + ": " + value + "\n")
		}
	}
	line("Abteilung", "Department", department)
	line("Vorgesetzt", "Supervisor", supervisor)
	line("Engine", "Engine", runtime)
	line("Angefragt von", "Requested by", p.Email)

	var enabled []string
	if s.Targets != nil {
		if plugins, err := s.Targets.List(ctx, p.OrgID); err == nil {
			for _, pl := range plugins {
				if pl.Enabled {
					enabled = append(enabled, pl.Name)
				}
			}
		}
	}
	if len(enabled) > 0 {
		line("Verfügbare Zielsysteme", "Available target systems", strings.Join(enabled, ", "))
	} else if de {
		b.WriteString("- Verfügbare Zielsysteme: keine angeschlossen\n")
	} else {
		b.WriteString("- Available target systems: none connected\n")
	}
	return b.String()
}

// handleHiringBriefStatus is what the interface watches while the hiring
// conversation runs: the state of the assignment, its notes, the question when
// the agent asks back — and the drafts that came out of it.
//
// The drafts are read off the recording, which is where the platform wrote them
// (hiring.go, rule 3). Deliberately not from what the agent reported: an ID a
// model hands back is an ID a model can invent.
func (s *Server) handleHiringBriefStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	task, err := s.Backlog.Get(r.Context(), taskID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if task.OrgID != p.OrgID {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	notes, err := s.Backlog.ListNotes(r.Context(), taskID)
	if err != nil {
		notes = nil
	}
	drafts := []agents.Agent{}
	rows, err := s.Pool.Query(r.Context(), `SELECT DISTINCT payload->>'drafted_agent'
		FROM recording_events WHERE task_id=$1 AND kind='lifecycle'
		  AND payload->>'status'='agent_drafted'`, taskID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) != nil {
				continue
			}
			id, perr := uuid.Parse(raw)
			if perr != nil {
				continue
			}
			if a, gerr := s.Registry.Get(r.Context(), id); gerr == nil {
				drafts = append(drafts, a)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task": task, "notes": notes, "drafts": drafts,
	})
}
