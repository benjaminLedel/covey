package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
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
}
