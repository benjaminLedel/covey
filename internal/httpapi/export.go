package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/identity"
)

// Export/Import der kompletten Bot-Konfiguration als JSON-Bundle.
//
// Das Bundle ist die portable Sicht auf alles, was einen Agenten konfiguriert:
// Stammdaten, Config-Dateien (SOUL.md, HEARTBEAT.md sowie die live gerenderten
// ACCESS.md/EGRESS.md), Board-Spalten, agent-scoped Guard-Rails, die dem
// Agenten zugewiesenen Egress-Templates (samt Hosts, damit sie beim Import
// fehlen dürfen) und die Namen der zugewiesenen Secrets. Secret-WERTE verlassen
// die Plattform nie (Leitplanke: write-only) — der Import meldet sie als
// nachzupflegende Warnungen.

const (
	bundleKind    = "covey.agent-config"
	bundleVersion = 1
)

type bundleAgent struct {
	Slug            string  `json:"slug"`
	DisplayName     string  `json:"display_name"`
	Runtime         string  `json:"runtime"`
	Model           string  `json:"model,omitempty"`
	MaxTurns        int     `json:"max_turns,omitempty"`
	BudgetUSD       float64 `json:"budget_usd,omitempty"`
	SupervisorEmail string  `json:"supervisor_email,omitempty"`
	// WebhookEnabled: beim Import wird ein FRISCHES Token erzeugt —
	// das Token selbst ist ein Secret und steckt nie im Bundle.
	WebhookEnabled bool `json:"webhook_enabled,omitempty"`
}

type bundleStage struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type bundleGuardrail struct {
	RuleType string          `json:"rule_type"`
	Pattern  string          `json:"pattern"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  bool            `json:"enabled"`
}

type bundleHost struct {
	Pattern string `json:"pattern"`
	Note    string `json:"note,omitempty"`
}

type bundleEgressTemplate struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Hosts       []bundleHost `json:"hosts"`
}

// bundleSecrets führt nur die NAMEN zugewiesener Secrets, nie Werte.
type bundleSecrets struct {
	OrgKeys   []string `json:"org_keys"`
	AgentKeys []string `json:"agent_keys"`
}

type agentBundle struct {
	Kind            string                 `json:"kind"`
	Version         int                    `json:"version"`
	ExportedAt      *time.Time             `json:"exported_at,omitempty"`
	Agent           bundleAgent            `json:"agent"`
	Files           map[string]string      `json:"files"`
	Stages          []bundleStage          `json:"stages,omitempty"`
	Guardrails      []bundleGuardrail      `json:"guardrails,omitempty"`
	EgressTemplates []bundleEgressTemplate `json:"egress_templates,omitempty"`
	Secrets         *bundleSecrets         `json:"secrets,omitempty"`
}

// canReadSecretKeys: dieselbe Grenze wie die Secrets-Endpunkte (securityRoles).
func canReadSecretKeys(role string) bool {
	return role == identity.RolePlatformAdmin || role == identity.RoleSecurity
}

// handleExportAgent — GET /api/v1/agents/{id}/export: die komplette
// Konfiguration eines Agenten als herunterladbares JSON-Bundle.
func (s *Server) handleExportAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	b := agentBundle{
		Kind: bundleKind, Version: bundleVersion, ExportedAt: &now,
		Agent: bundleAgent{
			Slug: a.Slug, DisplayName: a.DisplayName, Runtime: a.Runtime,
			Model: a.Model, MaxTurns: a.MaxTurns, BudgetUSD: a.BudgetUSD,
			WebhookEnabled: a.WebhookToken != nil,
		},
	}
	if a.SupervisorID != nil {
		if h, err := s.Org.GetHuman(r.Context(), p.OrgID, *a.SupervisorID); err == nil {
			b.Agent.SupervisorEmail = h.Email
		}
	}

	// Config-Dateien: Version-Snapshot plus die live gerenderten Text-Sichten
	// ACCESS.md/EGRESS.md — exakt das, was auch der Config-Editor zeigt.
	cv, err := s.Registry.CurrentConfig(r.Context(), id)
	if errors.Is(err, agents.ErrNotFound) {
		cv = agents.ConfigVersion{Files: map[string]string{"SOUL.md": "", "HEARTBEAT.md": ""}}
	} else if err != nil {
		mapErr(w, err)
		return
	}
	b.Files = cv.Files
	if s.Targets != nil {
		if access, err := s.renderAccessFile(r.Context(), id); err == nil {
			b.Files["ACCESS.md"] = access
		}
	}
	if s.EgressStore != nil {
		if eg, err := s.renderEgressFile(r.Context(), p.OrgID, id); err == nil {
			b.Files["EGRESS.md"] = eg
		}
	}
	delete(b.Files, "TOOLS.md") // Legacy: in ACCESS.md aufgegangen

	stages, err := s.Backlog.ListStages(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	for _, st := range stages {
		b.Stages = append(b.Stages, bundleStage{Name: st.Name, Color: st.Color})
	}

	rules, err := s.Rails.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for _, rule := range rules {
		if rule.ScopeLevel != "agent" || rule.AgentID == nil || *rule.AgentID != id {
			continue
		}
		b.Guardrails = append(b.Guardrails, bundleGuardrail{
			RuleType: rule.RuleType, Pattern: rule.Pattern,
			Params: rule.Params, Enabled: rule.Enabled,
		})
	}

	// Zugewiesene Egress-Templates samt Definition mitgeben, damit der Import
	// sie auf einer anderen Instanz anlegen kann, falls sie dort fehlen.
	if s.EgressStore != nil {
		cfg, err := s.EgressStore.AgentConfig(r.Context(), id)
		if err != nil {
			mapErr(w, err)
			return
		}
		templates, err := s.EgressStore.ListTemplates(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		assigned := map[uuid.UUID]bool{}
		for _, tid := range cfg.TemplateIDs {
			assigned[tid] = true
		}
		for _, t := range templates {
			if !assigned[t.ID] {
				continue
			}
			bt := bundleEgressTemplate{Name: t.Name, Description: t.Description, Hosts: []bundleHost{}}
			for _, h := range t.Hosts {
				bt.Hosts = append(bt.Hosts, bundleHost{Pattern: h.Pattern, Note: h.Note})
			}
			b.EgressTemplates = append(b.EgressTemplates, bt)
		}
	}

	// Secret-Namen nur für Rollen, die sie auch über die Secrets-Endpunkte
	// sehen dürften — Werte nie.
	if canReadSecretKeys(p.Role) {
		sec := &bundleSecrets{OrgKeys: []string{}, AgentKeys: []string{}}
		previews, err := s.Secrets.Previews(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		for _, kp := range previews {
			for _, aid := range kp.AgentIDs {
				if aid == id.String() {
					sec.OrgKeys = append(sec.OrgKeys, kp.Key)
				}
			}
		}
		agentPrev, err := s.Secrets.AgentPreviews(r.Context(), p.OrgID, id)
		if err != nil {
			mapErr(w, err)
			return
		}
		for _, kp := range agentPrev {
			sec.AgentKeys = append(sec.AgentKeys, kp.Key)
		}
		sort.Strings(sec.OrgKeys)
		sort.Strings(sec.AgentKeys)
		b.Secrets = sec
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Slug+"-config.json"))
	writeJSON(w, http.StatusOK, b)
}

// handleImportAgent — POST /api/v1/agents/import: legt aus einem Bundle einen
// NEUEN Agenten an. Query-Param slug überschreibt den Slug aus dem Bundle
// (für Kopien bzw. Kollisionen). Alles Sicherheitsrelevante bleibt bei den
// Grenzen der Einzel-Endpunkte: Tools/Egress/Guard-Rails importiert nur, wer
// sie auch dort ändern dürfte (fail-closed, 403 statt stillem Weglassen).
// Secret-Werte stecken nie im Bundle — vorhandene Org-Secrets werden per Name
// wieder zugewiesen, alles andere landet als Warnung in der Antwort.
func (s *Server) handleImportAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var b agentBundle
	if err := readJSON(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bundle nicht lesbar: "+err.Error())
		return
	}
	if b.Kind != bundleKind {
		writeErr(w, http.StatusBadRequest, "kind muss "+bundleKind+" sein")
		return
	}
	if b.Version != bundleVersion {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("bundle-version %d wird nicht unterstützt (erwartet %d)", b.Version, bundleVersion))
		return
	}
	if slug := r.URL.Query().Get("slug"); slug != "" {
		b.Agent.Slug = slug
	}
	if b.Agent.Slug == "" || b.Agent.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "agent.slug und agent.display_name sind Pflicht")
		return
	}
	ctx := r.Context()

	// --- Vorab-Validierung: erst prüfen, dann anlegen (kein halber Import). ---
	if _, err := s.Registry.GetBySlug(ctx, p.OrgID, b.Agent.Slug); err == nil {
		writeErr(w, http.StatusConflict, "slug "+b.Agent.Slug+" existiert bereits — ?slug= überschreibt")
		return
	} else if !errors.Is(err, agents.ErrNotFound) {
		mapErr(w, err)
		return
	}
	if _, ok := b.Files["TOOLS.md"]; ok {
		writeErr(w, http.StatusBadRequest, "TOOLS.md ist in ACCESS.md aufgegangen (Attribut tools: je System)")
		return
	}
	if _, err := agents.ParseHeartbeat(b.Files["HEARTBEAT.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "HEARTBEAT.md: "+err.Error())
		return
	}
	// Platzhalter-ID nur für die Validierung — der Agent existiert noch nicht.
	probeID := uuid.New()
	for _, g := range b.Guardrails {
		probe := guardrails.Rule{OrgID: p.OrgID, ScopeLevel: "agent", AgentID: &probeID,
			RuleType: g.RuleType, Pattern: g.Pattern, Params: g.Params}
		if len(probe.Params) == 0 {
			probe.Params = json.RawMessage(`{}`)
		}
		if err := guardrails.Validate(probe); err != nil {
			writeErr(w, http.StatusBadRequest, "guardrail "+g.RuleType+" "+g.Pattern+": "+err.Error())
			return
		}
	}

	// RBAC wie an den Einzel-Endpunkten: Guard-Rails, Egress und Tool-
	// Allowlists ändern nur Security-Rollen — im Bundle enthalten heißt
	// für andere Rollen 403, nicht stilles Überspringen.
	canSecurity := p.Role == identity.RolePlatformAdmin || p.Role == identity.RoleSecurity
	if !canSecurity {
		var restricted []string
		if len(b.Guardrails) > 0 {
			restricted = append(restricted, "guardrails")
		}
		if len(b.EgressTemplates) > 0 || len(parseEgressFile(b.Files["EGRESS.md"]).Templates) > 0 ||
			len(parseEgressFile(b.Files["EGRESS.md"]).Hosts) > 0 {
			restricted = append(restricted, "egress")
		}
		for _, acc := range agents.ParseAccess(b.Files["ACCESS.md"]) {
			if len(acc.Tools) > 0 {
				restricted = append(restricted, "tool-allowlists")
				break
			}
		}
		if len(restricted) > 0 {
			writeErr(w, http.StatusForbidden, "bundle enthält "+strings.Join(restricted, ", ")+" — import nur mit platform_admin oder security")
			return
		}
	}

	var warnings []string

	// Vorab: Supervisor per E-Mail auflösen (best-effort).
	var supervisorID *uuid.UUID
	if b.Agent.SupervisorEmail != "" {
		humans, err := s.Org.ListHumans(ctx, p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		for _, h := range humans {
			if strings.EqualFold(h.Email, b.Agent.SupervisorEmail) {
				id := h.ID
				supervisorID = &id
				break
			}
		}
		if supervisorID == nil {
			warnings = append(warnings, "supervisor "+b.Agent.SupervisorEmail+" nicht gefunden — Zuordnung übersprungen")
		}
	}

	// Fehlende Egress-Templates aus dem Bundle anlegen, damit die
	// templates:-Zeile in EGRESS.md auflösbar ist.
	if s.EgressStore != nil {
		existing, err := s.EgressStore.ListTemplates(ctx, p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		byName := map[string]bool{}
		for _, t := range existing {
			byName[t.Name] = true
		}
		for _, bt := range b.EgressTemplates {
			if byName[bt.Name] {
				warnings = append(warnings, "egress-template "+bt.Name+" existiert bereits — vorhandene Definition wird verwendet")
				continue
			}
			t, err := s.EgressStore.CreateTemplate(ctx, p.OrgID, bt.Name, bt.Description)
			if err != nil {
				if errors.Is(err, egress.ErrTemplateExists) {
					continue
				}
				mapErr(w, err)
				return
			}
			for _, h := range bt.Hosts {
				if _, err := s.EgressStore.AddTemplateHost(ctx, p.OrgID, t.ID, h.Pattern, h.Note); err != nil {
					writeErr(w, http.StatusBadRequest, "egress-template "+bt.Name+", host "+h.Pattern+": "+err.Error())
					return
				}
			}
		}
	} else if len(b.EgressTemplates) > 0 {
		warnings = append(warnings, "egress ist auf dieser Instanz nicht verfügbar — Templates übersprungen")
	}

	// --- Anlegen. Ab hier existiert der Agent; Folgefehler wären ein halber
	// Import, deshalb ist oben alles Parse-/RBAC-Kritische bereits geprüft. ---
	a, err := s.Registry.Create(ctx, p.OrgID, b.Agent.Slug, b.Agent.DisplayName, b.Agent.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if b.Agent.Model != "" {
		if err := s.Registry.SetModel(ctx, a.ID, b.Agent.Model); err != nil {
			mapErr(w, err)
			return
		}
	}
	if b.Agent.MaxTurns > 0 {
		if err := s.Registry.SetMaxTurns(ctx, a.ID, b.Agent.MaxTurns); err != nil {
			mapErr(w, err)
			return
		}
	}
	if b.Agent.BudgetUSD > 0 {
		if err := s.Registry.SetBudget(ctx, a.ID, b.Agent.BudgetUSD); err != nil {
			mapErr(w, err)
			return
		}
	}
	if supervisorID != nil {
		if err := s.Registry.SetSupervisor(ctx, a.ID, supervisorID); err != nil {
			mapErr(w, err)
			return
		}
	}
	if b.Agent.WebhookEnabled {
		buf := make([]byte, 32)
		rand.Read(buf)
		token := hex.EncodeToString(buf)
		if err := s.Registry.SetWebhookToken(ctx, a.ID, &token); err != nil {
			mapErr(w, err)
			return
		}
	}

	// Board: Spalten aus dem Bundle in Reihenfolge, sonst die Defaults.
	if len(b.Stages) > 0 {
		for _, st := range b.Stages {
			if _, err := s.Backlog.CreateStage(ctx, a.ID, st.Name, st.Color); err != nil {
				mapErr(w, err)
				return
			}
		}
	} else if err := s.Backlog.SeedDefaultStages(ctx, a.ID); err != nil {
		s.Log.Warn("default-stages seeden fehlgeschlagen", "agent", a.ID, "err", err)
	}

	// Config-Version speichern und ACCESS.md/EGRESS.md in die UI-Stores
	// übernehmen — derselbe Pfad wie PUT /config.
	if len(b.Files) > 0 {
		apply, err := s.prepareConfigApply(ctx, p.OrgID, a.ID, b.Files, canSecurity)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := s.Registry.SaveConfig(ctx, a.ID, b.Files, &p.ID); err != nil {
			mapErr(w, err)
			return
		}
		if err := apply(ctx); err != nil {
			s.Log.Error("import write-through", "agent", a.ID, "err", err)
			writeErr(w, http.StatusInternalServerError, "Config gespeichert, aber Übernahme in Tools/Egress fehlgeschlagen: "+err.Error())
			return
		}
	}

	for _, g := range b.Guardrails {
		aid := a.ID
		rule, err := s.Rails.Create(ctx, guardrails.Rule{
			OrgID: p.OrgID, ScopeLevel: "agent", AgentID: &aid,
			RuleType: g.RuleType, Pattern: g.Pattern, Params: g.Params,
		})
		if err != nil {
			mapErr(w, err)
			return
		}
		if !g.Enabled {
			if _, err := s.Rails.SetEnabled(ctx, p.OrgID, rule.ID, false); err != nil {
				mapErr(w, err)
				return
			}
		}
	}

	// Secrets: vorhandene Org-Secrets per Name wieder zuweisen; alles, was es
	// hier nicht gibt, als Warnung melden — Werte reisen nie im Bundle.
	if b.Secrets != nil {
		if !canReadSecretKeys(p.Role) {
			warnings = append(warnings, "secrets-zuweisung erfordert platform_admin oder security — übersprungen")
		} else {
			keys, err := s.Secrets.Keys(ctx, p.OrgID)
			if err != nil {
				mapErr(w, err)
				return
			}
			have := map[string]bool{}
			for _, k := range keys {
				have[k] = true
			}
			for _, k := range b.Secrets.OrgKeys {
				if !have[k] {
					warnings = append(warnings, "org-secret "+k+" fehlt auf dieser Instanz — anlegen und dem Agenten zuweisen")
					continue
				}
				if err := s.Secrets.Assign(ctx, p.OrgID, k, a.ID); err != nil {
					mapErr(w, err)
					return
				}
			}
			for _, k := range b.Secrets.AgentKeys {
				warnings = append(warnings, "agent-eigenes Secret "+k+" muss manuell neu gesetzt werden (Werte werden nie exportiert)")
			}
		}
	}

	a, err = s.Registry.Get(ctx, a.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": a, "warnings": warnings})
}
