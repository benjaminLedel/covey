package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/runner"
	"covey/internal/sandbox"
	"covey/internal/secrets"
)

// --- Agents ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []agents.Agent{}
	}
	phasen := s.phases()
	out := make([]agentWithPhase, 0, len(list))
	for _, a := range list {
		e := agentWithPhase{Agent: a}
		if ph, ok := phasen[a.ID]; ok {
			p := ph
			e.Phase = &p
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// agentWithPhase hängt an einen Agenten, worauf er in diesem Moment wartet: ein
// Bild wird geholt, ein Home hergestellt, ein Home zurückgeschrieben.
//
// Das gehört nicht in agents.Agent — der Agent ist ein Datensatz, die Phase ist
// Live-Zustand aus der Datenebene und steht in keiner Tabelle. Zusammengeführt
// wird erst für die Ansicht, eingebettet, damit der Agent im JSON flach bleibt.
//
// Ein Zeiger, weil „gerade nichts" nicht dasselbe ist wie eine Phase mit leeren
// Feldern: der Normalfall ist, dass ein Agent auf nichts wartet.
type agentWithPhase struct {
	agents.Agent
	Phase *runner.Phase `json:"phase,omitempty"`
}

// phases: was die Hosts gerade tun, oder nichts, wenn diese Installation keinen
// Pool hat (die Tests hängen den Server ohne Datenebene ein).
func (s *Server) phases() map[uuid.UUID]runner.Phase {
	if s.RunnerPool == nil {
		return nil
	}
	return s.RunnerPool.Phases.All()
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Runtime     string `json:"runtime"`
	}
	if err := readJSON(r, &in); err != nil || in.Slug == "" || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "slug and display_name are required")
		return
	}
	// Als Entwurf (spec/20). „Später fertig machen" hat hier bis eben einen
	// halb konfigurierten Agenten hinterlassen, der bereits scharf war — und das
	// ist genau der Weg, auf dem die wenigste Konfiguration entsteht: ein Slug,
	// ein Name, sonst nichts. Erst das Einstellen macht daraus einen Kollegen.
	a, err := s.Registry.CreateDraft(r.Context(), p.OrgID, in.Slug, in.DisplayName, in.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Every agent starts out with a default board. Best-effort: the agent is
	// already created, a seeding error must not topple that.
	if err := s.Backlog.SeedDefaultStages(r.Context(), a.ID); err != nil {
		s.Log.Warn("seeding default stages failed", "agent", a.ID, "err", err)
	}
	// And on a workplace, set up from whatever is already deposited. The simple
	// case — one token, one agent — must not require creating a contract by
	// hand first; that model earns its keep with the SECOND credential and
	// should carry the first one silently (spec/18).
	s.attachDefaultRuntime(r.Context(), p.OrgID, a)
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
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
	out := agentWithPhase{Agent: a}
	if s.RunnerPool != nil {
		if ph, ok := s.RunnerPool.Phases.Of(a.ID); ok {
			out.Phase = &ph
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Registry.Delete(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Config-as-Code (M2) ---

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	cv, err := s.Registry.CurrentConfig(r.Context(), id)
	if errors.Is(err, agents.ErrNotFound) {
		// No version yet: empty files, so that ACCESS.md/EGRESS.md
		// (rendered live) are visible anyway.
		cv = agents.ConfigVersion{AgentID: id, Files: map[string]string{"SOUL.md": "", "HEARTBEAT.md": ""}}
	} else if err != nil {
		mapErr(w, err)
		return
	}
	// ACCESS.md and EGRESS.md are the text view of the UI stores and are
	// rendered live — never served from the version snapshot (configsync.go).
	s.overlayLiveFiles(r.Context(), p.OrgID, id, cv.Files)
	delete(cv.Files, "TOOLS.md") // legacy: absorbed into ACCESS.md
	writeJSON(w, http.StatusOK, cv)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Files map[string]string `json:"files"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "files missing")
		return
	}
	s.saveAndApplyConfig(w, r, id, in.Files)
}

// saveAndApplyConfig stores files as a new config version and carries
// ACCESS.md/EGRESS.md over into the UI stores (write-through) — the shared core
// of PUT /config and the bundle config import. Validate and check RBAC first,
// so that a faulty file never produces a version; on success the new
// ConfigVersion (200) is written.
func (s *Server) saveAndApplyConfig(w http.ResponseWriter, r *http.Request, id uuid.UUID, files map[string]string) {
	apply, ok := s.prepareConfigWrite(w, r, id, files)
	if !ok {
		return
	}
	s.commitConfig(w, r, id, files, apply)
}

// prepareConfigWrite checks everything that has to be settled before the first
// write (file formats, RBAC for tools/egress) and returns the write-through
// function. Errors have already been answered; ok=false means: do nothing more.
//
// Separate from the writing because the bundle import also creates skills
// alongside, and both need a common order: ALL checks first, then all side
// effects. Otherwise a 403 on the config leaves already created skills behind,
// and the caller believes nothing happened.
func (s *Server) prepareConfigWrite(w http.ResponseWriter, r *http.Request, id uuid.UUID,
	files map[string]string) (func(context.Context) error, bool) {
	p := principalFrom(r)
	if _, ok := files["TOOLS.md"]; ok {
		writeErr(w, http.StatusBadRequest, "TOOLS.md has been absorbed into ACCESS.md (tools: attribute per system)")
		return nil, false
	}
	// Validate HEARTBEAT.md up front: a parse error should come back as a 400
	// with a comprehensible message instead of failing later in SaveConfig.
	if _, err := agents.ParseHeartbeat(files["HEARTBEAT.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "HEARTBEAT.md: "+err.Error())
		return nil, false
	}
	if _, err := agents.ParseKPIs(files["KPIS.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "KPIS.md: "+err.Error())
		return nil, false
	}
	// Write-through into the UI stores (tools, egress) — validate and check
	// RBAC first, so that a faulty file never produces a version.
	canSecurity := p.Role == identity.RoleOrgAdmin || p.Role == identity.RoleSecurity
	apply, err := s.prepareConfigApply(r.Context(), p.OrgID, id, files, canSecurity)
	if err != nil {
		if errors.Is(err, errNeedsSecurityRole) {
			writeErr(w, http.StatusForbidden, err.Error())
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return nil, false
	}
	return apply, true
}

// commitConfig writes the new version and runs the write-through.
func (s *Server) commitConfig(w http.ResponseWriter, r *http.Request, id uuid.UUID,
	files map[string]string, apply func(context.Context) error) {
	p := principalFrom(r)
	cv, err := s.Registry.SaveConfig(r.Context(), id, files, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := apply(r.Context()); err != nil {
		s.Log.Error("config write-through", "agent", id, "err", err)
		writeErr(w, http.StatusInternalServerError, "version saved, but applying it to tools/egress failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cv)
}

// handleHeartbeats returns the agent's materialized heartbeats including the
// computed next run — the monitoring view of HEARTBEAT.md.
func (s *Server) handleHeartbeats(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	hbs, err := s.Registry.Heartbeats(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hbs)
}

// handleFireHeartbeat fires a heartbeat right away, independent of the
// schedule (button in the UI). It creates the backlog task and wakes the agent;
// last_fired_at is advanced, so the regular schedule counts from now.
func (s *Server) handleFireHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	name := r.PathValue("name")
	orgID, body, err := s.Registry.FireHeartbeat(r.Context(), id, name)
	switch {
	case errors.Is(err, agents.ErrHeartbeatPending):
		writeErr(w, http.StatusConflict, "The task of the last run is still open — finish or cancel it first.")
		return
	case errors.Is(err, agents.ErrAgentKilled):
		writeErr(w, http.StatusConflict, "Agent or fleet is stopped — resume it first.")
		return
	case err != nil:
		mapErr(w, err)
		return
	}
	t, err := s.Backlog.Create(r.Context(), orgID, id, name, body, "heartbeat", 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- Backlog (M3) ---

func (s *Server) handleBacklog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	// ?q= searches instead of listing — title, body and what the run left
	// behind, the archive included. Whoever searches is looking for a task that
	// is no longer on the board; a search that stopped at the board's edge would
	// only ever find what one can already see. Without q: the board as before.
	var tasks []backlog.Task
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		tasks, err = s.Backlog.SearchByAgent(r.Context(), id, q, backlog.SearchMaxResults)
	} else {
		tasks, err = s.Backlog.ListByAgent(r.Context(), id, r.URL.Query().Get("archived") == "1")
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	costs, err := s.Obs.CostByTasks(r.Context(), ids)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]taskWithCost, 0, len(tasks))
	for _, t := range tasks {
		e := taskWithCost{Task: t}
		if c, ok := costs[t.ID]; ok {
			e.CostUSD = &c.TotalUSD
			e.CostEntries = c.Entries
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// taskWithCost hängt an eine Backlog-Aufgabe, was ihr Lauf gekostet hat. Die
// Zahl gehört nicht in backlog.Task — der Backlog weiß nichts von Kosten, das
// ist die Observability. Sie wird hier erst für die Ansicht zusammengeführt,
// eingebettet, damit die Aufgabe im JSON flach bleibt.
//
// CostUSD ist ein Zeiger: eine Aufgabe, die noch nicht gelaufen ist, hat keine
// Kosten — und das ist nicht 0,00 $, sondern „noch nichts". Die Oberfläche
// blendet das Feld dann aus, statt eine Null zu behaupten.
type taskWithCost struct {
	backlog.Task
	CostUSD     *float64 `json:"cost_usd,omitempty"`
	CostEntries int64    `json:"cost_entries,omitempty"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Priority int    `json:"priority"`
	}
	if err := readJSON(r, &in); err != nil || in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	t, err := s.Backlog.Create(r.Context(), p.OrgID, id, in.Title, in.Body, "manual:"+p.Email, in.Priority)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	t, err := s.Backlog.Cancel(r.Context(), id, "discarded by "+p.Email)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleRetryTask schedules a failed/discarded task again.
func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	t, err := s.Backlog.Retry(r.Context(), id, "rescheduled by "+p.Email)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleArchiveTask hides a terminal task from the active backlog.
func (s *Server) handleArchiveTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := s.Backlog.Archive(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleCleanupBacklog archives all terminal tasks of an agent.
func (s *Server) handleCleanupBacklog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	n, err := s.Backlog.ArchiveTerminal(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"archived": n})
}

// --- Stages (kanban overlay) ---

func (s *Server) handleListStages(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	stages, err := s.Backlog.ListStages(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if stages == nil {
		stages = []backlog.Stage{}
	}
	writeJSON(w, http.StatusOK, stages)
}

func (s *Server) handleCreateStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	st, err := s.Backlog.CreateStage(r.Context(), id, in.Name, in.Color)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func (s *Server) handleUpdateStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		Position int    `json:"position"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	st, err := s.Backlog.UpdateStage(r.Context(), id, in.Name, in.Color, in.Position)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDeleteStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Backlog.DeleteStage(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderStages(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Order []uuid.UUID `json:"order"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "order is required")
		return
	}
	if err := s.Backlog.ReorderStages(r.Context(), id, in.Order); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		StageID *uuid.UUID `json:"stage_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "stage_id missing")
		return
	}
	t, err := s.Backlog.SetTaskStage(r.Context(), id, in.StageID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTransitions(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	trs, err := s.Backlog.Transitions(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trs)
}

// handleTaskNotes returns the agent's proactive notes on a task.
func (s *Server) handleTaskNotes(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	notes, err := s.Backlog.ListNotes(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if notes == nil {
		notes = []backlog.Note{}
	}
	writeJSON(w, http.StatusOK, notes)
}

// --- Lifecycle control ---

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if a, err := s.Registry.Get(r.Context(), id); err == nil && draftBlocked(w, a) {
		return
	}
	s.Orch.EnsureRunning(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Orch.Kill(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResumeAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Registry.SetKilled(r.Context(), id, false); err != nil {
		mapErr(w, err)
		return
	}
	s.Orch.EnsureRunning(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetBudget(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := readJSON(r, &in); err != nil || in.BudgetUSD < 0 {
		writeErr(w, http.StatusBadRequest, "budget_usd missing")
		return
	}
	if err := s.Registry.SetBudget(r.Context(), id, in.BudgetUSD); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleListRuntimes returns the runtime plugins registered in the daemon
// (spec/12) — the same registry the daemon uses to run them.
func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, daemon.Runtimes())
}

// handleRename changes an agent's display name. The slug stays stable.
// checkDoctorIdentity hält Name und Slug von Covey Doctor fest und gibt
// die Meldung für einen 409 zurück (leer = in Ordnung).
//
// Zentral erzwungen und nicht in der Oberfläche: ein deaktiviertes Eingabefeld
// ist eine Bitte, keine Leitplanke. Es geht nicht um Ästhetik — der Doctor darf
// jeden Kollegen lesen und für ihn Änderungen vorschlagen, und wer ihm einen
// unauffälligen Namen gäbe, hätte einen Agenten mit diesen Rechten, den im
// Org-Chart niemand als solchen erkennt.
//
// Leere Argumente heißen „wird nicht geändert" — so kann jeder der beiden
// Endpunkte dieselbe Prüfung mit seinem einen Feld aufrufen.
func (s *Server) checkDoctorIdentity(ctx context.Context, id uuid.UUID, name, slug string) string {
	a, err := s.Registry.Get(ctx, id)
	if err != nil || !agents.IsDoctor(a) {
		// Kein Doctor (oder nicht lesbar) — dann entscheidet der Endpunkt wie
		// bisher, inklusive seiner eigenen Fehlerbehandlung.
		return ""
	}
	if name != "" && name != agents.DoctorName {
		return "the operations engineer is always called " + agents.DoctorName + " — the name is part of the platform, not of the organisation"
	}
	if slug != "" && slug != agents.DoctorSlug {
		return "the operations engineer keeps the slug " + agents.DoctorSlug +
			" — renaming it would be the detour around the fixed name"
	}
	return ""
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		DisplayName string `json:"display_name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.DisplayName) == "" {
		writeErr(w, http.StatusBadRequest, "display_name missing")
		return
	}
	if msg := s.checkDoctorIdentity(r.Context(), id, strings.TrimSpace(in.DisplayName), ""); msg != "" {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	if err := s.Registry.Rename(r.Context(), id, strings.TrimSpace(in.DisplayName)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleUpdateAgentProfile writes an agent's employee master data — the same
// profile fields as for humans (profilePatch); the values show up in the org
// chart and in the agents' covey/org_chart query.
func (s *Server) handleUpdateAgentProfile(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in profilePatch
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	a, err := s.Registry.UpdateProfile(r.Context(), p.OrgID, id, agents.ProfileUpdate{
		JobTitle:         trimPtr(in.JobTitle),
		Identities:       in.Identities,
		Phone:            trimPtr(in.Phone),
		Responsibilities: trimPtr(in.Responsibilities),
		Custom:           in.Custom,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleSetSlug changes an agent's slug. It has to be unique within the org.
func (s *Server) handleSetSlug(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Slug string `json:"slug"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Slug) == "" {
		writeErr(w, http.StatusBadRequest, "slug missing")
		return
	}
	if msg := s.checkDoctorIdentity(r.Context(), id, "", strings.TrimSpace(in.Slug)); msg != "" {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	if err := s.Registry.SetSlug(r.Context(), id, strings.TrimSpace(in.Slug)); err != nil {
		// Matched against the wording of agents.Registry.SetSlug: a slug already
		// taken or a bad format is a conflict, not a server error.
		if strings.Contains(err.Error(), "already taken") || strings.Contains(err.Error(), "Format") {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRuntime switches an agent's runtime (over to 'mock' for free demos,
// for example). Takes effect at the next task dispatch.
func (s *Server) handleSetRuntime(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Runtime string `json:"runtime"`
	}
	if err := readJSON(r, &in); err != nil || in.Runtime == "" {
		writeErr(w, http.StatusBadRequest, "runtime missing")
		return
	}
	if !daemon.IsRuntime(in.Runtime) {
		writeErr(w, http.StatusBadRequest, "unknown runtime")
		return
	}
	if err := s.Registry.SetRuntime(r.Context(), id, in.Runtime); err != nil {
		mapErr(w, err)
		return
	}
	// Der Denkaufwand gehört der Engine, nicht dem Agenten: `xhigh` ist eine
	// Claude-Code-Stufe. Wer die Engine wechselt, nimmt die Stufe nicht mit —
	// sie stünde sonst weiter im Profil, ohne dass sie noch jemand liest.
	// Zurück auf den Default der neuen Engine, still, aber nicht heimlich: das
	// Feld zeigt danach sichtbar „leer = Runtime-Default".
	a, getErr := s.Registry.Get(r.Context(), id)
	if getErr == nil && !daemon.AcceptsEffort(in.Runtime, a.Effort) {
		if err := s.Registry.SetEffort(r.Context(), id, ""); err != nil {
			s.Log.Warn("effort reset on runtime change", "agent", id, "err", err)
		}
	}
	// Dasselbe für das Modell, und aus demselben Grund: `claude-sonnet-5` ist
	// kein Modell, das ein Gateway routen muss. Wer die Engine wechselt, nimmt
	// die Modellwahl nur mit, wenn die neue Engine sie kennt.
	if getErr == nil && !daemon.AcceptsModel(in.Runtime, a.Model) {
		if err := s.Registry.SetModel(r.Context(), id, ""); err != nil {
			s.Log.Warn("model reset on runtime change", "agent", id, "err", err)
		}
	}
	// Und der SITZ, der von den dreien am meisten wiegt: er trägt die
	// Zugangsdaten. Blieb er stehen, bekam der Agent den Zugang einer FREMDEN
	// Engine gebrokert — unter deren Variable, mit deren Secret — und die neue
	// Engine meldete „nicht angemeldet", was auf das Token zeigt statt auf die
	// Zuweisung.
	//
	// Umgezogen wird nur, wenn der Sitz wirklich nicht mehr passt. Wer bewusst
	// auf dem zweiten Sitz derselben Engine sitzt, bleibt dort — sonst würde
	// jedes Speichern der Engine eine Wahl zurücknehmen, die jemand getroffen
	// hat. Und weil die Prüfung am Sitz hängt und nicht daran, ob sich der Wert
	// geändert hat, repariert ein erneutes Speichern einen Agenten, der schon
	// im falschen Sitz sitzt.
	if getErr == nil {
		s.reseatOnEngineChange(r.Context(), principalFrom(r).OrgID, a, in.Runtime)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// reseatOnEngineChange moves an agent onto a seat of its engine, if the one it
// has does not belong there. Best effort: an organisation without a seat for
// the new engine keeps an unassigned agent, which the interface already shows
// as missing capacity — better than an assignment that looks right and brokers
// the wrong token.
func (s *Server) reseatOnEngineChange(ctx context.Context, orgID uuid.UUID, a agents.Agent, engine string) {
	if s.Runtimes == nil {
		return
	}
	if a.RuntimeID != nil {
		seat, err := s.Runtimes.Get(ctx, orgID, *a.RuntimeID)
		if err == nil && seat.Engine == engine {
			return // the seat already belongs to this engine — leave the choice alone
		}
	}
	a.Runtime = engine
	s.attachDefaultRuntime(ctx, orgID, a)
}

// handleSetModel pins an agent's LLM (claude-opus-4-8, for example). An empty
// value resets it to the runtime default. Takes effect at the next task
// dispatch; the runtime reports the model actually used back through the cost
// event.
func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Model string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "model missing")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	model := strings.TrimSpace(in.Model)
	if msg := checkModel(a.Runtime, model); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.Registry.SetModel(r.Context(), id, model); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// checkModel validates a model id against the ENGINE the agent runs on, and
// returns the message for a 400 (empty = fine). The same rule as checkEffort,
// for the same reason: the ids belong to the runtime plugin, not to this layer.
//
// An engine that declares no ids accepts anything — in front of a single
// provider the model list is the provider's to publish and ours to pass
// through, and pinning it here would age badly. Empty always passes: it means
// "the engine's default", which for a declaring engine is the first id of its
// list and for the others is the runtime's own (spec/23).
func checkModel(runtime, model string) string {
	if daemon.AcceptsModel(runtime, model) {
		return ""
	}
	models := daemon.Models(runtime)
	if len(models) == 0 {
		// Unknown engine: fail-closed above, and the message says which knob is
		// actually broken rather than blaming the model.
		return "unknown runtime " + runtime + " — assign the agent to a known engine first"
	}
	return "model must be one of " + strings.Join(models, ", ") +
		" for the runtime " + runtime + " (or empty for its default, " + models[0] + ")"
}

// checkEffort validates a reasoning-effort level against the ENGINE the agent
// runs on, and returns the message for a 400 (empty = fine). The levels belong
// to the runtime plugin, not to this layer: `xhigh` is a Claude Code level, and
// storing it on an agent whose engine never reads it would be a setting that is
// configured, visible and without effect. Empty always passes — it means "the
// engine's own default".
func checkEffort(runtime, effort string) string {
	if daemon.AcceptsEffort(runtime, effort) {
		return ""
	}
	levels := daemon.EffortLevels(runtime)
	if len(levels) == 0 {
		return "runtime " + runtime + " has no reasoning-effort setting — leave effort empty"
	}
	return "effort must be one of " + strings.Join(levels, ", ") +
		" (or empty for the " + runtime + " default)"
}

func (s *Server) handleSetEffort(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Effort string `json:"effort"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "effort missing")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	effort := strings.TrimSpace(in.Effort)
	if msg := checkEffort(a.Runtime, effort); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.Registry.SetEffort(r.Context(), id, effort); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRecordingLevel sets the agent override of the recording depth
// (spec/06). Empty = back to inheriting the org floor. Takes effect at the next
// action event; the effective level stays max(org floor, override).
func (s *Server) handleSetRecordingLevel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "level missing")
		return
	}
	in.Level = strings.TrimSpace(in.Level)
	if in.Level != "" && !observability.ValidLevel(in.Level) {
		writeErr(w, http.StatusBadRequest, "level must be minimal|standard|full (or empty)")
		return
	}
	if err := s.Registry.SetRecordingLevel(r.Context(), id, in.Level); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetWarmSandbox toggles an agent's warm sandbox (opt-in). Takes effect
// from the next fall-asleep on — the sandbox then stays live (dev
// servers/caches).
func (s *Server) handleSetWarmSandbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Warm bool `json:"warm"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "warm missing")
		return
	}
	if err := s.Registry.SetWarmSandbox(r.Context(), id, in.Warm); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "warm_sandbox": in.Warm})
}

// handleSetSandboxImage sets an agent's workplace: a profile name (`base`,
// `dev`) or an image reference of its own; empty = the instance default.
// Takes effect at the next cold start.
//
// The value is not validated against a list of profiles. That is deliberate:
// the third row of the profile table is "org-owned: anything" (spec/16), and a
// check here would have to know every image an organisation builds for itself.
// A wrong value fails loudly at the next wake, with the image named.
func (s *Server) handleSetSandboxImage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Image string `json:"sandbox_image"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "sandbox_image missing")
		return
	}
	if err := s.Registry.SetSandboxImage(r.Context(), id, in.Image); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sandbox_image": strings.TrimSpace(in.Image)})
}

// handleSetRunnerTags sets the capabilities an agent needs of its host
// (spec/16). Empty = any runner of the organisation, which is the normal case.
func (s *Server) handleSetRunnerTags(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Tags []string `json:"runner_tags"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "runner_tags missing")
		return
	}
	if err := s.Registry.SetRunnerTags(r.Context(), id, in.Tags); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runner_tags": in.Tags})
}

// handleSetServices sets what runs beside an agent's sandbox (spec/16): a
// database, a queue — the half of a workplace an image cannot carry.
//
// Unlike the workplace image this IS validated against rules, and the
// difference is not an inconsistency: an image reference is a string docker
// either resolves or does not, while a service name becomes a host name on a
// segment and a container name on a shared runner. A wrong image fails loudly
// at the next wake with the image named; a wrong service name would fail as a
// resolver returning something else.
func (s *Server) handleSetServices(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Services []sandbox.Service `json:"services"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "services missing")
		return
	}
	clean, err := sandbox.ValidateServices(in.Services)
	if err != nil {
		// The declaration's own error, not a generic one: it names the service
		// and what is wrong with it, and it is written for whoever typed it.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Registry.SetServices(r.Context(), id, clean); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": clean})
}

// handleOrgRecording reads (GET) and sets (PATCH) the org floor of the
// recording depth — the org-wide minimum depth (security/compliance).
func (s *Server) handleGetOrgRecording(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	level, err := s.Obs.OrgRecordingLevel(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	tage, err := s.Obs.OrgRecordingRetention(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Both recording settings in one answer: the page that shows them asks once.
	writeJSON(w, http.StatusOK, map[string]any{"level": level, "retention_days": tage})
}

// handleSetOrgRecordingRetention sets how long the verbatim run is kept
// (spec/06). Days, 0 = forever. Only the transcript expires — what an action,
// an approval or a credential request recorded stays, which is why this sits
// under the security roles like the depth beside it and not under manage.
func (s *Server) handleSetOrgRecordingRetention(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Days int `json:"retention_days"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "retention_days missing")
		return
	}
	if err := s.Obs.SetOrgRecordingRetention(r.Context(), p.OrgID, in.Days); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRecordingRetention sets an agent's override; null = back to
// inheriting the organisation. The value only ever EXTENDS what the
// organisation keeps — a smaller number is stored and simply has no effect,
// see agents.SetRecordingRetention.
func (s *Server) handleSetRecordingRetention(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Days *int `json:"retention_days"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "retention_days missing")
		return
	}
	if err := s.Registry.SetRecordingRetention(r.Context(), id, in.Days); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetOrgRecording(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &in); err != nil || !observability.ValidLevel(strings.TrimSpace(in.Level)) {
		writeErr(w, http.StatusBadRequest, "level must be minimal|standard|full")
		return
	}
	if err := s.Obs.SetOrgRecordingLevel(r.Context(), p.OrgID, strings.TrimSpace(in.Level)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetMaxTurns sets the turn limit per runtime run (runaway guard).
// 0 resets it to the orchestrator default. Takes effect at the next task
// dispatch.
func (s *Server) handleSetMaxTurns(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		MaxTurns int `json:"max_turns"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "max_turns missing")
		return
	}
	if in.MaxTurns < 0 {
		writeErr(w, http.StatusBadRequest, "max_turns must not be negative")
		return
	}
	if err := s.Registry.SetMaxTurns(r.Context(), id, in.MaxTurns); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Recording, Cost, Memory (M6/M7) ---

func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var taskID *uuid.UUID
	if t := r.URL.Query().Get("task_id"); t != "" {
		if tid, err := uuid.Parse(t); err == nil {
			taskID = &tid
		}
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	events, err := s.Obs.Events(r.Context(), id, taskID, after, 500)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// handleRecordingBlob returns a recording artifact (screenshot), org-scoped.
func (s *Server) handleRecordingBlob(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	mime, data, err := s.Obs.GetBlob(r.Context(), p.OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, err := s.Obs.CostByAgent(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// costWindow reads bucket + days from the query and forms (bucket, since) out
// of them. Defaults: daily buckets over the last 30 days; days is capped at 365.
func costWindow(r *http.Request) (string, time.Time) {
	bucket := r.URL.Query().Get("bucket")
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}
	if days > 365 {
		days = 365
	}
	return bucket, time.Now().AddDate(0, 0, -days)
}

// handleCostSeries returns an agent's cost time series for the chart.
func (s *Server) handleCostSeries(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	bucket, since := costWindow(r)
	series, err := s.Obs.CostSeriesByAgent(r.Context(), id, bucket, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, series)
}

// handleOrgCost returns the org-wide cost report (totals, time series,
// per agent, per model) for the cost/token chart.
func (s *Server) handleOrgCost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	bucket, since := costWindow(r)
	rep, err := s.Obs.OrgCostReport(r.Context(), p.OrgID, bucket, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleOrgRunCosts ranks the organization's runs by cost — the answer to
// "which run burned the money", which the aggregates cannot give. ?limit=
// caps the list, ?days= the window.
func (s *Server) handleOrgRunCosts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	_, since := costWindow(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Obs.RunCosts(r.Context(), p.OrgID, nil, since, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleAgentRunCosts is the same list for a single agent.
func (s *Server) handleAgentRunCosts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	_, since := costWindow(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Obs.RunCosts(r.Context(), p.OrgID, &id, since, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	// With ?q= the endpoint returns the semantic vector search (spec/05,
	// pgvector) instead of the most recent pages — the same view the agent gets
	// in the triage step. Without q: the latest pages for the index.
	var entries []memory.Entry
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		entries, err = s.Memory.Query(r.Context(), id, q, 20)
	} else {
		entries, err = s.Memory.List(r.Context(), id, 50)
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleCreateMemory creates wiki knowledge by hand (onboarding, corrections) —
// the counterpart to the automatic ingest in the done step. With title (and
// optionally slug) a named page comes about; with content alone the insight is
// routed into the fitting page (spec/05).
func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Content string   `json:"content"`
		Title   string   `json:"title"`
		Slug    string   `json:"slug"`
		Type    string   `json:"type"`
		Tags    []string `json:"tags"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if memory.IsNoise(in.Content) {
		writeErr(w, http.StatusBadRequest, "no usable content")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	p := principalFrom(r)
	if strings.TrimSpace(in.Title) != "" || strings.TrimSpace(in.Slug) != "" {
		slug := in.Slug
		if slug == "" {
			slug = in.Title
		}
		if _, err := s.Memory.Write(r.Context(), id, memory.PageInput{
			Slug: slug, Title: in.Title, Body: in.Content,
			Source: "manual", Type: in.Type, Tags: in.Tags,
		}); err != nil {
			if errors.Is(err, memory.ErrNoContent) {
				writeErr(w, http.StatusBadRequest, "no usable content")
				return
			}
			mapErr(w, err)
			return
		}
	} else if err := s.Memory.Ingest(r.Context(), id, in.Content,
		map[string]string{"source": "manual", "by": p.Email}); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := s.Memory.UpdatePage(r.Context(), id, in.Title, in.Content); err != nil {
		if errors.Is(err, memory.ErrNoContent) {
			writeErr(w, http.StatusBadRequest, "no usable content")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Memory.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWikiHealth returns the quality findings of a wiki (spec/05): orphaned
// pages, dead links, missing types, diary titles, suspected duplicates and
// stubs. Purely reading — nothing is changed automatically.
func (s *Server) handleWikiHealth(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	h, err := s.Memory.CheckHealth(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// handleWikiLog returns the chronological wiki log (log.md, spec/05) —
// transparency about ingests, edits, merges and deletions.
func (s *Server) handleWikiLog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.Memory.Log(r.Context(), id, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleWikiConsolidate kicks off the consolidation pass for an agent by hand
// (spec/05) and reports the number of merges.
func (s *Server) handleWikiConsolidate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	merged, err := s.Memory.Consolidate(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"merged": merged})
}

// --- Approvals (M6) ---

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Obs.ListApprovals(r.Context(), p.OrgID, r.URL.Query().Get("status"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Approve *bool `json:"approve"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Required field: a missing approve must not silently pass as a rejection —
	// the decision has to be explicit.
	if in.Approve == nil {
		writeErr(w, http.StatusBadRequest, "field approve (true|false) missing")
		return
	}
	appr, err := s.Obs.DecideApproval(r.Context(), p.OrgID, id, *in.Approve, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// The decision wakes the blocked task (wake-on-correlation).
	s.Orch.OnApprovalDecided(r.Context(), appr)
	writeJSON(w, http.StatusOK, appr)
}

// --- Guard-Rails (M6) ---

func (s *Server) handleListGuardrails(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	rules, err := s.Rails.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleCreateGuardrail(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		ScopeLevel string          `json:"scope_level"`
		AgentID    *uuid.UUID      `json:"agent_id"`
		RuleType   string          `json:"rule_type"`
		Pattern    string          `json:"pattern"`
		Params     json.RawMessage `json:"params"`
	}
	if err := readJSON(r, &in); err != nil || in.RuleType == "" {
		writeErr(w, http.StatusBadRequest, "rule_type is required")
		return
	}
	if in.ScopeLevel == "" {
		in.ScopeLevel = "global"
	}
	// Budget caps apply per scope, not per action — without a pattern, to all.
	in.Pattern = strings.TrimSpace(in.Pattern)
	if in.RuleType == guardrails.RuleBudgetLimit && in.Pattern == "" {
		in.Pattern = "*"
	}
	draft := guardrails.Rule{
		OrgID: p.OrgID, ScopeLevel: in.ScopeLevel, AgentID: in.AgentID,
		RuleType: in.RuleType, Pattern: in.Pattern, Params: in.Params, Enabled: true,
	}
	if len(draft.Params) == 0 {
		draft.Params = json.RawMessage(`{}`)
	}
	if err := guardrails.Validate(draft); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.Rails.Create(r.Context(), draft)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleDeleteGuardrail(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if err := s.Rails.Delete(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleUpdateGuardrail arms a rule or pauses it (enabled).
func (s *Server) handleUpdateGuardrail(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "field enabled (true|false) missing")
		return
	}
	rule, err := s.Rails.SetEnabled(r.Context(), p.OrgID, id, *in.Enabled)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleTestGuardrail is the rule tester: it evaluates a subject (system or
// system:action) against the current rules without running anything — that way
// a policy can be verified before it is armed.
func (s *Server) handleTestGuardrail(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Subject string     `json:"subject"`
		AgentID *uuid.UUID `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Subject) == "" {
		writeErr(w, http.StatusBadRequest, "field subject is required")
		return
	}
	rules, err := s.Rails.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	agentID := uuid.Nil
	if in.AgentID != nil {
		agentID = *in.AgentID
	}
	verdict := guardrails.Evaluate(rules, agentID, strings.TrimSpace(in.Subject))
	out := map[string]any{
		"subject":  strings.TrimSpace(in.Subject),
		"decision": verdict.Decision,
	}
	if verdict.Rule != nil {
		out["rule"] = verdict.Rule
	}
	if limit := guardrails.BudgetLimit(rules, agentID); limit > 0 {
		out["budget_limit_usd"] = limit
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGuardrailEvents returns the most recently triggered guard-rails org-wide.
func (s *Server) handleGuardrailEvents(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.Obs.OrgEventsByKind(r.Context(), p.OrgID, observability.KindGuardrail, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// --- Secrets (write-only API) ---

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	previews, err := s.Secrets.Previews(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if previews == nil {
		previews = []secrets.KeyPreview{}
	}
	writeJSON(w, http.StatusOK, previews)
}

func (s *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	key := r.PathValue("key")
	var in struct {
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value missing")
		return
	}
	if err := s.Secrets.Put(r.Context(), p.OrgID, key, in.Value); err != nil {
		mapErr(w, err)
		return
	}
	if in.Sensitive {
		if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, key); err != nil {
			mapErr(w, err)
			return
		}
	}
	// An LLM credential goes straight onto its engine's workplace — so that the
	// simple setup stays two steps (deposit, create agent) and not four.
	s.syncDefaultRuntime(r.Context(), p.OrgID, key)
	// Check known credentials live right away — a dead token should stand out
	// here, not first at the 401 inside the sandbox.
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": check})
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Secrets.Delete(r.Context(), p.OrgID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePatchSecret marks an org secret as sensitive. Deliberately one-way:
// sensitive=false is refused — lifting the protection would mean disclosing the
// value after all. Back only by deleting and creating it anew.
func (s *Server) handlePatchSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Sensitive *bool `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || in.Sensitive == nil {
		writeErr(w, http.StatusBadRequest, "sensitive missing")
		return
	}
	if !*in.Sensitive {
		writeErr(w, http.StatusConflict, "once marked sensitive a secret stays protected — delete it and create it anew")
		return
	}
	if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePatchAgentSecret — like handlePatchSecret, for agent-owned secrets.
func (s *Server) handlePatchAgentSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Sensitive *bool `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || in.Sensitive == nil {
		writeErr(w, http.StatusBadRequest, "sensitive missing")
		return
	}
	if !*in.Sensitive {
		writeErr(w, http.StatusConflict, "once marked sensitive a secret stays protected — delete it and create it anew")
		return
	}
	if err := s.Secrets.MarkAgentSensitive(r.Context(), p.OrgID, agentID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Secrets with agent scope: explicit assignments + agent-owned secrets ---

func (s *Server) handleAssignSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	if err := s.Secrets.Assign(r.Context(), p.OrgID, r.PathValue("key"), agentID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnassignSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	if err := s.Secrets.Unassign(r.Context(), p.OrgID, r.PathValue("key"), agentID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListAgentSecrets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	previews, err := s.Secrets.AgentPreviews(r.Context(), p.OrgID, agentID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if previews == nil {
		previews = []secrets.KeyPreview{}
	}
	writeJSON(w, http.StatusOK, previews)
}

func (s *Server) handlePutAgentSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	key := r.PathValue("key")
	var in struct {
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value missing")
		return
	}
	if err := s.Secrets.PutAgent(r.Context(), p.OrgID, agentID, key, in.Value); err != nil {
		mapErr(w, err)
		return
	}
	if in.Sensitive {
		if err := s.Secrets.MarkAgentSensitive(r.Context(), p.OrgID, agentID, key); err != nil {
			mapErr(w, err)
			return
		}
	}
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": check})
}

func (s *Server) handleDeleteAgentSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Secrets.DeleteAgent(r.Context(), p.OrgID, agentID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Fleet (fleet-wide kill switch) ---

func (s *Server) handleFleetKill(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Orch.KillFleet(r.Context(), p.OrgID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFleetResume(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.Orch.ResumeFleet(r.Context(), p.OrgID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFleetStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	killed, err := s.Registry.FleetKilled(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"fleet_killed": killed})
}
