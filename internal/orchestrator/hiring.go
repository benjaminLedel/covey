package orchestrator

// Hiring: the meta actions with which an agent drafts another agent
// (spec/20-hiring-and-setup.md).
//
// This is the platform's own target system. It has no external counterpart and
// no credential — it runs here, where the registry is, instead of taking the
// detour through the HTTP API with a token that would have to live somewhere.
//
// Four rules carry it, and all four are enforced here rather than in a prompt.
// The distinction matters more here than anywhere else: a limit in SOUL.md is
// self-binding, and this agent's output is other agents.
//
//  1. What it creates is a DRAFT. It does not run until a human hires it, and
//     there is no op for hiring — an agent may draft a colleague, employing one
//     is a human act.
//  2. NO SELF-PROPAGATION. A drafted agent may not carry the `covey` system in
//     its ACCESS.md. Rejected at the action.
//  3. PROVENANCE is written by the platform, not reported by the model: which
//     task an agent came out of is recorded here.
//  4. ONLY ITS OWN CHILDREN. set_agent_config reaches exactly the agents drafted
//     in the same assignment. A compromised People department cannot rewrite the
//     QA agent's soul — and it cannot rewrite its own either.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/observability"
)

// coveyOp ist eine Meta-Action: ihr Guard-Rail-Subjekt, die Scopes, von denen
// EINER genügt, und ob sie immer einen Menschen fragt.
type coveyOp struct {
	Subject string
	Scopes  []string
	// AlwaysApprove: die Aktion legt jedes Mal eine Freigabe an, auch ohne
	// Guard-Rail-Regel. Genau eine hat das — covey:read_recording, und dort ist
	// es die Regel selbst und keine Voreinstellung: „er liest Fakten. Ein
	// Gespräch ist nur über eine Freigabe erreichbar, ein Lauf auf einmal"
	// (spec/21). Eine Organisation kann das Subjekt zusätzlich ganz verbieten;
	// lockerer als hier geht es nicht.
	AlwaysApprove bool
}

// coveyOps sind die Ops und ihre Subjekte. Alles, was ein Agent gegenüber der
// Plattform selbst tun darf, steht hier — was fehlt, kann nicht aufgerufen
// werden, und deshalb fehlt das Einstellen (spec/20) wie das Entlassen
// (spec/21). Nicht verboten: nicht vorhanden, damit es nichts zu vergessen
// gibt.
var coveyOps = map[string]coveyOp{
	"list_targets":     {Subject: "covey:list_targets", Scopes: []string{scopeWrite}},
	"create_agent":     {Subject: "covey:create_agent", Scopes: []string{scopeWrite}},
	"set_agent_config": {Subject: "covey:set_agent_config", Scopes: []string{scopeWrite}},
	// Die Config eines Kollegen zu LESEN brauchen beide Seiten: wer entwirft,
	// um den Hausstil zu treffen, und wer begutachtet, um zu wissen, worüber er
	// urteilt (spec/21).
	"get_agent_config": {Subject: "covey:get_agent_config", Scopes: []string{scopeWrite, scopeReview}},

	"work_record":    {Subject: "covey:work_record", Scopes: []string{scopeReview}},
	"read_recording": {Subject: "covey:read_recording", Scopes: []string{scopeReview}, AlwaysApprove: true},
	// Der Vorschlag trägt beide Scopes, aber nicht dieselbe Reichweite:
	// `agents:write` erreicht damit NUR die eigene Konfiguration (spec/20, der
	// Selbstvorschlag nach dem Self-Onboarding), `agents:review` auch die eines
	// Kollegen. Geprüft in reviewPropose, wo der Betroffene bekannt ist.
	"propose_agent_config": {Subject: "covey:propose_agent_config", Scopes: []string{scopeReview, scopeWrite}},
	"write_review":         {Subject: "covey:write_review", Scopes: []string{scopeReview}},
}

// hiringSystem is the name of the access in ACCESS.md that unlocks these
// actions: `- system: covey scope: agents:write`. The same word the agent's
// config uses, so the entry says what it does.
const hiringSystem = "covey"

// Die beiden Scopes dieses Systems — und sie sind ERNST, nicht Zierde. Ein
// Scope, der in einer ACCESS.md steht, wie eine Grenze gelesen wird und keine
// ist, ist schlimmer als gar keiner: er lässt die Zeile schmaler aussehen, als
// sie ist, und das hier ist die Zeile, deren Ausgabe andere Agenten sind.
//
// scopeWrite stellt ein (entwirft), scopeReview liest und schlägt vor. Ein
// Agent darf beide halten; die Personalabteilung und der Betriebsingenieur tun
// es bewusst nicht — keiner von beiden kann mit den Zugängen des anderen
// dessen Arbeit machen (spec/21).
const (
	scopeWrite  = "agents:write"
	scopeReview = "agents:review"
	// hiringScope bleibt als Name stehen, wo der Entwurfs-Pfad ihn nennt.
	hiringScope = scopeWrite
)

// mayUseCovey: hat dieser Agent das eigene System der Plattform in seiner
// ACCESS.md, mit einem der Scopes, die die Aktion trägt? Fail-closed — ein
// Zugang, der sich nicht lesen lässt, ist keiner, und ein Eintrag ohne den
// Scope ebenfalls nicht.
func (o *Orchestrator) mayUseCovey(ctx context.Context, agent agents.Agent, scopes ...string) bool {
	accesses, err := o.Registry.Accesses(ctx, agent.ID)
	if err != nil {
		return false
	}
	for _, a := range accesses {
		if a.System != hiringSystem {
			continue
		}
		for _, have := range a.Scopes {
			for _, want := range scopes {
				if strings.EqualFold(strings.TrimSpace(have), want) {
					return true
				}
			}
		}
	}
	return false
}

// mayDraftAgents/mayReviewAgents sind die beiden Fragen, die auch der Prompt
// stellt: der Abschnitt folgt dem Scope. Ein Agent, der in seinem Prompt von
// create_agent liest und dann abgewiesen wird, ist genau die
// Fähigkeit-durch-Andeutung, gegen die diese Datei gebaut ist.
func (o *Orchestrator) mayDraftAgents(ctx context.Context, agent agents.Agent) bool {
	return o.mayUseCovey(ctx, agent, scopeWrite)
}

func (o *Orchestrator) mayReviewAgents(ctx context.Context, agent agents.Agent) bool {
	return o.mayUseCovey(ctx, agent, scopeReview)
}

func (o *Orchestrator) hiring(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring) daemon.InjectHiring {
	fail := func(format string, args ...any) daemon.InjectHiring {
		return daemon.InjectHiring{RequestID: req.RequestID, OK: false, Error: fmt.Sprintf(format, args...)}
	}
	ok := func(v any) daemon.InjectHiring {
		raw, err := json.Marshal(v)
		if err != nil {
			return fail("result not serialisable: %v", err)
		}
		return daemon.InjectHiring{RequestID: req.RequestID, OK: true, Data: raw}
	}

	op := strings.TrimSpace(req.Op)
	def, known := coveyOps[op]
	if !known {
		return fail("unknown covey action %q", op)
	}
	// The gate: without `- system: covey` with the matching scope in ACCESS.md
	// these actions do not exist for this agent. Checked here and not only in
	// the prompt — a prompt can be worked around, and what comes out of these
	// actions is a colleague or a judgement about one. The error names the whole
	// line, scope included: whoever reads it is a human editing a config, and
	// half a line is a second round trip.
	if !o.mayUseCovey(ctx, agent, def.Scopes...) {
		return fail("%s", "this agent has no access to the platform's own system "+
			"(`- system: "+hiringSystem+" scope: "+strings.Join(def.Scopes, "` or `")+"` in ACCESS.md)")
	}
	// Through the guard-rails, like create_task: what comes out of these actions
	// is a colleague or an assessment of one, and that has to be governable
	// centrally rather than in a prompt.
	//
	// create_task geht denselben Weg an einer anderen Stelle — der Action-Proxy
	// fragt dafür checkAction (Subjekt `covey:create_task`, beim Delegieren
	// `covey:create_task:foreign`), was in decideAction landet. Wer hier nach
	// railsAllow sucht, findet es dort nicht und hält die Prüfung für
	// vergessen; sie ist es nicht.
	//
	// Drei Ausgänge, nicht zwei. Steht die Regel auf require_approval — oder
	// fragt die Aktion ohnehin immer —, ist die Aktion NICHT ausgeführt: der
	// Agent bekommt den Korrelationsschlüssel, seine Aufgabe geht blocked, und
	// nach der Entscheidung eines Menschen wiederholt er sie (spec/21).
	params := hiringParams(req)
	if def.AlwaysApprove {
		// Die Freigabe wird an DIESEN Lauf gebunden: eine erteilte Freigabe ist
		// die Antwort auf „darf er dieses Gespräch lesen", nicht auf „darf er
		// Gespräche lesen".
		params["binding"] = strings.TrimSpace(req.Task)
	}
	verdict := o.railsAllow(ctx, agent, taskID, def.Subject, params, def.AlwaysApprove)
	if verdict.Pending {
		return daemon.InjectHiring{RequestID: req.RequestID, Pending: true,
			ApprovalID: verdict.ApprovalID, CorrelationKey: verdict.CorrelationKey}
	}
	if !verdict.Allowed {
		return fail("%s", verdict.Reason)
	}

	switch op {
	case "list_targets":
		return ok(o.hiringTargets(ctx, agent))
	case "get_agent_config":
		return o.hiringGetConfig(ctx, agent, req, ok, fail)
	case "create_agent":
		return o.hiringCreateAgent(ctx, agent, taskID, req, ok, fail)
	case "set_agent_config":
		return o.hiringSetConfig(ctx, agent, taskID, req, ok, fail)
	case "work_record":
		return o.reviewWorkRecord(ctx, agent, req, ok, fail)
	case "read_recording":
		return o.reviewReadRecording(ctx, agent, req, ok, fail)
	case "propose_agent_config":
		return o.reviewPropose(ctx, agent, taskID, req, ok, fail)
	case "write_review":
		return o.reviewWrite(ctx, agent, taskID, req, ok, fail)
	}
	return fail("unknown covey action %q", op)
}

// hiringTarget is one connectable target system as the drafting agent sees it:
// enough to write an honest ACCESS.md, and nothing about credentials.
type hiringTarget struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	Enabled     bool     `json:"enabled"`
}

// hiringTargets reports what the organisation actually has. That is the point
// of the action: a drafted agent whose ACCESS.md names a system nobody has
// connected looks finished and is not.
func (o *Orchestrator) hiringTargets(ctx context.Context, agent agents.Agent) map[string]any {
	out := []hiringTarget{}
	plugins, err := o.Targets.List(ctx, agent.OrgID)
	if err == nil {
		for _, p := range plugins {
			out = append(out, hiringTarget{
				Name: p.Name, Label: p.Label, Description: p.Description,
				Scopes: p.Scopes, Enabled: p.Enabled,
			})
		}
	}
	engines := []string{}
	for _, d := range daemon.Runtimes() {
		engines = append(engines, d.Name)
	}
	return map[string]any{"systems": out, "engines": engines}
}

func (o *Orchestrator) hiringGetConfig(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	slug := strings.TrimSpace(req.Agent)
	if slug == "" {
		return fail("agent is missing (the slug of the colleague whose config you want to read)")
	}
	other, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
	if err != nil {
		return fail("no agent %q in this organisation", slug)
	}
	cfg, err := o.Registry.CurrentConfig(ctx, other.ID)
	if err != nil {
		return ok(map[string]any{"agent": slug, "files": map[string]string{}})
	}
	return ok(map[string]any{"agent": slug, "job_title": other.JobTitle, "files": cfg.Files})
}

func (o *Orchestrator) hiringCreateAgent(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		return fail("display_name is missing")
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = agents.Slugify(name)
	}
	if _, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug); err == nil {
		return fail("the slug %q is taken — pick a different one", slug)
	}
	engine := strings.TrimSpace(req.Runtime)
	if engine == "" {
		engine = agent.Runtime // the drafting agent's own engine is the safe default
	}
	if !daemon.IsRuntime(engine) {
		return fail("unknown engine %q", engine)
	}

	// The draft belongs to whoever is accountable for the drafting agent — an
	// agent has no owner of its own to hand on.
	created, err := o.Registry.CreateDraft(ctx, agent.OrgID, slug, name, engine, agent.OwnerID)
	if err != nil {
		return fail("%v", err)
	}
	if jt := strings.TrimSpace(req.JobTitle); jt != "" {
		if _, err := o.Registry.UpdateProfile(ctx, agent.OrgID, created.ID,
			agents.ProfileUpdate{JobTitle: &jt}); err != nil {
			o.Log.Warn("hiring: job title not set", "agent", created.Slug, "err", err)
		}
	}
	o.hiringPlace(ctx, agent, created, req)

	// Provenance, written by the platform: the interface finds the draft through
	// this instead of trusting a model to hand back an ID whose format it also
	// invented.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "agent_drafted", "drafted_agent": created.ID.String(),
			"slug": created.Slug, "display_name": created.DisplayName})
	if err := o.Backlog.SeedDefaultStages(ctx, created.ID); err != nil {
		o.Log.Warn("hiring: board not seeded", "agent", created.Slug, "err", err)
	}

	return ok(map[string]any{
		"agent": created.Slug, "id": created.ID.String(), "draft": true,
		"note": "The agent is a draft: it does not work until a human hires it. Write its config with set_agent_config.",
	})
}

// hiringPlace puts the draft into the org chart. Best effort by design: a
// department nobody could resolve must not cost the whole draft — the agent
// exists, the human sees where it sits and corrects it in two clicks.
func (o *Orchestrator) hiringPlace(ctx context.Context, agent agents.Agent, created agents.Agent, req daemon.RequestHiring) {
	if dept := strings.TrimSpace(req.Department); dept != "" {
		if id, err := o.findDepartment(ctx, agent.OrgID, dept); err == nil {
			if err := o.Registry.SetDepartment(ctx, created.ID, &id); err != nil {
				o.Log.Warn("hiring: department not set", "agent", created.Slug, "err", err)
			}
		} else {
			o.Log.Info("hiring: department unknown", "agent", created.Slug, "department", dept)
		}
	}
	if sup := strings.TrimSpace(req.Supervisor); sup != "" {
		if id, err := o.findHuman(ctx, agent.OrgID, sup); err == nil {
			if err := o.Registry.SetSupervisor(ctx, created.ID, &id); err != nil {
				o.Log.Warn("hiring: supervisor not set", "agent", created.Slug, "err", err)
			}
		} else {
			o.Log.Info("hiring: supervisor unknown", "agent", created.Slug, "supervisor", sup)
		}
	}
}

func (o *Orchestrator) hiringSetConfig(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	slug := strings.TrimSpace(req.Agent)
	if slug == "" {
		return fail("agent is missing (the slug of the draft you want to configure)")
	}
	target, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
	if err != nil {
		return fail("no agent %q in this organisation", slug)
	}
	// Rule 4: only its own children. Deliberately checked against the drafts of
	// THIS assignment and not against "is a draft" — otherwise one hiring
	// assignment could rewrite the drafts of another.
	if !o.draftedHere(ctx, taskID, target.ID) {
		return fail("agent %q was not drafted in this assignment — you can only configure your own drafts", slug)
	}
	if len(req.Files) == 0 {
		return fail("files is missing (file name → complete content)")
	}
	// MERGED into what is already there, not replacing it.
	//
	// The first version replaced the whole set, and the first real run showed why
	// that is wrong: a model writes a config in two calls — first the character,
	// then the procedures — and the second call silently deleted SOUL.md. What
	// came out looked complete and had no soul. Nothing here needs to DELETE a
	// file, so the forgiving semantics are also the correct ones; whoever wants a
	// file gone writes it empty.
	files := map[string]string{}
	if current, err := o.Registry.CurrentConfig(ctx, target.ID); err == nil {
		for name, content := range current.Files {
			files[name] = content
		}
	}
	for name, content := range req.Files {
		files[name] = content
	}
	// Rule 2: no self-propagation. An agent that could hand on the platform's own
	// target system would be a workforce that grows by itself.
	for _, acc := range agents.ParseAccess(files["ACCESS.md"]) {
		if acc.System == hiringSystem {
			return fail("a drafted agent may not get the system `covey` — drafting colleagues stays with the People department")
		}
	}
	// A config without a SOUL.md is an agent without a character: the platform
	// would compile a prompt that says nothing about who this is. Refused with
	// the reason, so the drafting agent writes the file instead of finishing.
	if strings.TrimSpace(files["SOUL.md"]) == "" {
		return fail("SOUL.md is missing or empty — without it the agent has no character. Write it before you finish.")
	}
	if _, err := o.Registry.SaveConfig(ctx, target.ID, files, nil); err != nil {
		return fail("%v", err)
	}
	written := make([]string, 0, len(req.Files))
	for name := range req.Files {
		written = append(written, name)
	}
	sort.Strings(written)
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "agent_configured", "drafted_agent": target.ID.String(),
			"slug": target.Slug, "files": strings.Join(written, ", ")})
	return ok(map[string]any{"agent": target.Slug, "written": written, "config": sortedKeys(files)})
}

// hiringParams ist das, was in der Freigabe steht — was ein Mensch lesen muss,
// um zu entscheiden. Bewusst nicht die ganze Anfrage: die Dateien einer Config
// sind seitenlang und gehören nicht in eine Zeile im Posteingang. Ihre NAMEN
// beantworten die Frage schon („er will die SOUL.md umschreiben").
func hiringParams(req daemon.RequestHiring) map[string]any {
	out := map[string]any{"op": req.Op}
	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	add("agent", req.Agent)
	add("slug", req.Slug)
	add("display_name", req.DisplayName)
	add("runtime", req.Runtime)
	add("job_title", req.JobTitle)
	add("department", req.Department)
	add("supervisor", req.Supervisor)
	if len(req.Files) > 0 {
		out["files"] = sortedKeys(req.Files)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- Provenance: which drafts came out of which assignment ---

// draftedHere answers whether this assignment drafted that agent.
//
// Read off the recording, the same place the interface reads it from, and
// nowhere else: a second bookkeeping place in memory would be a second truth,
// and this one survives a restart — which matters, because an assignment that
// goes `blocked` and resumes hours later has to be allowed to keep configuring
// its own drafts.
func (o *Orchestrator) draftedHere(ctx context.Context, taskID, agentID uuid.UUID) bool {
	var found bool
	err := o.Pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM recording_events
		WHERE task_id=$1 AND kind=$2 AND payload->>'status'='agent_drafted'
		  AND payload->>'drafted_agent'=$3)`,
		taskID, observability.KindLifecycle, agentID.String()).Scan(&found)
	return err == nil && found
}

// findDepartment resolves a department by name, case-insensitively. The model
// reads and writes names, not IDs — asking it to carry a UUID through a
// multi-turn conversation is asking for an invented one.
func (o *Orchestrator) findDepartment(ctx context.Context, orgID uuid.UUID, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := o.Pool.QueryRow(ctx,
		`SELECT id FROM departments WHERE org_id=$1 AND lower(name)=lower($2) LIMIT 1`, orgID, name).Scan(&id)
	return id, err
}

// findHuman resolves a person by email or display name, case-insensitively.
func (o *Orchestrator) findHuman(ctx context.Context, orgID uuid.UUID, who string) (uuid.UUID, error) {
	var id uuid.UUID
	err := o.Pool.QueryRow(ctx,
		`SELECT id FROM humans WHERE org_id=$1 AND (lower(email)=lower($2) OR lower(display_name)=lower($2))
		 ORDER BY created_at LIMIT 1`, orgID, who).Scan(&id)
	return id, err
}

// railsVerdict ist, was die Guard-Rails zu einer Meta-Action sagen. Drei
// Ausgänge statt zwei: erlaubt, verboten — und „ein Mensch entscheidet".
type railsVerdict struct {
	Allowed        bool
	Reason         string
	Pending        bool
	ApprovalID     string
	CorrelationKey string
}

// railsAllow applies the org-wide guard rails to a meta action. Fail-closed:
// rules that cannot be read forbid, they do not wave through.
//
// Eine require_approval-Regel legt eine Freigabe an und meldet Pending — sie
// verbietet NICHT mehr. Der alte Satz („requires an approval and cannot be
// performed unattended") war der bequeme Ausweg: die Meta-Actions kannten den
// Freigabe-Pfad nicht, den die Zielsystem-Aktionen seit dem MVP gehen. Eine
// Leitplanke, die für eine Klasse von Aktionen still zu einem Verbot wird,
// sagt über sich selbst die Unwahrheit — wer sie setzt, meint „jemand schaut
// drauf" und bekommt „geht nicht" (spec/21).
//
// Die Parameter gehen in die Freigabe: was ein Mensch entscheiden soll, muss
// er lesen können — welcher Agent, welche Datei, welcher Lauf.
func (o *Orchestrator) railsAllow(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	subject string, params map[string]any, alwaysApprove bool) railsVerdict {

	frage := func() railsVerdict {
		raw, err := json.Marshal(params)
		if err != nil {
			raw = json.RawMessage(`{}`)
		}
		gate := o.approvalGate(ctx, agent, taskID, subject, raw, bindingOf(params))
		switch {
		case gate.Error != "":
			return railsVerdict{Reason: "approval could not be created: " + gate.Error}
		case gate.Approved:
			return railsVerdict{Allowed: true}
		}
		return railsVerdict{Pending: true, ApprovalID: gate.ApprovalID, CorrelationKey: gate.CorrelationKey}
	}

	if o.Rails == nil {
		if alwaysApprove {
			return frage()
		}
		return railsVerdict{Allowed: true}
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return railsVerdict{Reason: "guard rails not readable (fail-closed)"}
	}
	verdict := guardrails.Evaluate(rules, agent.ID, subject)
	switch verdict.Decision {
	case guardrails.Deny:
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindGuardrail,
			map[string]any{"rule": verdict.Rule.RuleType, "pattern": verdict.Rule.Pattern,
				"action": subject, "decision": "denied"})
		return railsVerdict{Reason: "forbidden by guard rail: " + subject}
	case guardrails.RequireApproval:
		return frage()
	}
	// Erlaubt — und trotzdem gefragt, wenn die Aktion es immer tut. EIN Gate
	// und nicht zwei: sonst erzeugte eine require_approval-Regel auf derselben
	// Aktion zwei Freigaben hintereinander für einen Handgriff.
	if alwaysApprove {
		return frage()
	}
	return railsVerdict{Allowed: true}
}

// bindingOf zieht die Bindung aus den Parametern. Leer = die Freigabe gilt für
// die Aktion; gesetzt = für genau diesen einen Gegenstand.
func bindingOf(params map[string]any) string {
	if v, ok := params["binding"].(string); ok {
		return v
	}
	return ""
}
