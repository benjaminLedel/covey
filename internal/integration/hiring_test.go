package integration

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestEntwurfArbeitetNicht: a draft exists, can be configured — and does not
// work. The one property the whole hiring design rests on (spec/20).
//
// It is checked over the dispatch and not over a flag: a task at a draft has to
// STAY where it is, and start when the hiring happens. Everything else would be
// a state that only exists in the interface.
func TestEntwurfArbeitetNicht(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// A draft comes about the way an import produces one — via the bundle path.
	bundle := map[string]any{
		"kind": "covey.agent-config", "version": 1,
		"agent": map[string]any{"slug": "entwurf", "display_name": "Entwurf", "runtime": "mock"},
		"files": map[string]string{"SOUL.md": "# Rolle\n\nTest."},
	}
	imported := admin.expect(http.MethodPost, "/api/v1/agents/import", bundle, http.StatusCreated)
	agent := imported["agent"].(map[string]any)
	agentID := agent["id"].(string)
	if agent["hired_at"] != nil {
		t.Fatalf("an imported agent has to be a draft: %v", agent["hired_at"])
	}

	// A task at the draft: accepted, and it waits.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/tasks",
		map[string]any{"title": "Wartet", "body": "Bis zur Einstellung."}, http.StatusCreated)
	// Waking by hand is refused — the button does not exist in the interface,
	// and the endpoint must not be the back door around it.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wake", nil, http.StatusConflict)
	// The dream likewise: it costs a control-plane model call, and "a draft costs
	// nothing" is the whole point of the state. There is also nothing to tidy up —
	// an agent that never ran has no memory.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/dreams", nil, http.StatusConflict)

	// The dispatcher leaves it alone. Deliberately over a stretch of real time:
	// the tick comes back, and it has to keep NOT picking the task up.
	time.Sleep(2 * time.Second)
	tasks := admin.expectList(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil, http.StatusOK)
	if len(tasks) != 1 || tasks[0]["state"] != "open" {
		t.Fatalf("the task of a draft has to stay open: %v", tasks)
	}
	events := admin.expectList(http.MethodGet, "/api/v1/agents/"+agentID+"/recording", nil, http.StatusOK)
	if len(events) != 0 {
		t.Fatalf("a draft must not have produced a run: %v", events)
	}

	// Hiring releases it — and hiring twice is a second click, not an error.
	hired := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/hire", nil, http.StatusOK)
	if hired["hired_at"] == nil {
		t.Fatal("after hiring the first day has to be recorded")
	}
	firstDay := hired["hired_at"]
	again := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/hire", nil, http.StatusOK)
	if again["hired_at"] != firstDay {
		t.Fatalf("the hiring date must not be overwritten: %v → %v", firstDay, again["hired_at"])
	}

	waitFor(t, "the task of the hired agent runs", 30*time.Second, func() bool {
		list := admin.expectList(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil, http.StatusOK)
		return len(list) == 1 && list[0]["state"] != "open"
	})
}

// TestJederWegEndetImEntwurf: no door into the workforce goes past the hiring.
//
// The import path had it from the start; the manual form did not, and that was
// the wrong way round — it is the path that produces the LEAST configuration
// (a slug, a name, nothing else) and the one that used to hand that straight to
// the dispatcher (spec/20, § Who else benefits).
func TestJederWegEndetImEntwurf(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "von-hand", "display_name": "Von Hand", "runtime": "mock"},
		http.StatusCreated)
	if created["hired_at"] != nil {
		t.Fatalf("the manual form has to produce a draft: %v", created["hired_at"])
	}
	agentID := created["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wake", nil, http.StatusConflict)

	hired := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/hire", nil, http.StatusOK)
	if hired["hired_at"] == nil {
		t.Fatal("after hiring the first day has to be recorded")
	}
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wake", nil, http.StatusOK)
}

// TestSetupStrecke walks the three cards: the credential creates the workplace,
// the company description sticks to the organisation, and the People department
// comes into being — as a draft with a waiting first assignment.
func TestSetupStrecke(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	state := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK)
	if state["engine_done"] != false || state["org_done"] != false || state["people_done"] != false {
		t.Fatalf("a fresh organisation has nothing done yet: %v", state)
	}
	if len(state["engines"].([]any)) == 0 {
		t.Fatal("the engine list comes from the registry and must not be empty")
	}

	// Card 2 first, on purpose: the cards may be walked in any order.
	admin.expect(http.MethodPost, "/api/v1/setup/org",
		map[string]string{"name": "Testfirma", "description": "Wir bauen Prüfstände für Windräder."}, http.StatusOK)
	org := admin.expect(http.MethodGet, "/api/v1/org", nil, http.StatusOK)
	if org["description"] != "Wir bauen Prüfstände für Windräder." {
		t.Fatalf("the description has to stick to the organisation: %v", org)
	}

	// Card 3 without a credential: allowed. The People department is a draft and
	// waits for capacity — that is exactly what the state is for.
	created := admin.expect(http.MethodPost, "/api/v1/setup/people",
		map[string]any{"onboard": true}, http.StatusCreated)
	people := created["agent"].(map[string]any)
	if people["hired_at"] != nil {
		t.Fatal("the People department comes into being as a draft")
	}
	if people["slug"] != "people" {
		t.Fatalf("fixed slug expected, got %v", people["slug"])
	}
	peopleID := people["id"].(string)

	cfg := admin.expect(http.MethodGet, "/api/v1/agents/"+peopleID+"/config", nil, http.StatusOK)
	files := cfg["files"].(map[string]any)
	if soul, _ := files["SOUL.md"].(string); soul == "" {
		t.Fatal("the People department has no SOUL.md")
	}
	if orgMD, _ := files["ORG.md"].(string); orgMD == "" ||
		!strings.Contains(orgMD, "Prüfstände") {
		t.Fatalf("the company description belongs in ORG.md: %q", orgMD)
	}

	// Its first assignment is queued and waiting.
	tasks := admin.expectList(http.MethodGet, "/api/v1/agents/"+peopleID+"/backlog", nil, http.StatusOK)
	if len(tasks) != 1 || tasks[0]["state"] != "open" {
		t.Fatalf("the first assignment has to be waiting: %v", tasks)
	}

	// A second call does not create a second one.
	second := admin.expect(http.MethodPost, "/api/v1/setup/people", map[string]any{}, http.StatusOK)
	if second["existed"] != true {
		t.Fatalf("the People department must not be created twice: %v", second)
	}

	state = admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK)
	if state["org_done"] != true || state["people_done"] != true {
		t.Fatalf("the state has to report both cards as done: %v", state)
	}
}

// TestAusschreibung: the agent form ends in an assignment to the People
// department — and says so honestly when there is none.
func TestAusschreibung(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Without a People department: a clear refusal, so the interface can fall
	// back to the manual form instead of parking a task nobody looks at.
	admin.expect(http.MethodPost, "/api/v1/hiring/brief",
		map[string]string{"description": "Erstlinie im Ticketsystem."}, http.StatusPreconditionFailed)

	admin.expect(http.MethodPost, "/api/v1/setup/org",
		map[string]string{"name": "Testfirma", "description": "Wir betreiben Ladesäulen."}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/setup/people", map[string]any{}, http.StatusCreated)

	brief := admin.expect(http.MethodPost, "/api/v1/hiring/brief", map[string]string{
		"description": "Sie soll die Erstlinie im Ticketsystem übernehmen.",
		"department":  "People & Culture",
	}, http.StatusCreated)
	if brief["waiting_for_hire"] != true {
		t.Fatal("as long as the People department is a draft, the brief waits")
	}
	task := brief["task"].(map[string]any)
	body := task["body"].(string)
	// The brief carries the company and the frame — that is what separates an
	// assignment the agent can work from from a wish list.
	for _, want := range []string{"Ladesäulen", "Erstlinie", "People & Culture", "admin@test.local"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the brief is missing %q:\n%s", want, body)
		}
	}

	status := admin.expect(http.MethodGet, "/api/v1/hiring/brief/"+task["id"].(string), nil, http.StatusOK)
	if status["task"] == nil {
		t.Fatalf("the status has to report the assignment: %v", status)
	}
	if drafts, ok := status["drafts"].([]any); !ok || len(drafts) != 0 {
		t.Fatalf("nothing has been drafted yet: %v", status["drafts"])
	}
}

// TestEinstellenNurMitZugang: the hiring actions exist for an agent only if its
// ACCESS.md says so — and only then does its prompt describe them.
//
// The two halves belong together, and that is the point of the test. A
// capability that is described in the prompt and refused by the control plane is
// the worst kind of gate: the agent tries, fails, and reports a platform error
// for something it was never meant to do.
func TestEinstellenNurMitZugang(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	prompt := func(agentID uuid.UUID) string {
		t.Helper()
		task, err := s.backlog.Create(ctx, s.orgID, agentID, "Prompt zeigen", "[mock:prompt]", "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "task done", 20*time.Second, func() bool {
			return s.taskState(task.ID) == backlog.StateDone
		})
		got, err := s.backlog.Get(ctx, task.ID)
		if err != nil || got.Result == nil {
			t.Fatalf("the run has to deliver the system prompt: %v", err)
		}
		return *got.Result
	}

	// Ordinary agent: no word about drafting colleagues.
	ordinary := s.newSupportAgent("gewoehnlich")
	if p := prompt(ordinary.ID); strings.Contains(p, "covey/create_agent") {
		t.Fatal("an agent without the access must not read in its prompt that it can draft colleagues")
	}

	// With the access entry: the section is there.
	drafter, err := s.registry.Create(ctx, s.orgID, "personal", "Personal", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal\n\n## Rolle\nEntwirft Kollegen.",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	p := prompt(drafter.ID)
	for _, want := range []string{"covey/create_agent", "covey/set_agent_config", "covey/list_targets"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the hiring section is incomplete — %q is missing", want)
		}
	}
	// And the one sentence that has to survive every rewrite of that section.
	if !strings.Contains(p, "hire") {
		t.Fatal("the prompt has to say that hiring is not its job")
	}

	// The scope carries the entry, and the entry alone carries nothing.
	//
	// The second case is the one that was silently open: a scope that IS there,
	// gets read like a limit and was never compared against anything — so
	// `scope: agents:read` looked narrow in review and granted create_agent.
	for _, fall := range []struct{ slug, access string }{
		{"ohne-scope", "- system: covey"},
		{"falscher-scope", "- system: covey scope: agents:read"},
	} {
		a, err := s.registry.Create(ctx, s.orgID, fall.slug, fall.slug, "mock", &s.adminID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.registry.SaveConfig(ctx, a.ID, map[string]string{
			"SOUL.md":   "# " + fall.slug + "\n\n## Rolle\nHat den Eintrag, nicht den Scope.",
			"ACCESS.md": fall.access,
		}, &s.adminID); err != nil {
			t.Fatalf("%q has to be storable — it is a line a human writes: %v", fall.access, err)
		}
		if p := prompt(a.ID); strings.Contains(p, "covey/create_agent") {
			t.Fatalf("%q must not put the hiring section in the prompt either — "+
				"otherwise the agent reads a capability the control plane refuses it", fall.access)
		}
	}
}

// TestEntwerfenSchreibtInMehrerenZuegen pins the two properties the first real
// run got wrong (spec/20):
//
//   - set_agent_config MERGES. A model writes a config in two calls — first the
//     character, then the procedures — and the second call used to delete the
//     first silently. What came out looked complete and had no soul.
//   - No config without a SOUL.md. The refusal has to reach the agent while it
//     can still act, not the human afterwards.
//
// Drafting happens in one task; the follow-up in a second one reaches the
// same draft, which is the point of #584.
func TestEntwerfenSchreibtInMehrerenZuegen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	drafter, err := s.registry.Create(ctx, s.orgID, "personal-2", "Personal", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal\n\n## Rolle\nEntwirft Kollegen.",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	run := func(name, body string) string {
		t.Helper()
		task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, name, body, "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "task done", 30*time.Second, func() bool {
			st := s.taskState(task.ID)
			return st == backlog.StateDone || st == backlog.StateFailed
		})
		got, err := s.backlog.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == backlog.StateFailed && got.Error != nil {
			return *got.Error
		}
		if got.Result == nil {
			return ""
		}
		return *got.Result
	}

	// Draft and configure in one assignment, the config in two calls — the
	// second without SOUL.md.
	run("Entwerfen",
		`[mock:action covey/create_agent {"display_name":"Testkollege","slug":"testkollege","runtime":"mock"}]`+
			` [mock:action covey/set_agent_config {"agent":"testkollege","files":{"SOUL.md":"# Testkollege","CAPABILITIES.md":"# Zustaendigkeit"}}]`+
			` [mock:action covey/set_agent_config {"agent":"testkollege","files":{"PLAYBOOKS.md":"# Verfahren"}}]`)

	drafted, err := s.registry.GetBySlug(ctx, s.orgID, "testkollege")
	if err != nil {
		t.Fatalf("the draft is missing: %v", err)
	}
	if !drafted.Draft() {
		t.Fatal("what an agent creates is a draft")
	}
	cfg, err := s.registry.CurrentConfig(ctx, drafted.ID)
	if err != nil {
		t.Fatalf("the draft has no config at all: %v", err)
	}
	for _, want := range []string{"SOUL.md", "CAPABILITIES.md", "PLAYBOOKS.md"} {
		if strings.TrimSpace(cfg.Files[want]) == "" {
			t.Fatalf("%s was lost by the second call — set_agent_config has to merge (files: %v)",
				want, sortedFileNames(cfg.Files))
		}
	}

	// A first config without a soul is refused, with the reason.
	refused := run("Seelenlos entwerfen",
		`[mock:action covey/create_agent {"display_name":"Seelenlos","slug":"seelenlos","runtime":"mock"}]`+
			` [mock:action covey/set_agent_config {"agent":"seelenlos","files":{"PLAYBOOKS.md":"# Verfahren"}}]`)
	if !strings.Contains(refused, "SOUL.md") {
		t.Fatalf("a config without SOUL.md has to be refused with the reason: %s", refused)
	}
	soulless, err := s.registry.GetBySlug(ctx, s.orgID, "seelenlos")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.CurrentConfig(ctx, soulless.ID); err == nil {
		t.Fatal("the refused config must not have been saved")
	}

	// A follow-up is a new task, and it reaches the draft of the first one: the
	// People department finishes what it started (#584).
	nachgefasst := run("Nachfassen",
		`[mock:action covey/set_agent_config {"agent":"testkollege","files":{"HEARTBEAT.md":"# Takt"}}]`)
	if strings.Contains(nachgefasst, "error") {
		t.Fatalf("a follow-up has to reach the earlier draft: %s", nachgefasst)
	}
	if cfg, err := s.registry.CurrentConfig(ctx, drafted.ID); err != nil || cfg.Files["HEARTBEAT.md"] != "# Takt" {
		t.Fatalf("the follow-up's file did not arrive: %v", err)
	}

	// Rule 4: not itself. Its own config stays a proposal.
	selbst := run("Selbst umschreiben",
		`[mock:action covey/set_agent_config {"agent":"personal-2","files":{"SOUL.md":"# Grenzenlos"}}]`)
	if !strings.Contains(selbst, "own configuration") {
		t.Fatalf("an agent may not write its own config: %s", selbst)
	}

	// The foreign case has a subject of its own, so an organisation can put the
	// old boundary back without touching its own drafts.
	if _, err := s.rails.Create(ctx, railRule(s.orgID, guardrails.RuleDenyAction, "covey:set_agent_config:foreign")); err != nil {
		t.Fatal(err)
	}
	gesperrt := run("Fremd trotz Regel",
		`[mock:action covey/set_agent_config {"agent":"testkollege","files":{"SOUL.md":"# Uebernommen"}}]`)
	if !strings.Contains(gesperrt, "covey:set_agent_config:foreign") {
		t.Fatalf("a deny rule on covey:set_agent_config:foreign has to hold: %s", gesperrt)
	}
	if cfg, _ := s.registry.CurrentConfig(ctx, drafted.ID); cfg.Files["SOUL.md"] == "# Uebernommen" {
		t.Fatal("the denied config was written anyway")
	}
}

// What a colleague needs besides its config: a target system switched on, and
// a host on its egress allowlist (#584). Both through the People department's
// own access, neither for itself.
func TestEinrichtenSystemUndEgress(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	drafter, err := s.registry.Create(ctx, s.orgID, "personal-3", "Personal", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	kollege := s.newSupportAgent("kollege-3")

	run := func(name, body string) string {
		t.Helper()
		task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, name, body, "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "task done", 30*time.Second, func() bool {
			st := s.taskState(task.ID)
			return st == backlog.StateDone || st == backlog.StateFailed
		})
		got, err := s.backlog.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == backlog.StateFailed && got.Error != nil {
			return *got.Error
		}
		if got.Result == nil {
			return ""
		}
		return *got.Result
	}

	run("Browser einschalten", `[mock:action covey/enable_target {"system":"browser"}]`)
	plugins, err := s.targets.List(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	on := false
	for _, p := range plugins {
		if p.Name == "browser" {
			on = p.Enabled
		}
	}
	if !on {
		t.Fatal("enable_target did not switch the system on")
	}
	if res := run("Unbekanntes System", `[mock:action covey/enable_target {"system":"gibtsnicht"}]`); !strings.Contains(res, "no target system") {
		t.Fatalf("an unknown system has to be refused by name: %s", res)
	}

	run("Host freigeben", `[mock:action covey/add_egress_host {"agent":"kollege-3","host":"docs.example.org","note":"Handbuch"}]`)
	cfg, err := s.egress.AgentConfig(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].Pattern != "docs.example.org" {
		t.Fatalf("the host did not reach the colleague's allowlist: %+v", cfg.Hosts)
	}

	if res := run("Selbst freigeben", `[mock:action covey/add_egress_host {"agent":"personal-3","host":"example.org"}]`); !strings.Contains(res, "your own egress") {
		t.Fatalf("an agent may not widen its own allowlist: %s", res)
	}
	if own, _ := s.egress.AgentConfig(ctx, drafter.ID); len(own.Hosts) != 0 {
		t.Fatalf("the refused host was written anyway: %+v", own.Hosts)
	}
}

func sortedFileNames(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEinGezeichneterKollegeHatEinenPlatz is #648: a colleague the HR agent
// drafted had no seat, was hired, and every wake was refused for a missing
// credential while the chat said its workplace was starting. Drafted by the
// agent it now sits on its engine's default seat; a draft without one — made
// before — gets it when it is hired.
func TestEinGezeichneterKollegeHatEinenPlatz(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	drafter, err := s.registry.Create(ctx, s.orgID, "personal-648", "Personal", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, "Entwerfen",
		`[mock:action covey/create_agent {"display_name":"Platzkollege","slug":"platzkollege","runtime":"mock"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	gezeichnet, err := s.registry.GetBySlug(ctx, s.orgID, "platzkollege")
	if err != nil {
		t.Fatal(err)
	}
	if gezeichnet.RuntimeID == nil {
		t.Fatal("a colleague drafted by the HR agent has to sit on its engine's seat")
	}

	// A draft without a seat — as the HR agent made them before — gets one
	// when it is hired.
	alt, err := s.registry.CreateDraft(ctx, s.orgID, "ohne-platz", "Ohne Platz", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if alt.RuntimeID != nil {
		t.Fatalf("the old way left the seat empty: %v", alt.RuntimeID)
	}
	admin := teamLogin(t, s)
	admin.expect(http.MethodPost, "/api/v1/agents/"+alt.ID.String()+"/hire", nil, http.StatusOK)
	if got, _ := s.registry.Get(ctx, alt.ID); got.RuntimeID == nil {
		t.Fatal("hiring has to put the agent on a seat")
	}
}
