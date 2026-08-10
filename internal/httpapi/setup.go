package httpapi

// Setup: the credential first, then let the credential do the work
// (spec/20-hiring-and-setup.md).
//
// Three cards, each of which can be skipped: the engine and its credential, what
// this organisation does, and the People department. Skipping is safe because
// everything here can also be done by hand afterwards — the secrets and runtimes
// pages, the template library, the manual agent form. What the setup buys is not
// exclusivity but ORDER: without a credential nothing the interface offers can
// actually run, and with one most of the rest can be done for the person rather
// than by them.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/examples"
	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/llm"
)

// setupState is what the interface needs to draw the three cards: what is done,
// and what can be chosen.
type instanceSetupState struct {
	// EngineDone: a runtime with at least one credential exists.
	EngineDone bool `json:"engine_done"`
	// OrgDone: the organisation has described itself.
	OrgDone bool `json:"org_done"`
	// PeopleDone: a People department exists (draft or hired — it is there).
	PeopleDone bool   `json:"people_done"`
	PeopleID   string `json:"people_id,omitempty"`
	// Engines are the registered engines with their declared credentials — the
	// card renders itself from this, so a new engine brings its own setup step.
	Engines []daemon.RuntimeDescriptor `json:"engines"`
	// OrgName/OrgDescription prefill card 2.
	OrgName        string `json:"org_name"`
	OrgDescription string `json:"org_description"`
	// LLMAvailable: can the control plane personalise the People department for
	// this organisation (tier 2), or does it stay with the base bundle?
	LLMAvailable bool `json:"llm_available"`
}

func (s *Server) handleSetupState(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ctx := r.Context()
	st := instanceSetupState{Engines: daemon.Runtimes()}

	if s.Runtimes != nil {
		if list, err := s.Runtimes.List(ctx, p.OrgID); err == nil {
			for _, rt := range list {
				if len(rt.Credentials) > 0 {
					st.EngineDone = true
					break
				}
			}
		}
	}
	if o, err := s.Org.GetOrg(ctx, p.OrgID); err == nil {
		st.OrgName = o.Name
		st.OrgDescription = o.Description
		st.OrgDone = strings.TrimSpace(o.Description) != ""
	}
	if a, err := s.Registry.GetBySlug(ctx, p.OrgID, peopleSlug); err == nil {
		st.PeopleDone = true
		st.PeopleID = a.ID.String()
	}
	st.LLMAvailable = llm.Available(ctx, s.Secrets, p.OrgID)
	writeJSON(w, http.StatusOK, st)
}

// handleSetupEngine is card 1: the engine, its credential, and the workplace
// around it.
//
// The value is checked BEFORE it is stored. A wrong credential has to fail here
// and not three clicks later, where it arrives as a sandbox error nobody can
// attribute — which is exactly what used to happen with the two values that get
// mixed up most often (an API key filed as a subscription token and the other
// way round).
func (s *Server) handleSetupEngine(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Engine string `json:"engine"`
		Kind   string `json:"kind"` // daemon.CredAPIKey | daemon.CredSubscription
		Value  string `json:"value"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Value) == "" {
		writeErr(w, http.StatusBadRequest, "engine, kind and value are required")
		return
	}
	d, ok := daemon.Describe(strings.TrimSpace(in.Engine))
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine: "+in.Engine)
		return
	}
	cred, ok := d.Credential(strings.TrimSpace(in.Kind))
	if !ok {
		writeErr(w, http.StatusBadRequest,
			"the engine "+d.Name+" knows no credential of kind "+in.Kind)
		return
	}

	check := checkCredential(r.Context(), cred.Secret, in.Value)
	if check.Checked && !check.Valid {
		// Deliberately a 400 and not a warning: this is the one moment where
		// somebody is looking, and the hint says what is wrong.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": check.Hint, "check": check,
		})
		return
	}
	if err := s.Secrets.Put(r.Context(), p.OrgID, cred.Secret, strings.TrimSpace(in.Value)); err != nil {
		mapErr(w, err)
		return
	}
	if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, cred.Secret); err != nil {
		s.Log.Warn("setup: credential not marked sensitive", "key", cred.Secret, "err", err)
	}
	// The workplace around the credential: from here on there is a contract that
	// agents can be assigned to, instead of a loose secret the runtime happens
	// to find (spec/18).
	s.syncDefaultRuntime(r.Context(), p.OrgID, cred.Secret)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": check, "secret": cred.Secret})
}

// handleSetupOrg is card 2: what this organisation does. Deliberately its own
// endpoint next to PATCH /org/description — the setup may also set the name,
// and it is the one place where both are asked in one breath.
func (s *Server) handleSetupOrg(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		if err := s.Org.RenameOrg(r.Context(), p.OrgID, name); err != nil {
			mapErr(w, err)
			return
		}
	}
	if err := s.Org.SetOrgDescription(r.Context(), p.OrgID, strings.TrimSpace(in.Description)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// peopleSlug is the People department's fixed slug. Fixed because several
// places have to find it again — the setup state, the brief from the agent
// form, and whoever wants to know later whether this organisation has one.
const peopleSlug = "people"

// handleSetupPeople is card 3: the People department.
//
// Three tiers, each of which stands on its own, and the highest available one
// wins (spec/20): the base bundle always, personalisation through the control
// plane where a credential allows it, and the self-onboarding as its first
// task — which is where the setup ends not with "you can get started now" but
// with three applications.
func (s *Server) handleSetupPeople(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	ctx := r.Context()
	var in struct {
		DisplayName string `json:"display_name"`
		Slug        string `json:"slug"`
		// Onboard: give the People department its own first assignment right
		// away (tier 3). Off means: it exists, and it waits for a brief.
		Onboard bool `json:"onboard"`
	}
	_ = readJSON(r, &in)

	if existing, err := s.Registry.GetBySlug(ctx, p.OrgID, peopleSlug); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"agent": existing, "existed": true})
		return
	}

	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name = agents.RollName(langFrom(r)).Name
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = peopleSlug
	}

	bundle, err := peopleBundle(langFrom(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "the People department bundle is not readable: "+err.Error())
		return
	}
	org, _ := s.Org.GetOrg(ctx, p.OrgID)

	// Tier 2: personalise on top of the base. Best effort by design — the base
	// bundle is a working agent, and a provider that does not answer must not
	// cost the card.
	if provider, perr := llm.Resolve(ctx, s.Secrets, p.OrgID); perr == nil && strings.TrimSpace(org.Description) != "" {
		if files, ferr := personaliseFiles(ctx, provider, bundle.Files, org.Name, org.Description); ferr == nil {
			bundle.Files = files
		} else {
			s.Log.Info("setup: People department not personalised — base bundle stays", "err", ferr)
		}
	}
	// The company description belongs in the config in any case: whether the
	// model rephrased it or not, the agent has to know whose house it is in.
	bundle.Files["ORG.md"] = appendCompanySection(bundle.Files["ORG.md"], org.Name, org.Description)

	dept, err := s.ensureDepartment(ctx, p.OrgID, peopleDepartmentName(langFrom(r)))
	if err != nil {
		s.Log.Warn("setup: People & Culture department not created", "err", err)
	}

	created, err := s.Registry.CreateDraft(ctx, p.OrgID, slug, name, bundle.Agent.Runtime, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := s.Backlog.SeedDefaultStages(ctx, created.ID); err != nil {
		s.Log.Warn("setup: board not seeded", "agent", created.Slug, "err", err)
	}
	s.attachDefaultRuntime(ctx, p.OrgID, created)
	if jt := strings.TrimSpace(bundle.Agent.JobTitle); jt != "" {
		if _, err := s.Registry.UpdateProfile(ctx, p.OrgID, created.ID, agents.ProfileUpdate{JobTitle: &jt}); err != nil {
			s.Log.Warn("setup: job title not set", "err", err)
		}
	}
	if dept != uuid.Nil {
		if err := s.Registry.SetDepartment(ctx, created.ID, &dept); err != nil {
			s.Log.Warn("setup: department not assigned", "err", err)
		}
	}
	// Supervisor: whoever set it up. An agent whose escalation path points
	// nowhere escalates into the void.
	if err := s.Registry.SetSupervisor(ctx, created.ID, &p.ID); err != nil {
		s.Log.Warn("setup: supervisor not set", "err", err)
	}
	if _, err := s.Registry.SaveConfig(ctx, created.ID, bundle.Files, &p.ID); err != nil {
		mapErr(w, err)
		return
	}

	var firstTask *backlog.Task
	if in.Onboard {
		t, err := s.Backlog.Create(ctx, p.OrgID, created.ID,
			onboardingTitle(langFrom(r)), onboardingBody(langFrom(r), org.Name, org.Description), "setup", 5)
		if err != nil {
			s.Log.Warn("setup: first assignment not created", "err", err)
		} else {
			firstTask = &t
		}
	}

	agent, _ := s.Registry.Get(ctx, created.ID)
	out := map[string]any{"agent": agent, "existed": false}
	if firstTask != nil {
		out["task"] = firstTask
		// The assignment waits: a draft is not dispatched. That is the point —
		// the human looks at the People department before it starts working.
		out["note"] = "the first assignment is queued and starts when the agent is hired"
	}
	writeJSON(w, http.StatusCreated, out)
}

// ensureDepartment finds or creates a department by name.
func (s *Server) ensureDepartment(ctx context.Context, orgID uuid.UUID, name string) (uuid.UUID, error) {
	list, err := s.Org.ListDepartments(ctx, orgID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, name) {
			return d.ID, nil
		}
	}
	d, err := s.Org.CreateDepartment(ctx, orgID, name, "", "")
	if err != nil {
		return uuid.Nil, err
	}
	return d.ID, nil
}

func peopleDepartmentName(lang string) string {
	if strings.HasPrefix(strings.ToLower(lang), "de") {
		return "People & Culture"
	}
	return "People & Culture"
}

// appendCompanySection puts the company description into ORG.md — as its own
// section, so a later edit of the description can find and replace it.
func appendCompanySection(orgMD, name, description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return orgMD
	}
	section := "\n## " + strings.TrimSpace(name) + "\n\n" + description + "\n"
	return strings.TrimRight(orgMD, "\n") + "\n" + section
}

// personaliseFiles is tier 2: one control-plane call that rewrites the base
// bundle for this company. The visible payoff of card 2 — you type three
// sentences, press on, and the preview shows a People department that talks
// about your business.
//
// Strictly guarded: only files that already exist may come back, and only with
// content. A model that answers with something unusable leaves the base bundle
// in place rather than an empty agent.
func personaliseFiles(ctx context.Context, provider llm.Provider, files map[string]string, orgName, orgDescription string) (map[string]string, error) {
	system := `You are adapting the configuration of an agent for a specific company.

The agent is the "People department" of a platform on which AI agents work like
employees: it turns a description of a job into a complete agent configuration.
Its configuration is given below, and it works as it stands. Your job is a
TARGETED adaptation to this company — not a rewrite.

Rules:
- Keep structure, headings and length. Change sentences, not the architecture.
- Keep every limit exactly as it is (what the agent may not do stays).
- Do not invent target systems, tools or responsibilities.
- Answer in the language the configuration is written in.
- Only touch SOUL.md and CAPABILITIES.md. Leave everything else alone.

Answer EXCLUSIVELY with a single JSON object, no markdown fence, no text
before or after it:
{"files": {"SOUL.md": "<complete new content>", "CAPABILITIES.md": "<…>"}}`

	var b strings.Builder
	b.WriteString("## The company\nName: " + orgName + "\n" + orgDescription + "\n\n")
	b.WriteString("## The configuration so far\n")
	for _, name := range []string{"SOUL.md", "CAPABILITIES.md"} {
		if content := strings.TrimSpace(files[name]); content != "" {
			b.WriteString("### " + name + "\n```\n" + content + "\n```\n")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := provider.Complete(ctx, llm.Request{
		Tier: llm.TierBest, MaxTokens: 8000, System: system,
		Messages: []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(trimToJSON(raw)), &parsed); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	changed := 0
	for name, content := range parsed.Files {
		// Only files that were already there, and only with content. Everything
		// else is a model that took liberties — the base bundle is better than a
		// half-invented one.
		if _, known := files[name]; !known || strings.TrimSpace(content) == "" {
			continue
		}
		out[name] = content
		changed++
	}
	if changed == 0 {
		return nil, errNothingPersonalised
	}
	return out, nil
}

var errNothingPersonalised = errorString("the model changed no file")

type errorString string

func (e errorString) Error() string { return string(e) }

// trimToJSON narrows a model answer down to the JSON object inside it — the
// same tolerance the config copilot applies (parseAssistReply).
func trimToJSON(raw string) string {
	txt := strings.TrimSpace(raw)
	if strings.HasPrefix(txt, "```") {
		if i := strings.IndexByte(txt, '\n'); i >= 0 {
			txt = txt[i+1:]
		}
		txt = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(txt), "```"))
	}
	if i := strings.IndexByte(txt, '{'); i > 0 {
		txt = txt[i:]
	}
	if j := strings.LastIndexByte(txt, '}'); j >= 0 && j < len(txt)-1 {
		txt = txt[:j+1]
	}
	return txt
}

// peopleBundle reads the bundled People department template — the same file the
// template library offers, so there is one source and not a second, hardcoded
// config next to it.
func peopleBundle(lang string) (agentBundle, error) {
	var b agentBundle
	for _, builtin := range examples.Builtins() {
		if builtin.Key != "builtin:people-department" {
			continue
		}
		if err := json.Unmarshal(builtin.LocalizedBundle(lang), &b); err != nil {
			return b, err
		}
		return b, nil
	}
	return b, errorString("the People department bundle is not part of this build")
}

// The People department's own first assignment (tier 3). It runs in the
// sandbox, so it works on any engine — and it ends with three drafts a human
// looks through, which is what turns the end of setup from "you can get started
// now" into "here are three applications".
func onboardingTitle(lang string) string {
	if strings.HasPrefix(strings.ToLower(lang), "de") {
		return "Erste Kollegen vorschlagen"
	}
	return "Propose the first colleagues"
}

func onboardingBody(lang, orgName, orgDescription string) string {
	de := strings.HasPrefix(strings.ToLower(lang), "de")
	var b strings.Builder
	if de {
		b.WriteString("Du bist gerade eingerichtet worden. Dein erster Auftrag ist dein eigener.\n\n")
		b.WriteString("## Unternehmen\n" + orgName + "\n")
		if strings.TrimSpace(orgDescription) != "" {
			b.WriteString(strings.TrimSpace(orgDescription) + "\n")
		}
		b.WriteString(`
## Auftrag
1. Sieh nach, was da ist: covey/list_targets (welche Zielsysteme angeschlossen
   sind) und covey/org_chart (Abteilungen, Menschen, bestehende Agenten).
2. Entwirf DREI Agenten, die zu genau diesem Unternehmen und zu genau diesen
   angeschlossenen Systemen passen. Keine Lehrbuchrollen — was hier tatsaechlich
   Arbeit abnimmt.
3. Ist noch kein einziges Zielsystem angeschlossen, entwirf stattdessen einen
   einzigen Agenten, der ohne externes System auskommt, und sag im Bericht,
   welche Systeme zuerst angeschlossen werden sollten.
4. Berichte am Ende: wen du entworfen hast, was er tun soll, welche Zugaenge er
   braucht und was der Mensch vor dem Einstellen entscheiden muss.
`)
		return b.String()
	}
	b.WriteString("You have just been set up. Your first assignment is your own.\n\n")
	b.WriteString("## The company\n" + orgName + "\n")
	if strings.TrimSpace(orgDescription) != "" {
		b.WriteString(strings.TrimSpace(orgDescription) + "\n")
	}
	b.WriteString(`
## Assignment
1. Look at what is there: covey/list_targets (which target systems are
   connected) and covey/org_chart (departments, people, existing agents).
2. Draft THREE agents that fit this particular company and these particular
   connected systems. No textbook roles — whatever actually takes work off
   somebody's hands here.
3. If no target system is connected yet, draft a single agent instead that gets
   by without an external system, and say in your report which systems should be
   connected first.
4. Report at the end: whom you drafted, what they should do, which access they
   need and what the human has to decide before hiring.
`)
	return b.String()
}
