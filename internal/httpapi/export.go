package httpapi

import (
	"context"
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
	"covey/internal/skills"
)

// Export/import of the complete bot configuration as a JSON bundle.
//
// The bundle is the portable view of everything that configures an agent:
// master data, config files (SOUL.md, HEARTBEAT.md as well as the live
// rendered ACCESS.md/EGRESS.md), board columns, agent-scoped guard rails, the
// egress templates assigned to the agent (including their hosts, so they may
// be missing on the importing instance) and the names of the assigned secrets.
// Secret VALUES never leave the platform (guard rail: write-only) — the import
// reports them as warnings to be followed up manually.

const (
	bundleKind    = "covey.agent-config"
	bundleVersion = 1
)

type bundleAgent struct {
	Slug            string  `json:"slug"`
	DisplayName     string  `json:"display_name"`
	Runtime         string  `json:"runtime"`
	Model           string  `json:"model,omitempty"`
	Effort          string  `json:"effort,omitempty"`
	MaxTurns        int     `json:"max_turns,omitempty"`
	BudgetUSD       float64 `json:"budget_usd,omitempty"`
	SupervisorEmail string  `json:"supervisor_email,omitempty"`
	// JobTitle is the function in the employee profile. Part of the bundle
	// because agents are employees (spec/02): a template that describes a role
	// but leaves the function empty produces a colleague nobody can find in the
	// team directory by what they do.
	JobTitle string `json:"job_title,omitempty"`
	// WebhookEnabled: on import a FRESH token is generated — the token itself
	// is a secret and is never part of the bundle.
	WebhookEnabled bool `json:"webhook_enabled,omitempty"`
	// WarmSandbox keeps the sandbox alive between wake phases (opt-in, e.g. QA).
	WarmSandbox bool `json:"warm_sandbox,omitempty"`
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

// bundleSecrets carries only the NAMES of assigned secrets, never values.
type bundleSecrets struct {
	OrgKeys   []string `json:"org_keys"`
	AgentKeys []string `json:"agent_keys"`
}

// bundleSkill is one skill of the agent, with its full content.
//
// Origin says where it came from: "agent" belongs to this agent alone,
// "library" was a library skill of the organization that is linked to it. Both
// travel along in full — unlike secrets there is nothing confidential here,
// and a bundle that merely names them would produce an agent without its
// procedures on import. Nobody would notice that at import time, only during
// a run.
//
// Files is a map like the files field of the config: the JSON encoder sorts
// map keys, which keeps exported bundles diffable (the bundled
// examples/*.bundle.json live in git).
type bundleSkill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Origin      string            `json:"origin,omitempty"`
	Files       map[string]string `json:"files"`
}

const (
	skillOriginAgent   = "agent"
	skillOriginLibrary = "library"
)

// input turns the bundle entry into the same input the HTTP API processes —
// including frontmatter handling (skillInput.spec). A hand-written bundle may
// therefore carry the SKILL.md with its YAML header.
func (bs bundleSkill) input() skillInput {
	in := skillInput{Name: bs.Name, Description: bs.Description,
		Files: make([]skills.File, 0, len(bs.Files))}
	paths := make([]string, 0, len(bs.Files))
	for p := range bs.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		in.Files = append(in.Files, skills.File{Path: p, Content: bs.Files[p]})
	}
	return in
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
	Skills          []bundleSkill          `json:"skills,omitempty"`
	Secrets         *bundleSecrets         `json:"secrets,omitempty"`
}

// canReadSecretKeys: the same boundary as the secrets endpoints (securityRoles).
func canReadSecretKeys(role string) bool {
	return role == identity.RoleOrgAdmin || role == identity.RoleSecurity
}

// buildBundle assembles the agentBundle for an agent. includeSecrets controls
// whether secret names travel along (only call it for authorized roles).
func (s *Server) buildBundle(ctx context.Context, orgID, agentID uuid.UUID, includeSecrets bool) (agentBundle, error) {
	a, err := s.Registry.Get(ctx, agentID)
	if err != nil {
		return agentBundle{}, err
	}

	b := agentBundle{
		Kind: bundleKind, Version: bundleVersion,
		Agent: bundleAgent{
			Slug: a.Slug, DisplayName: a.DisplayName, Runtime: a.Runtime,
			Model: a.Model, Effort: a.Effort, MaxTurns: a.MaxTurns, BudgetUSD: a.BudgetUSD,
			WebhookEnabled: a.WebhookToken != nil,
			WarmSandbox:    a.WarmSandbox,
			JobTitle:       a.JobTitle,
		},
	}
	if a.SupervisorID != nil {
		if h, err := s.Org.GetHuman(ctx, orgID, *a.SupervisorID); err == nil {
			b.Agent.SupervisorEmail = h.Email
		}
	}

	cv, err := s.Registry.CurrentConfig(ctx, agentID)
	if errors.Is(err, agents.ErrNotFound) {
		cv = agents.ConfigVersion{Files: map[string]string{"SOUL.md": "", "HEARTBEAT.md": ""}}
	} else if err != nil {
		return agentBundle{}, err
	}
	b.Files = cv.Files
	if s.Targets != nil {
		if access, err := s.renderAccessFile(ctx, agentID); err == nil {
			b.Files["ACCESS.md"] = access
		}
	}
	if s.EgressStore != nil {
		if eg, err := s.renderEgressFile(ctx, orgID, agentID); err == nil {
			b.Files["EGRESS.md"] = eg
		}
	}
	delete(b.Files, "TOOLS.md")

	stages, err := s.Backlog.ListStages(ctx, agentID)
	if err != nil {
		return agentBundle{}, err
	}
	for _, st := range stages {
		b.Stages = append(b.Stages, bundleStage{Name: st.Name, Color: st.Color})
	}

	rules, err := s.Rails.List(ctx, orgID)
	if err != nil {
		return agentBundle{}, err
	}
	for _, rule := range rules {
		if rule.ScopeLevel != "agent" || rule.AgentID == nil || *rule.AgentID != agentID {
			continue
		}
		b.Guardrails = append(b.Guardrails, bundleGuardrail{
			RuleType: rule.RuleType, Pattern: rule.Pattern,
			Params: rule.Params, Enabled: rule.Enabled,
		})
	}

	if s.EgressStore != nil {
		cfg, err := s.EgressStore.AgentConfig(ctx, agentID)
		if err != nil {
			return agentBundle{}, err
		}
		tmplList, err := s.EgressStore.ListTemplates(ctx, orgID)
		if err != nil {
			return agentBundle{}, err
		}
		assigned := map[uuid.UUID]bool{}
		for _, tid := range cfg.TemplateIDs {
			assigned[tid] = true
		}
		for _, t := range tmplList {
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

	// Skills: what the agent actually gets (own plus linked ones; on a name
	// clash its own wins — skills.ForAgent). Exactly this resolution belongs
	// in the bundle: it is unambiguous by name and matches what the agent sees
	// during a run.
	if s.Skills != nil {
		found, err := s.Skills.ForAgent(ctx, orgID, agentID)
		if err != nil {
			return agentBundle{}, err
		}
		for _, sk := range found {
			bs := bundleSkill{Name: sk.Name, Description: sk.Description,
				Origin: skillOriginAgent, Files: map[string]string{}}
			if sk.Library() {
				bs.Origin = skillOriginLibrary
			}
			for _, f := range sk.Files {
				bs.Files[f.Path] = f.Content
			}
			b.Skills = append(b.Skills, bs)
		}
	}

	if includeSecrets {
		sec := &bundleSecrets{OrgKeys: []string{}, AgentKeys: []string{}}
		previews, err := s.Secrets.Previews(ctx, orgID)
		if err != nil {
			return agentBundle{}, err
		}
		for _, kp := range previews {
			for _, aid := range kp.AgentIDs {
				if aid == agentID.String() {
					sec.OrgKeys = append(sec.OrgKeys, kp.Key)
				}
			}
		}
		agentPrev, err := s.Secrets.AgentPreviews(ctx, orgID, agentID)
		if err != nil {
			return agentBundle{}, err
		}
		for _, kp := range agentPrev {
			sec.AgentKeys = append(sec.AgentKeys, kp.Key)
		}
		sort.Strings(sec.OrgKeys)
		sort.Strings(sec.AgentKeys)
		b.Secrets = sec
	}
	return b, nil
}

// validateBundleSkills checks all skills of a bundle before anything is
// created — the import only starts creating once the whole bundle holds up.
// Duplicate names are an error and no trifle: both would claim the same
// directory in the agent's home.
func validateBundleSkills(list []bundleSkill) error {
	seen := map[string]bool{}
	for i, bs := range list {
		spec, err := bs.input().checked(nil)
		if err != nil {
			return err
		}
		label := spec.Name
		if label == "" {
			label = fmt.Sprintf("#%d (without name)", i+1)
		}
		if err := skills.Validate(spec); err != nil {
			return fmt.Errorf("skill %s: %w", label, err)
		}
		if seen[spec.Name] {
			return fmt.Errorf("skill %s appears twice in the bundle", label)
		}
		seen[spec.Name] = true
	}
	return nil
}

// importSkills registers the skills of a bundle for an agent.
//
// Two levels, two behaviours:
//
//   - Agent-owned skills belong to this agent alone; for them the bundle is
//     the source of truth, an existing one of the same name is replaced.
//   - Library skills are org-wide. If one of that name already exists there,
//     the EXISTING version is linked instead of overwritten — it may belong to
//     other agents that know nothing about this import. Same behaviour as with
//     the egress templates, warning included.
//
// Additive: skills the agent has and the bundle does not know about stay in
// place. An import should bring capabilities, not quietly take some away.
func (s *Server) importSkills(ctx context.Context, orgID, agentID uuid.UUID, list []bundleSkill) ([]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	if s.Skills == nil {
		return []string{"skills are not configured on this instance — skipped"}, nil
	}
	lib, err := s.Skills.ListLibrary(ctx, orgID)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]uuid.UUID, len(lib))
	for _, sk := range lib {
		existing[sk.Name] = sk.ID
	}

	var warnings []string
	for _, bs := range list {
		in := bs.input()
		if bs.Origin != skillOriginLibrary {
			if _, err := s.Skills.Upsert(ctx, orgID, in.spec(&agentID)); err != nil {
				return warnings, err
			}
			continue
		}
		spec := in.spec(nil)
		id, known := existing[spec.Name]
		if known {
			warnings = append(warnings,
				"library skill "+spec.Name+" already exists — the existing version is linked")
		} else {
			sk, err := s.Skills.Upsert(ctx, orgID, spec)
			if err != nil {
				return warnings, err
			}
			id = sk.ID
			existing[spec.Name] = id
		}
		if err := s.Skills.Assign(ctx, orgID, id, agentID); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

// handleExportAgent — GET /api/v1/agents/{id}/export: the complete
// configuration of an agent as a downloadable JSON bundle.
func (s *Server) handleExportAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	b, err := s.buildBundle(r.Context(), p.OrgID, id, canReadSecretKeys(p.Role))
	if err != nil {
		mapErr(w, err)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	b.ExportedAt = &now
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", b.Agent.Slug+"-config.json"))
	writeJSON(w, http.StatusOK, b)
}

// handleImportConfig — POST /api/v1/agents/{id}/config/import: overwrites the
// configuration of an EXISTING agent from a bundle. Unlike /agents/import
// (which creates a new agent) this path takes ONLY the config files (SOUL.md,
// HEARTBEAT.md, ACCESS.md, EGRESS.md, …) and the skills. Everything else in
// the bundle — master data, board columns, guard rails, egress templates,
// secret assignments — is deliberately ignored. The save and write-through
// path is identical to PUT /config (new version, RBAC via prepareConfigApply):
// if the bundle contains tool allowlists or egress, the same security-role
// boundary applies as in the text editor (403 instead of silently dropping).
func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	var b agentBundle
	if err := readJSON(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bundle not readable: "+err.Error())
		return
	}
	if b.Kind != bundleKind {
		writeErr(w, http.StatusBadRequest, "kind must be "+bundleKind)
		return
	}
	if b.Version != bundleVersion {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("bundle version %d is not supported (expected %d)", b.Version, bundleVersion))
		return
	}
	if len(b.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "bundle contains no config files (files)")
		return
	}
	// Skills are part of the agent's configuration ever since procedures moved
	// there from PLAYBOOKS.md — a bundle import that left them out would yield
	// an agent without half of its craft.
	//
	// Order: ALL checks first (skills and config), then the side effects.
	// Otherwise the skills would already be in the database when the config
	// fails at its RBAC boundary (a bundle with a tools: allowlist is a 403 for
	// agent_owner) — and the caller would see an error although half of it had
	// been applied.
	if err := validateBundleSkills(b.Skills); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	apply, ok := s.prepareConfigWrite(w, r, id, b.Files)
	if !ok {
		return
	}
	// The warnings go to the log: the response of this endpoint is the config
	// version (as with PUT /config).
	warnings, err := s.importSkills(r.Context(), principalFrom(r).OrgID, id, b.Skills)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	for _, warn := range warnings {
		s.Log.Warn("skill import", "agent", id, "note", warn)
	}
	// Dieser Endpunkt schreibt die KONFIGURATION eines bestehenden Agenten —
	// Dateien und Skills. Die Stammdaten daneben (Engine, Modell, Denkaufwand,
	// Turn-Limit) gehören den jeweiligen PATCH-Routen und werden hier bewusst
	// nicht angefasst: ein Config-Import soll nicht nebenbei die Engine
	// umstellen. Ein Bundle trägt sie trotzdem, weil `agents/import` sie
	// braucht — also sagen wir, dass sie liegen bleiben, statt sie still zu
	// schlucken.
	var ignored []string
	if b.Agent.Model != "" {
		ignored = append(ignored, "model")
	}
	if b.Agent.Effort != "" {
		ignored = append(ignored, "effort")
	}
	if b.Agent.MaxTurns > 0 {
		ignored = append(ignored, "max_turns")
	}
	if len(ignored) > 0 {
		s.Log.Warn("config import: agent fields not applied by this endpoint",
			"agent", id, "fields", strings.Join(ignored, ", "),
			"note", "set them through PATCH /agents/{id}/<field> or import as a new agent")
	}
	s.commitConfig(w, r, id, b.Files, apply)
}

// handleImportAgent — POST /api/v1/agents/import: creates a NEW agent from a
// bundle. The slug query param overrides the slug from the bundle (for copies
// resp. collisions). Everything security-relevant keeps the boundaries of the
// individual endpoints: tools/egress/guard rails are only imported by someone
// who would be allowed to change them there as well (fail-closed, 403 instead
// of silently dropping). Secret values are never part of the bundle — existing
// org secrets are re-assigned by name, everything else ends up as a warning in
// the response.
func (s *Server) handleImportAgent(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var b agentBundle
	if err := readJSON(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bundle not readable: "+err.Error())
		return
	}
	if b.Kind != bundleKind {
		writeErr(w, http.StatusBadRequest, "kind must be "+bundleKind)
		return
	}
	if b.Version != bundleVersion {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("bundle version %d is not supported (expected %d)", b.Version, bundleVersion))
		return
	}
	if slug := r.URL.Query().Get("slug"); slug != "" {
		b.Agent.Slug = slug
	}
	if b.Agent.Slug == "" || b.Agent.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "agent.slug and agent.display_name are mandatory")
		return
	}
	ctx := r.Context()

	// --- Up-front validation: check first, create afterwards (no half import). ---
	if _, err := s.Registry.GetBySlug(ctx, p.OrgID, b.Agent.Slug); err == nil {
		writeErr(w, http.StatusConflict, "slug "+b.Agent.Slug+" already exists — ?slug= overrides it")
		return
	} else if !errors.Is(err, agents.ErrNotFound) {
		mapErr(w, err)
		return
	}
	if _, ok := b.Files["TOOLS.md"]; ok {
		writeErr(w, http.StatusBadRequest, "TOOLS.md has been merged into ACCESS.md (tools: attribute per system)")
		return
	}
	if _, err := agents.ParseHeartbeat(b.Files["HEARTBEAT.md"]); err != nil {
		writeErr(w, http.StatusBadRequest, "HEARTBEAT.md: "+err.Error())
		return
	}
	// Der Denkaufwand wird hier geprüft und nicht erst beim Setzen: eine
	// Vertippung im Bundle ist sonst ein Agent, der sauber importiert und dann
	// bei JEDEM Lauf am Runtime-Flag stirbt — mit einem Fehler, der nach
	// Infrastruktur aussieht und nach Bundle-Tippfehler nicht. Gegen die Engine
	// geprüft, auf der er landen wird, nicht gegen eine feste Liste.
	b.Agent.Effort = strings.TrimSpace(b.Agent.Effort)
	importRuntime := b.Agent.Runtime
	if importRuntime == "" {
		importRuntime = agents.DefaultRuntime
	}
	if msg := checkEffort(importRuntime, b.Agent.Effort); msg != "" {
		writeErr(w, http.StatusBadRequest, "agent.effort: "+msg)
		return
	}
	// Placeholder ID for the validation only — the agent does not exist yet.
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
	if err := validateBundleSkills(b.Skills); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// RBAC as at the individual endpoints: guard rails, egress and tool
	// allowlists are only changed by security roles — being contained in the
	// bundle means 403 for other roles, not silent skipping.
	canSecurity := p.Role == identity.RoleOrgAdmin || p.Role == identity.RoleSecurity
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
			writeErr(w, http.StatusForbidden, "bundle contains "+strings.Join(restricted, ", ")+" — import only with org_admin or security")
			return
		}
	}

	var warnings []string

	// Up front: resolve the supervisor by e-mail (best effort).
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
			warnings = append(warnings, "supervisor "+b.Agent.SupervisorEmail+" not found — assignment skipped")
		}
	}

	// Create missing egress templates from the bundle so that the templates:
	// line in EGRESS.md can be resolved.
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
				warnings = append(warnings, "egress template "+bt.Name+" already exists — the existing definition is used")
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
					writeErr(w, http.StatusBadRequest, "egress template "+bt.Name+", host "+h.Pattern+": "+err.Error())
					return
				}
			}
		}
	} else if len(b.EgressTemplates) > 0 {
		warnings = append(warnings, "egress is not available on this instance — templates skipped")
	}

	// --- Creating. From here on the agent exists; subsequent errors would be a
	// half import, which is why everything parse-/RBAC-critical was checked
	// above. ---
	// As a draft (spec/20). An imported agent is precisely the case where
	// something is regularly still missing — a secret the bundle could only name,
	// an access that has to be approved, a playbook written for a different
	// organisation. Until now it started working with all of that open; now
	// somebody looks at it and hires it.
	a, err := s.Registry.CreateDraft(ctx, p.OrgID, b.Agent.Slug, b.Agent.DisplayName, b.Agent.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	warnings = append(warnings, "the agent was created as a draft — check it and hire it, then it starts working")
	// An imported agent needs a workplace just as much as a hand-made one — a
	// ready-made bundle from examples/ is for many people the FIRST agent, and
	// it must not be the one that cannot work.
	s.attachDefaultRuntime(ctx, p.OrgID, a)
	if b.Agent.Model != "" {
		if err := s.Registry.SetModel(ctx, a.ID, b.Agent.Model); err != nil {
			mapErr(w, err)
			return
		}
	}
	if b.Agent.Effort != "" {
		if err := s.Registry.SetEffort(ctx, a.ID, b.Agent.Effort); err != nil {
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
	if b.Agent.WarmSandbox {
		if err := s.Registry.SetWarmSandbox(ctx, a.ID, true); err != nil {
			mapErr(w, err)
			return
		}
	}
	if jt := strings.TrimSpace(b.Agent.JobTitle); jt != "" {
		if _, err := s.Registry.UpdateProfile(ctx, p.OrgID, a.ID, agents.ProfileUpdate{JobTitle: &jt}); err != nil {
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

	// Board: columns from the bundle in order, otherwise the defaults.
	if len(b.Stages) > 0 {
		for _, st := range b.Stages {
			if _, err := s.Backlog.CreateStage(ctx, a.ID, st.Name, st.Color); err != nil {
				mapErr(w, err)
				return
			}
		}
	} else if err := s.Backlog.SeedDefaultStages(ctx, a.ID); err != nil {
		s.Log.Warn("seeding default stages failed", "agent", a.ID, "err", err)
	}

	// Save the config version and apply ACCESS.md/EGRESS.md to the UI stores —
	// the same path as PUT /config.
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
			writeErr(w, http.StatusInternalServerError, "config saved, but applying it to tools/egress failed: "+err.Error())
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

	// Skills: create the agent-owned ones, create the library skills resp. link
	// the existing version.
	skillWarnings, err := s.importSkills(ctx, p.OrgID, a.ID, b.Skills)
	warnings = append(warnings, skillWarnings...)
	if err != nil {
		mapSkillErr(w, err)
		return
	}

	// Secrets: re-assign existing org secrets by name; report everything that
	// does not exist here as a warning — values never travel in the bundle.
	if b.Secrets != nil {
		if !canReadSecretKeys(p.Role) {
			warnings = append(warnings, "assigning secrets requires org_admin or security — skipped")
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
					warnings = append(warnings, "org secret "+k+" is missing on this instance — create it and assign it to the agent")
					continue
				}
				if err := s.Secrets.Assign(ctx, p.OrgID, k, a.ID); err != nil {
					mapErr(w, err)
					return
				}
			}
			for _, k := range b.Secrets.AgentKeys {
				warnings = append(warnings, "agent-owned secret "+k+" must be set again manually (values are never exported)")
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
