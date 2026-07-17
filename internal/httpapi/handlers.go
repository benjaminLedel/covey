package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/secrets"
)

// --- Agenten ---

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
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Runtime     string `json:"runtime"`
	}
	if err := readJSON(r, &in); err != nil || in.Slug == "" || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "slug und display_name sind Pflicht")
		return
	}
	a, err := s.Registry.Create(r.Context(), p.OrgID, in.Slug, in.DisplayName, in.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Jeder Agent startet mit einem Default-Board. Best-effort: der Agent ist
	// bereits angelegt, ein Seed-Fehler darf die Erstellung nicht kippen.
	if err := s.Backlog.SeedDefaultStages(r.Context(), a.ID); err != nil {
		s.Log.Warn("default-stages seeden fehlgeschlagen", "agent", a.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// --- Config-as-Code (M2) ---

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	cv, err := s.Registry.CurrentConfig(r.Context(), id)
	if errors.Is(err, agents.ErrNotFound) {
		// Noch keine Version: leere Dateien, damit ACCESS.md/EGRESS.md
		// (live gerendert) trotzdem sichtbar sind.
		cv = agents.ConfigVersion{AgentID: id, Files: map[string]string{"SOUL.md": "", "HEARTBEAT.md": ""}}
	} else if err != nil {
		mapErr(w, err)
		return
	}
	// ACCESS.md und EGRESS.md sind die Text-Sicht auf die UI-Stores und werden
	// live gerendert — nie aus dem Version-Snapshot serviert (configsync.go).
	if access, err := s.renderAccessFile(r.Context(), id); err != nil {
		s.Log.Warn("ACCESS.md rendern", "agent", id, "err", err)
	} else {
		cv.Files["ACCESS.md"] = access
	}
	if eg, err := s.renderEgressFile(r.Context(), p.OrgID, id); err != nil {
		s.Log.Warn("EGRESS.md rendern", "agent", id, "err", err)
	} else {
		cv.Files["EGRESS.md"] = eg
	}
	delete(cv.Files, "TOOLS.md") // Legacy: in ACCESS.md aufgegangen
	writeJSON(w, http.StatusOK, cv)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Files map[string]string `json:"files"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "files fehlt")
		return
	}
	if _, ok := in.Files["TOOLS.md"]; ok {
		writeErr(w, http.StatusBadRequest, "TOOLS.md ist in ACCESS.md aufgegangen (Attribut tools: je System)")
		return
	}
	// HEARTBEAT.md vorab validieren: ein Parse-Fehler soll als 400 mit
	// verständlicher Meldung zurückkommen, nicht erst in SaveConfig scheitern.
	if _, err := agents.ParseHeartbeat(in.Files["HEARTBEAT.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "HEARTBEAT.md: "+err.Error())
		return
	}
	// Write-Through in die UI-Stores (Tools, Egress) — erst validieren und
	// RBAC prüfen, damit eine fehlerhafte Datei keine Version erzeugt.
	canSecurity := p.Role == identity.RolePlatformAdmin || p.Role == identity.RoleSecurity
	apply, err := s.prepareConfigApply(r.Context(), p.OrgID, id, in.Files, canSecurity)
	if err != nil {
		if errors.Is(err, errNeedsSecurityRole) {
			writeErr(w, http.StatusForbidden, err.Error())
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	cv, err := s.Registry.SaveConfig(r.Context(), id, in.Files, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := apply(r.Context()); err != nil {
		s.Log.Error("config write-through", "agent", id, "err", err)
		writeErr(w, http.StatusInternalServerError, "Version gespeichert, aber Übernahme in Tools/Egress fehlgeschlagen: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cv)
}

// handleHeartbeats liefert die materialisierten Heartbeats des Agenten
// inklusive berechnetem nächstem Lauf — die Monitoring-Sicht auf HEARTBEAT.md.
func (s *Server) handleHeartbeats(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	hbs, err := s.Registry.Heartbeats(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hbs)
}

// --- Backlog (M3) ---

func (s *Server) handleBacklog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	tasks, err := s.Backlog.ListByAgent(r.Context(), id, r.URL.Query().Get("archived") == "1")
	if err != nil {
		mapErr(w, err)
		return
	}
	if tasks == nil {
		tasks = []backlog.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Priority int    `json:"priority"`
	}
	if err := readJSON(r, &in); err != nil || in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title ist Pflicht")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	t, err := s.Backlog.Cancel(r.Context(), id, "verworfen von "+p.Email)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleRetryTask plant eine gescheiterte/verworfene Aufgabe erneut ein.
func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	t, err := s.Backlog.Retry(r.Context(), id, "erneut eingeplant von "+p.Email)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleArchiveTask blendet eine terminale Aufgabe aus dem aktiven Backlog aus.
func (s *Server) handleArchiveTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	t, err := s.Backlog.Archive(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleCleanupBacklog archiviert alle terminalen Aufgaben eines Agenten.
func (s *Server) handleCleanupBacklog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	n, err := s.Backlog.ArchiveTerminal(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"archived": n})
}

// --- Stages (Kanban-Overlay) ---

func (s *Server) handleListStages(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name ist Pflicht")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		Position int    `json:"position"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name ist Pflicht")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Order []uuid.UUID `json:"order"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "order ist Pflicht")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		StageID *uuid.UUID `json:"stage_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "stage_id fehlt")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	trs, err := s.Backlog.Transitions(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trs)
}

// --- Lifecycle-Steuerung ---

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	s.Orch.EnsureRunning(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := readJSON(r, &in); err != nil || in.BudgetUSD < 0 {
		writeErr(w, http.StatusBadRequest, "budget_usd fehlt")
		return
	}
	if err := s.Registry.SetBudget(r.Context(), id, in.BudgetUSD); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleListRuntimes liefert die im Daemon registrierten Runtime-Plugins
// (spec/12) — dieselbe Registry, die der Daemon zur Ausführung nutzt.
func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, daemon.Runtimes())
}

// handleRename ändert den Anzeigenamen eines Agenten. Der Slug bleibt stabil.
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		DisplayName string `json:"display_name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.DisplayName) == "" {
		writeErr(w, http.StatusBadRequest, "display_name fehlt")
		return
	}
	if err := s.Registry.Rename(r.Context(), id, strings.TrimSpace(in.DisplayName)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRuntime schaltet die Runtime eines Agenten um (Umschalten z. B. auf
// 'mock' für kostenlose Demos). Wirkt beim nächsten Task-Dispatch.
func (s *Server) handleSetRuntime(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Runtime string `json:"runtime"`
	}
	if err := readJSON(r, &in); err != nil || in.Runtime == "" {
		writeErr(w, http.StatusBadRequest, "runtime fehlt")
		return
	}
	if !daemon.IsRuntime(in.Runtime) {
		writeErr(w, http.StatusBadRequest, "unbekannte runtime")
		return
	}
	if err := s.Registry.SetRuntime(r.Context(), id, in.Runtime); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Recording, Cost, Memory (M6/M7) ---

func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	c, err := s.Obs.CostByAgent(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	entries, err := s.Memory.List(r.Context(), id, 50)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleCreateMemory speist eine Episode manuell ein (Onboarding-Wissen,
// Korrekturen) — Gegenstück zum automatischen Ingest im done-Schritt.
func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if memory.IsNoise(in.Content) {
		writeErr(w, http.StatusBadRequest, "kein verwertbarer inhalt")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	p := principalFrom(r)
	if err := s.Memory.Ingest(r.Context(), id, in.Content,
		map[string]string{"source": "manual", "by": p.Email}); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if err := s.Memory.Update(r.Context(), id, in.Content); err != nil {
		if errors.Is(err, memory.ErrNoContent) {
			writeErr(w, http.StatusBadRequest, "kein verwertbarer inhalt")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.Memory.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Approve *bool `json:"approve"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	// Pflichtfeld: ein fehlendes approve darf nicht still als Ablehnung
	// durchgehen — die Entscheidung muss explizit sein.
	if in.Approve == nil {
		writeErr(w, http.StatusBadRequest, "feld approve (true|false) fehlt")
		return
	}
	appr, err := s.Obs.DecideApproval(r.Context(), p.OrgID, id, *in.Approve, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Entscheidung weckt die geblockte Aufgabe (Wake-on-correlation).
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
		writeErr(w, http.StatusBadRequest, "rule_type ist Pflicht")
		return
	}
	if in.ScopeLevel == "" {
		in.ScopeLevel = "global"
	}
	// Budget-Deckel gelten pro Scope, nicht pro Aktion — ohne Muster auf alles.
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	if err := s.Rails.Delete(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleUpdateGuardrail schaltet eine Regel scharf/pausiert sie (enabled).
func (s *Server) handleUpdateGuardrail(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "feld enabled (true|false) fehlt")
		return
	}
	rule, err := s.Rails.SetEnabled(r.Context(), p.OrgID, id, *in.Enabled)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleTestGuardrail ist der Regel-Tester: wertet ein Subjekt (System oder
// system:aktion) gegen die aktuellen Regeln aus, ohne etwas auszuführen —
// so lässt sich eine Policy vor dem Scharfschalten verifizieren.
func (s *Server) handleTestGuardrail(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Subject string     `json:"subject"`
		AgentID *uuid.UUID `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Subject) == "" {
		writeErr(w, http.StatusBadRequest, "feld subject ist Pflicht")
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

// handleGuardrailEvents liefert die jüngsten ausgelösten Guard-Rails org-weit.
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
		Value string `json:"value"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value fehlt")
		return
	}
	if err := s.Secrets.Put(r.Context(), p.OrgID, key, in.Value); err != nil {
		mapErr(w, err)
		return
	}
	// Bekannte Credentials sofort live prüfen — ein totes Token soll hier
	// auffallen, nicht erst beim 401 in der Sandbox.
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

// --- Secrets mit Agent-Scope: explizite Zuweisungen + agent-eigene Secrets ---

func (s *Server) handleAssignSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige agent-id")
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
		writeErr(w, http.StatusBadRequest, "ungültige agent-id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	key := r.PathValue("key")
	var in struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value fehlt")
		return
	}
	if err := s.Secrets.PutAgent(r.Context(), p.OrgID, agentID, key, in.Value); err != nil {
		mapErr(w, err)
		return
	}
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": check})
}

func (s *Server) handleDeleteAgentSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.Secrets.DeleteAgent(r.Context(), p.OrgID, agentID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Fleet (Kill-Switch flottenweit) ---

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
	if err := s.Registry.SetFleetKilled(r.Context(), p.OrgID, false); err != nil {
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
