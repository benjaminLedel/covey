package httpapi

import (
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

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
	var in struct {
		Files map[string]string `json:"files"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "files fehlt")
		return
	}
	s.saveAndApplyConfig(w, r, id, in.Files)
}

// saveAndApplyConfig speichert files als neue Config-Version und übernimmt
// ACCESS.md/EGRESS.md in die UI-Stores (Write-Through) — der gemeinsame Kern
// von PUT /config und dem Bundle-Config-Import. Erst validieren und RBAC
// prüfen, damit eine fehlerhafte Datei keine Version erzeugt; bei Erfolg wird
// die neue ConfigVersion (200) geschrieben.
func (s *Server) saveAndApplyConfig(w http.ResponseWriter, r *http.Request, id uuid.UUID, files map[string]string) {
	p := principalFrom(r)
	if _, ok := files["TOOLS.md"]; ok {
		writeErr(w, http.StatusBadRequest, "TOOLS.md ist in ACCESS.md aufgegangen (Attribut tools: je System)")
		return
	}
	// HEARTBEAT.md vorab validieren: ein Parse-Fehler soll als 400 mit
	// verständlicher Meldung zurückkommen, nicht erst in SaveConfig scheitern.
	if _, err := agents.ParseHeartbeat(files["HEARTBEAT.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "HEARTBEAT.md: "+err.Error())
		return
	}
	// Write-Through in die UI-Stores (Tools, Egress) — erst validieren und
	// RBAC prüfen, damit eine fehlerhafte Datei keine Version erzeugt.
	canSecurity := p.Role == identity.RolePlatformAdmin || p.Role == identity.RoleSecurity
	apply, err := s.prepareConfigApply(r.Context(), p.OrgID, id, files, canSecurity)
	if err != nil {
		if errors.Is(err, errNeedsSecurityRole) {
			writeErr(w, http.StatusForbidden, err.Error())
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	cv, err := s.Registry.SaveConfig(r.Context(), id, files, &p.ID)
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

// handleFireHeartbeat feuert einen Heartbeat sofort, unabhängig vom Zeitplan
// (Button in der UI). Legt die Backlog-Aufgabe an und weckt den Agenten;
// last_fired_at wird fortgeschrieben, der reguläre Zeitplan rechnet ab jetzt.
func (s *Server) handleFireHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	name := r.PathValue("name")
	orgID, body, err := s.Registry.FireHeartbeat(r.Context(), id, name)
	switch {
	case errors.Is(err, agents.ErrHeartbeatPending):
		writeErr(w, http.StatusConflict, "Die Aufgabe des letzten Laufs ist noch offen — erst abschließen oder abbrechen.")
		return
	case errors.Is(err, agents.ErrAgentKilled):
		writeErr(w, http.StatusConflict, "Agent oder Flotte ist gestoppt — erst fortsetzen.")
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

// handleTaskNotes liefert die proaktiven Notizen des Agenten an einer Aufgabe.
func (s *Server) handleTaskNotes(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

// handleUpdateAgentProfile schreibt die Mitarbeiter-Stammdaten eines Agenten —
// dieselben Profilfelder wie bei den Menschen (profilePatch), Werte erscheinen
// im Org-Chart und in der covey/org_chart-Abfrage der Agenten.
func (s *Server) handleUpdateAgentProfile(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in profilePatch
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
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

// handleSetSlug ändert den Slug eines Agenten. Muss innerhalb der Org eindeutig sein.
func (s *Server) handleSetSlug(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Slug string `json:"slug"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Slug) == "" {
		writeErr(w, http.StatusBadRequest, "slug fehlt")
		return
	}
	if err := s.Registry.SetSlug(r.Context(), id, strings.TrimSpace(in.Slug)); err != nil {
		if strings.Contains(err.Error(), "bereits vergeben") || strings.Contains(err.Error(), "Format") {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
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

// handleSetModel legt das LLM eines Agenten fest (z. B. claude-opus-4-8).
// Leerer Wert setzt zurück auf den Runtime-Default. Wirkt beim nächsten
// Task-Dispatch; die Runtime meldet das tatsächlich genutzte Modell über
// das Cost-Event zurück.
func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Model string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "model fehlt")
		return
	}
	if err := s.Registry.SetModel(r.Context(), id, strings.TrimSpace(in.Model)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRecordingLevel setzt den Agent-Override der Aufzeichnungstiefe
// (spec/06). Leer = zurück auf Erben des Org-Bodens. Wirkt beim nächsten
// action-Event; der effektive Level bleibt max(Org-Boden, Override).
func (s *Server) handleSetRecordingLevel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Level string `json:"level"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "level fehlt")
		return
	}
	in.Level = strings.TrimSpace(in.Level)
	if in.Level != "" && !observability.ValidLevel(in.Level) {
		writeErr(w, http.StatusBadRequest, "level muss minimal|standard|full (oder leer) sein")
		return
	}
	if err := s.Registry.SetRecordingLevel(r.Context(), id, in.Level); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetWarmSandbox schaltet die warme Sandbox eines Agenten (opt-in). Wirkt
// ab dem nächsten Einschlafen — die Sandbox bleibt dann live (Dev-Server/Caches).
func (s *Server) handleSetWarmSandbox(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Warm bool `json:"warm"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "warm fehlt")
		return
	}
	if err := s.Registry.SetWarmSandbox(r.Context(), id, in.Warm); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "warm_sandbox": in.Warm})
}

// handleOrgRecording liest (GET) bzw. setzt (PATCH) den Org-Boden der
// Aufzeichnungstiefe — die org-weite Mindesttiefe (Security/Compliance).
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
		writeErr(w, http.StatusBadRequest, "level muss minimal|standard|full sein")
		return
	}
	if err := s.Obs.SetOrgRecordingLevel(r.Context(), p.OrgID, strings.TrimSpace(in.Level)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetMaxTurns legt das Turn-Limit je Runtime-Lauf fest (Runaway-Guard).
// 0 setzt zurück auf den Orchestrator-Default. Wirkt beim nächsten Task-Dispatch.
func (s *Server) handleSetMaxTurns(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		MaxTurns int `json:"max_turns"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "max_turns fehlt")
		return
	}
	if in.MaxTurns < 0 {
		writeErr(w, http.StatusBadRequest, "max_turns darf nicht negativ sein")
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

// handleRecordingBlob liefert ein Recording-Artefakt (Screenshot) org-gescopt.
func (s *Server) handleRecordingBlob(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

// costWindow liest bucket + days aus der Query und bildet daraus (bucket, since).
// Defaults: Tages-Buckets über die letzten 30 Tage; days wird auf 1..365 begrenzt.
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

// handleCostSeries liefert die Kostenzeitreihe eines Agenten fürs Diagramm.
func (s *Server) handleCostSeries(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

// handleOrgCost liefert den org-weiten Kostenbericht (Summen, Zeitreihe,
// pro Agent, pro Modell) fürs Kosten-/Token-Diagramm.
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	// Mit ?q= liefert der Endpunkt die semantische Vektorsuche (spec/05,
	// pgvector) statt der jüngsten Seiten — dieselbe Sicht, die der Agent im
	// triage-Schritt bekommt. Ohne q: die letzten Seiten für den Index.
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

// handleCreateMemory legt manuell Wiki-Wissen an (Onboarding, Korrekturen) —
// Gegenstück zum automatischen Ingest im done-Schritt. Mit title (und optional
// slug) entsteht eine benannte Seite; nur mit content wird die Erkenntnis in
// die passende Seite geroutet (spec/05).
func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
				writeErr(w, http.StatusBadRequest, "kein verwertbarer inhalt")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if err := s.Memory.UpdatePage(r.Context(), id, in.Title, in.Content); err != nil {
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

// handleWikiHealth liefert die Qualitätsbefunde eines Wikis (spec/05):
// verwaiste Seiten, tote Verweise, fehlende Typen, Tagebuch-Titel, Dubletten-
// Verdacht und Stummel. Rein lesend — es wird nichts automatisch geändert.
func (s *Server) handleWikiHealth(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

// handleWikiLog liefert das chronologische Wiki-Protokoll (log.md, spec/05) —
// Transparenz über Ingests, Edits, Verschmelzungen und Löschungen.
func (s *Server) handleWikiLog(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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

// handleWikiConsolidate stößt den Konsolidierungs-Pass für einen Agenten manuell
// an (spec/05) und meldet die Zahl der Verschmelzungen.
func (s *Server) handleWikiConsolidate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value fehlt")
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

// handlePatchSecret markiert ein Org-Secret als sensibel. Bewusst einweg:
// sensitive=false wird abgelehnt — den Schutz aufheben hieße, den Wert doch
// offenzulegen. Zurück nur durch Löschen und Neuanlegen.
func (s *Server) handlePatchSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Sensitive *bool `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || in.Sensitive == nil {
		writeErr(w, http.StatusBadRequest, "sensitive fehlt")
		return
	}
	if !*in.Sensitive {
		writeErr(w, http.StatusConflict, "einmal als sensibel markiert bleibt ein Secret geschützt — löschen und neu anlegen")
		return
	}
	if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePatchAgentSecret — wie handlePatchSecret, für agent-eigene Secrets.
func (s *Server) handlePatchAgentSecret(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Sensitive *bool `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || in.Sensitive == nil {
		writeErr(w, http.StatusBadRequest, "sensitive fehlt")
		return
	}
	if !*in.Sensitive {
		writeErr(w, http.StatusConflict, "einmal als sensibel markiert bleibt ein Secret geschützt — löschen und neu anlegen")
		return
	}
	if err := s.Secrets.MarkAgentSensitive(r.Context(), p.OrgID, agentID, r.PathValue("key")); err != nil {
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
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value fehlt")
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
