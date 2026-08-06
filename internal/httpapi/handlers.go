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
	"covey/internal/claudeapi"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
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
		writeErr(w, http.StatusBadRequest, "slug and display_name are required")
		return
	}
	a, err := s.Registry.Create(r.Context(), p.OrgID, in.Slug, in.DisplayName, in.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Every agent starts out with a default board. Best-effort: the agent is
	// already created, a seeding error must not topple that.
	if err := s.Backlog.SeedDefaultStages(r.Context(), a.ID); err != nil {
		s.Log.Warn("seeding default stages failed", "agent", a.ID, "err", err)
	}
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
	writeJSON(w, http.StatusOK, a)
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
	if access, err := s.renderAccessFile(r.Context(), id); err != nil {
		s.Log.Warn("rendering ACCESS.md", "agent", id, "err", err)
	} else {
		cv.Files["ACCESS.md"] = access
	}
	if eg, err := s.renderEgressFile(r.Context(), p.OrgID, id); err != nil {
		s.Log.Warn("rendering EGRESS.md", "agent", id, "err", err)
	} else {
		cv.Files["EGRESS.md"] = eg
	}
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
	// Write-through into the UI stores (tools, egress) — validate and check
	// RBAC first, so that a faulty file never produces a version.
	canSecurity := p.Role == identity.RolePlatformAdmin || p.Role == identity.RoleSecurity
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
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	if err := s.Registry.SetModel(r.Context(), id, strings.TrimSpace(in.Model)); err != nil {
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

// handleOrgRecording reads (GET) and sets (PATCH) the org floor of the
// recording depth — the org-wide minimum depth (security/compliance).
func (s *Server) handleGetOrgRecording(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	level, err := s.Obs.OrgRecordingLevel(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"level": level})
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

// --- Runtime credential: which Anthropic token an agent burns ---

// handleGetRuntimeCredential reports the pin AND what actually resolves. Two
// separate facts: "pinned" is the intent, "effective_key" what the next wake
// would really use — they only differ when the pin has gone stale, and that is
// exactly the case worth seeing. Readable for managers too, so an agent owner
// can see which token their agent burns without being able to redirect it.
//
// GET /api/v1/agents/{id}/runtime-credential
func (s *Server) handleGetRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := map[string]any{"pinned": agent.RuntimeCredentialKey, "resolvable": false,
		"effective_key": "", "kind": "", "env_var": ""}
	res, err := claudeapi.ResolveAgent(r.Context(), s.Secrets, p.OrgID, id, agent.RuntimeCredentialKey)
	if err == nil {
		out["resolvable"] = true
		out["effective_key"] = res.Key
		out["kind"] = res.Kind.String()
		out["env_var"] = res.Kind.EnvVar()
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetRuntimeCredential pins the secret the agent's runtime authenticates
// with; an empty key returns it to the fallback order.
//
// An org-wide secret is assigned along the way, because a pin only takes effect
// once the secret actually reaches the agent — pin without grant is a trap that
// springs at the next wake and nowhere earlier. That is also why the route is
// gated to the security roles and not to the managers: whoever may pin decides
// which account an agent bills, and could grant the secret directly anyway.
//
// The pin takes that grant with it when it MOVES to another credential: an
// agent that no longer uses a token has no business still reaching it, and
// without this every re-pin would leave one more live grant behind. Unpinning
// is the exception — the fallback order needs something assigned to find, so
// there the grant stays.
//
// PATCH /api/v1/agents/{id}/runtime-credential  {"key": "..."}
func (s *Server) handleSetRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "key missing")
		return
	}
	key := strings.TrimSpace(in.Key)
	// The previous pin decides whether a grant has to be cleaned up afterwards.
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	previous := agent.RuntimeCredentialKey

	if key == "" { // unpin — back to the fallback order, grant stays
		if err := s.Registry.SetRuntimeCredentialKey(r.Context(), id, ""); err != nil {
			mapErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": "", "assigned": false, "unassigned": ""})
		return
	}
	if claudeapi.Classify(key) == claudeapi.KindNone {
		writeErr(w, http.StatusBadRequest, "not a runtime credential: the name must be "+
			claudeapi.KeyAPIKey+" or "+claudeapi.KeyOAuth+", optionally with an _suffix")
		return
	}

	// Owned secret shadows the org one, so there is nothing to grant then.
	owned := false
	if keys, err := s.Secrets.ResolvableKeys(r.Context(), p.OrgID, id); err == nil {
		for _, k := range keys {
			if k.Key == key && k.Owned {
				owned = true
			}
		}
	}
	assigned := false
	if !owned {
		if err := s.Secrets.Assign(r.Context(), p.OrgID, key, id); err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				writeErr(w, http.StatusConflict,
					"no secret named "+key+" — store it first, org-wide or for this agent")
				return
			}
			mapErr(w, err)
			return
		}
		assigned = true
	}
	if err := s.Registry.SetRuntimeCredentialKey(r.Context(), id, key); err != nil {
		mapErr(w, err)
		return
	}
	// The pin has moved: take back the grant the old pin had handed out. Only
	// org-wide grants — an agent's own secret is nobody's to revoke here.
	unassigned := ""
	if previous != "" && previous != key {
		if err := s.Secrets.Unassign(r.Context(), p.OrgID, previous, id); err == nil {
			unassigned = previous
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "key": key, "assigned": assigned, "unassigned": unassigned})
}

// refusePinned refuses to take a secret away from agents that pin it. The pin
// deliberately does not fall back (see orchestrator.pushAnthropicKey) — so
// without this check deleting or unassigning would break the next wake with no
// visible cause. onlyAgent narrows the question to a single agent for the
// agent-scoped routes; uuid.Nil asks about the whole organization.
//
// Returns true when it has already answered with 409.
func (s *Server) refusePinned(w http.ResponseWriter, r *http.Request, orgID, onlyAgent uuid.UUID, key string) bool {
	users, err := s.Registry.AgentsUsingCredential(r.Context(), orgID, key)
	if err != nil {
		return false
	}
	slugs := make([]string, 0, len(users))
	for _, a := range users {
		// Deliberately blunt in the rare case where an agent both owns a secret
		// of this name and has the org one assigned: it would keep resolving,
		// yet refusing costs one extra click while a wrong guess costs a run.
		if onlyAgent != uuid.Nil && a.ID != onlyAgent {
			continue
		}
		slugs = append(slugs, a.Slug)
	}
	if len(slugs) == 0 {
		return false
	}
	// Say what to do, not just what went wrong — and say it about the right
	// agent: on the agent-scoped routes it is the one being edited, so "those
	// agents" would send the reader looking for somebody else.
	if onlyAgent != uuid.Nil {
		writeErr(w, http.StatusConflict, key+" is the runtime credential of "+slugs[0]+
			" — pick a different one (or the default order) under the runtime credential first")
		return true
	}
	writeErr(w, http.StatusConflict, key+" is the runtime credential of "+
		strings.Join(slugs, ", ")+" — pin those agents elsewhere first")
	return true
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
	// Check known credentials live right away — a dead token should stand out
	// here, not first at the 401 inside the sandbox.
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": check})
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if s.refusePinned(w, r, p.OrgID, uuid.Nil, r.PathValue("key")) {
		return
	}
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
	if s.refusePinned(w, r, p.OrgID, agentID, r.PathValue("key")) {
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
	if s.refusePinned(w, r, p.OrgID, agentID, r.PathValue("key")) {
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
