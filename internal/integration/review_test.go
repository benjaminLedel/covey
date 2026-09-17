package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/examples"
	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
)

// reviewAgent creates an agent with the given covey scope.
func reviewAgent(t *testing.T, s *stack, slug, scope string) agents.Agent {
	t.Helper()
	ctx := context.Background()
	a, err := s.registry.Create(ctx, s.orgID, slug, slug, "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	access := ""
	if scope != "" {
		access = "- system: covey scope: " + scope
	}
	if _, err := s.registry.SaveConfig(ctx, a.ID, map[string]string{
		"SOUL.md":   "# " + slug + "\n\n## Rolle\nTest.",
		"ACCESS.md": access,
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	return a
}

// laufLassen gives the agent a task and waits until it is done —
// returning the result or the error message.
func laufLassen(t *testing.T, s *stack, agent agents.Agent, titel, body string) (backlog.Task, string) {
	t.Helper()
	ctx := context.Background()
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, titel, body, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run finishes: "+titel, 40*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed || st == backlog.StateBlocked
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.Error != nil:
		return got, *got.Error
	case got.Result != nil:
		return got, *got.Result
	}
	return got, ""
}

// TestReviewScopeTrenntDieBeidenHaelften: `agents:review` unlocks reading and
// proposing and NOT the drafting; `agents:write` the other way round. HR
// hires, covey Doctor reads and proposes, and
// neither of the two can do the other's work with the other's access
// (spec/21).
func TestReviewScopeTrenntDieBeidenHaelften(t *testing.T) {
	s := newStack(t)
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	personal := reviewAgent(t, s, "personal", "agents:write")
	reviewAgent(t, s, "kollege", "")

	// covey Doctor may read …
	if _, msg := laufLassen(t, s, betrieb, "Akte lesen",
		`[mock:action covey/work_record {"agent":"kollege","days":30}]`); strings.Contains(msg, "no access") {
		t.Fatalf("agents:review muss die Arbeitsakte freischalten: %s", msg)
	}
	// … and not draft.
	if _, msg := laufLassen(t, s, betrieb, "Entwerfen versuchen",
		`[mock:action covey/create_agent {"display_name":"Heimlich","slug":"heimlich","runtime":"mock"}]`); !strings.Contains(msg, "agents:write") {
		t.Fatalf("agents:review darf nicht entwerfen duerfen: %s", msg)
	}
	if _, err := s.registry.GetBySlug(context.Background(), s.orgID, "heimlich"); err == nil {
		t.Fatal("und es darf auch kein Entwurf entstanden sein")
	}

	// HR the other way round: drafting yes, work record no.
	if _, msg := laufLassen(t, s, personal, "Akte lesen versuchen",
		`[mock:action covey/work_record {"agent":"kollege"}]`); !strings.Contains(msg, "agents:review") {
		t.Fatalf("agents:write darf keine Arbeitsakte lesen: %s", msg)
	}

	// Both sides need to READ a colleague's config — whoever drafts, for the
	// house style, whoever reviews, to know what he is judging.
	for _, a := range []agents.Agent{betrieb, personal} {
		if _, msg := laufLassen(t, s, a, "Config lesen",
			`[mock:action covey/get_agent_config {"agent":"kollege"}]`); strings.Contains(msg, "no access") {
			t.Fatalf("get_agent_config gehoert beiden Scopes (%s): %s", a.Slug, msg)
		}
	}
}

// TestReviewLiestDieEigenenZahlenNicht: the one half of rule 2 that
// stays. The reason is the same one KPIS.md is not compiled into the
// system prompt for: whoever knows what he is measured on works towards
// the measure instead of the thing.
//
// The PROPOSAL about himself is open instead — nothing runs from there, a
// human decides it anyway (spec/20, the open point).
func TestReviewLiestDieEigenenZahlenNicht(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")

	if _, msg := laufLassen(t, s, betrieb, "Eigene Akte",
		`[mock:action covey/work_record {"agent":"betrieb"}]`); !strings.Contains(msg, "your own record") {
		t.Fatalf("die eigene Arbeitsakte muss abgelehnt werden: %s", msg)
	}

	task, _ := laufLassen(t, s, betrieb, "Eigener Vorschlag",
		`[mock:action covey/propose_agent_config {"agent":"betrieb","title":"Rolle schaerfen",`+
			`"rationale":"Nach dem ersten Auftrag weiss ich mehr.","files":{"SOUL.md":"# Betrieb\n\nGeschaerft."}}]`)
	if task.State != backlog.StateDone {
		t.Fatalf("der Vorschlag an sich selbst muss durchgehen: %v", task.State)
	}
	items, err := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("genau ein Vorschlag erwartet: %v %v", items, err)
	}
	if items[0].AgentID != betrieb.ID || *items[0].AuthorAgentID != betrieb.ID {
		t.Fatalf("Absender und Betroffener sind derselbe: %v", items[0])
	}
}

// TestSelbstvorschlagBrauchtKeinenReviewScope: the open point from spec/20.
// Whoever may draft may, after his self-onboarding, propose his OWN config
// — and only that one. For a colleague's it takes the second
// scope, otherwise the separation would be undone through the back door.
func TestSelbstvorschlagBrauchtKeinenReviewScope(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	personal := reviewAgent(t, s, "personal", "agents:write")
	reviewAgent(t, s, "kollege", "")

	task, _ := laufLassen(t, s, personal, "Eigene Config vorschlagen",
		`[mock:action covey/propose_agent_config {"agent":"personal","title":"Rolle schaerfen",`+
			`"rationale":"Das Self-Onboarding hat gezeigt, was fehlt.","files":{"SOUL.md":"# Personal\n\nGeschaerft."}}]`)
	if task.State != backlog.StateDone {
		t.Fatalf("der Selbstvorschlag muss mit agents:write gehen: %v", task.State)
	}

	if _, msg := laufLassen(t, s, personal, "Fremde Config vorschlagen",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Anders",`+
			`"rationale":"Weil.","files":{"SOUL.md":"# Anders"}}]`); !strings.Contains(msg, "agents:review") {
		t.Fatalf("fuer einen Kollegen braucht es den Review-Scope: %s", msg)
	}
	items, err := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("nur der Selbstvorschlag darf entstanden sein: %v %v", items, err)
	}
}

// TestReviewVorschlagLaeuftNicht: propose_agent_config writes an inactive
// version. Rule 4 from spec/20 stands untouched — the new action is
// strictly weaker than set_agent_config, not an extension of it.
func TestReviewVorschlagLaeuftNicht(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	kollege := reviewAgent(t, s, "kollege", "")

	task, _ := laufLassen(t, s, betrieb, "Vorschlagen",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Teilergebnis abschliessen",`+
			`"rationale":"Elf Laeufe endeten am Turn-Limit.","files":{"PLAYBOOKS.md":"## Turn-Limit\n\nSchliesse ab."}}]`)
	if task.State != backlog.StateDone {
		t.Fatalf("der Vorschlag sollte durchgehen: %v", task.State)
	}

	// The colleague's running config is unchanged — from here there is
	// no path to a config that runs.
	cfg, err := s.registry.CurrentConfig(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Files["PLAYBOOKS.md"] != "" {
		t.Fatalf("ein Vorschlag darf keine Version erzeugen: v%d %v", cfg.Version, sortedFileNames(cfg.Files))
	}

	// It lies in the inbox, with author, subject and rationale.
	page := getInbox(t, admin, "?status=open&type=proposal")
	if page.Total != 1 {
		t.Fatalf("der Vorschlag gehoert in den Posteingang: %+v", page)
	}
	items := admin.expectList(http.MethodGet, "/api/v1/improvements?status=pending", nil, http.StatusOK)
	it := items[0]
	if it["agent_slug"] != "kollege" || it["author_slug"] != "betrieb" {
		t.Fatalf("Betroffener und Absender muessen von der Plattform kommen: %v", it)
	}
	if !strings.Contains(it["rationale"].(string), "Turn-Limit") {
		t.Fatalf("die Begruendung gehoert dazu: %v", it["rationale"])
	}
	// Origin: which task it came from is written by the platform.
	if it["task_id"] != task.ID.String() {
		t.Fatalf("die Herkunft muss die Aufgabe sein, aus der er kam: %v", it["task_id"])
	}

	// It does not go without a rationale — a proposal without the observation
	// behind it is one a human cannot decide on.
	if _, msg := laufLassen(t, s, betrieb, "Ohne Begruendung",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Einfach so","files":{"SOUL.md":"# Anders"}}]`); !strings.Contains(msg, "rationale") {
		t.Fatalf("ohne Begruendung muss der Vorschlag abgelehnt werden: %s", msg)
	}

	// And it cannot hand the platform's system to a colleague: otherwise
	// the way around rule 2 would be an accepted proposal.
	if _, msg := laufLassen(t, s, betrieb, "Selbstvermehrung",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Mehr Zugang","rationale":"Weil.","files":{"ACCESS.md":"- system: covey scope: agents:write"}}]`); !strings.Contains(msg, "`covey`") {
		t.Fatalf("ein Vorschlag darf das eigene System nicht weiterreichen: %s", msg)
	}
}

// TestReviewRecordingNurMitFreigabe: it reads facts. A conversation is reachable only over
// an approval, one run at a time — and the approval is bound to exactly
// this run (spec/21, rule 3).
func TestReviewRecordingNurMitFreigabe(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	kollege := reviewAgent(t, s, "kollege", "")

	// Two runs at the colleague, so the binding is checkable.
	laufA, _ := laufLassen(t, s, kollege, "Lauf A", "[mock:result A fertig.]")
	laufB, _ := laufLassen(t, s, kollege, "Lauf B", "[mock:result B fertig.]")

	task, _ := laufLassen(t, s, betrieb, "Lauf A lesen",
		`[mock:action covey/read_recording {"agent":"kollege","task":"`+laufA.ID.String()+`"}]`)
	if task.State != backlog.StateBlocked {
		t.Fatalf("das Lesen eines Gespraechs muss auf einen Menschen warten: %v", task.State)
	}

	// The human sees WHICH run he approves.
	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 || approvals[0]["action"] != "covey:read_recording" {
		t.Fatalf("genau eine Freigabe fuer das Lesen erwartet: %v", approvals)
	}
	params := approvals[0]["params"].(map[string]any)
	binding, _ := params["binding"].(string)
	// The binding names the run — and carries the fingerprint of the
	// remaining parameters behind it, so the approval is bound not only to THIS run, but
	// to exactly this request (bindingOf in hiring.go).
	if params["agent"] != "kollege" || !strings.HasPrefix(binding, laufA.ID.String()+":") {
		t.Fatalf("die Freigabe muss den Lauf benennen: %v", params)
	}
	if binding == laufA.ID.String() {
		t.Fatalf("die Bindung muss ueber den Lauf hinaus die Parameter abdecken: %v", params)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+approvals[0]["id"].(string)+"/decide",
		map[string]any{"approve": true}, http.StatusOK)

	// After the approval the recording comes — and the reading itself stands in
	// the recording of the READER.
	waitFor(t, "the reading happens after the approval", 40*time.Second, func() bool {
		var n int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='recording_read'`,
			betrieb.ID).Scan(&n)
		return n > 0
	})
	var gelesen int
	if err := s.pool.QueryRow(ctx, `SELECT (payload->>'events')::int FROM recording_events
		WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='recording_read'
		  AND payload->>'run'=$2`, betrieb.ID, laufA.ID.String()).Scan(&gelesen); err != nil {
		t.Fatal(err)
	}
	if gelesen == 0 {
		t.Fatal("der freigegebene Lauf muss auch Ereignisse geliefert haben")
	}

	// The approval was for run A. Run B asks again — it is no licence
	// on the action, but the answer to a question.
	zweite, _ := laufLassen(t, s, betrieb, "Lauf B lesen",
		`[mock:action covey/read_recording {"agent":"kollege","task":"`+laufB.ID.String()+`"}]`)
	if zweite.State != backlog.StateBlocked {
		t.Fatalf("die Freigabe fuer Lauf A darf Lauf B nicht oeffnen: %v", zweite.State)
	}
	offen := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	zweiteBindung, _ := offen[0]["params"].(map[string]any)["binding"].(string)
	if len(offen) != 1 || !strings.HasPrefix(zweiteBindung, laufB.ID.String()+":") {
		t.Fatalf("die zweite Freigabe muss den zweiten Lauf benennen: %v", offen)
	}
}

// TestReviewPromptFolgtDemScope: the section stands in the prompt of whoever has the
// scope — and in no other. An agent that reads about an action and
// is then dismissed is ability by hint (spec/20).
func TestReviewPromptFolgtDemScope(t *testing.T) {
	s := newStack(t)
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	personal := reviewAgent(t, s, "personal", "agents:write")
	ohne := reviewAgent(t, s, "ohne", "")

	prompt := func(a agents.Agent) string {
		_, out := laufLassen(t, s, a, "Prompt zeigen", "[mock:prompt]")
		return out
	}

	if p := prompt(betrieb); !strings.Contains(p, "covey/work_record") ||
		strings.Contains(p, "covey/create_agent") {
		t.Fatal("agents:review liest vom Begutachten und nicht vom Entwerfen")
	}
	if p := prompt(personal); !strings.Contains(p, "covey/create_agent") ||
		strings.Contains(p, "covey/work_record") {
		t.Fatal("agents:write liest vom Entwerfen und nicht vom Begutachten")
	}
	if p := prompt(ohne); strings.Contains(p, "covey/work_record") ||
		strings.Contains(p, "covey/create_agent") {
		t.Fatal("ohne Scope steht keines von beidem im Prompt")
	}
}

// TestCoveyDoctorBundle takes the shipped bundle the way a
// human would have it take: import over the API, and afterwards it must be able to do
// what it was built for.
//
// The test stands here and not with the templates because it checks the interplay
// and not the file: the scope in the ACCESS.md unlocks exactly the three
// actions, the prompt section follows it, and the review cycle is
// weekly — every one of these three properties is decided elsewhere and
// here visible together.
func TestCoveyDoctorBundle(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	reviewAgent(t, s, "kollege", "")

	var bundle json.RawMessage
	for _, b := range examples.Builtins() {
		if b.Key == "builtin:improvement-engineer" {
			bundle = b.Bundle
		}
	}
	if bundle == nil {
		t.Fatal("das ausgelieferte Bundle von covey Doctor fehlt")
	}
	imported := admin.expect(http.MethodPost, "/api/v1/agents/import?slug=betrieb",
		bundle, http.StatusCreated)
	id := imported["agent"].(map[string]any)["id"].(string)
	// The bundle ships claude-code; the test has no engine for it.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+id+"/runtime",
		map[string]any{"runtime": "mock"}, http.StatusOK)

	// An import creates a draft — this one too. Hiring is done by
	// a human (spec/20).
	admin.expect(http.MethodPost, "/api/v1/agents/"+id+"/hire", nil, http.StatusOK)

	// The cadence: weekly, one task per cycle and not one per colleague.
	hbs := admin.expectList(http.MethodGet, "/api/v1/agents/"+id+"/heartbeats", nil, http.StatusOK)
	var zyklus map[string]any
	for _, hb := range hbs {
		if hb["source"] == "config" {
			zyklus = hb
		}
	}
	if zyklus == nil {
		t.Fatalf("der Review-Zyklus fehlt: %v", hbs)
	}
	if zyklus["every_seconds"].(float64) != 7*24*3600 {
		t.Fatalf("der Zyklus ist woechentlich: %v", zyklus["every_seconds"])
	}

	agent, err := s.registry.Get(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatal(err)
	}
	// And it can do what it was built for: read and propose.
	if task, msg := laufLassen(t, s, agent, "Akte lesen",
		`[mock:action covey/work_record {"agent":"kollege"}]`); task.State != backlog.StateDone {
		t.Fatalf("das Bundle muss die Arbeitsakte lesen koennen: %v %s", task.State, msg)
	}
	// But not draft — the scope does not carry the other half.
	if _, msg := laufLassen(t, s, agent, "Entwerfen versuchen",
		`[mock:action covey/create_agent {"display_name":"X","slug":"x","runtime":"mock"}]`); !strings.Contains(msg, "agents:write") {
		t.Fatalf("das Bundle darf nicht entwerfen koennen: %s", msg)
	}
}

// TestReviewLandetAufDemProfil is the run as spec/21 describes it: read the
// record, determine the cause, and then write the review — dated, on the
// colleague's page, with the points beside it that only a human
// can do.
//
// The test holds above all the separation: the review waits for no one
// and therefore stands NOT in the inbox; the finding and the issue do.
func TestReviewLandetAufDemProfil(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	kollege := reviewAgent(t, s, "kollege", "")

	task, msg := laufLassen(t, s, betrieb, "Review schreiben",
		`[mock:action covey/write_review {"agent":"kollege","days":30,`+
			`"summary":"## Befund\n\nElf von dreizehn Laeufen endeten am Turn-Limit.",`+
			`"findings":[{"title":"Zwei Warteschlangen mit widersprechenden Erwartungen",`+
			`"detail":"Der Auftrag laesst sich so nicht erfuellen."}],`+
			`"issues":[{"title":"Kein Weg, ein Teilergebnis zurueckzugeben","link":"https://gitlab.example/covey/-/issues/42"}]}]`)
	if task.State != backlog.StateDone {
		t.Fatalf("das Review sollte durchgehen: %v %s", task.State, msg)
	}

	// On the COLLEAGUE's profile, dated, with period and origin.
	revs := admin.expectList(http.MethodGet,
		"/api/v1/agents/"+kollege.ID.String()+"/reviews", nil, http.StatusOK)
	if len(revs) != 1 {
		t.Fatalf("genau ein Review erwartet: %v", revs)
	}
	if !strings.Contains(revs[0]["summary"].(string), "Turn-Limit") {
		t.Fatalf("der Text gehoert dazu: %v", revs[0]["summary"])
	}
	if revs[0]["task_id"] != task.ID.String() {
		t.Fatalf("das Review muss auf den Lauf zeigen, aus dem es kam: %v", revs[0]["task_id"])
	}
	if revs[0]["period_from"] == nil || revs[0]["period_to"] == nil {
		t.Fatalf("ohne Zeitraum ist „elf Abbrueche\" keine Aussage: %v", revs[0])
	}
	// And none stands at the WRITER — it is a statement about the other.
	if own := admin.expectList(http.MethodGet,
		"/api/v1/agents/"+betrieb.ID.String()+"/reviews", nil, http.StatusOK); len(own) != 0 {
		t.Fatalf("das Review gehoert dem Beurteilten, nicht dem Autor: %v", own)
	}

	// The finding and the issue wait for a human — the review does not.
	page := getInbox(t, admin, "?status=open")
	if page.Total != 2 {
		t.Fatalf("Befund und Issue gehoeren in den Posteingang, das Review nicht: %+v", page)
	}
	sorten := map[string]bool{}
	for _, it := range page.Items {
		sorten[it.Type] = true
	}
	if !sorten["finding"] || !sorten["issue"] {
		t.Fatalf("beide Sorten erwartet: %+v", page.Items)
	}
	items := admin.expectList(http.MethodGet, "/api/v1/improvements?kind=issue", nil, http.StatusOK)
	if len(items) != 1 || items[0]["link"] != "https://gitlab.example/covey/-/issues/42" {
		t.Fatalf("beim Issue gehoert die Adresse dazu: %v", items)
	}

	// No text, no review: a colleague with three proposals and without
	// an assessment is a diff without a diagnosis.
	if _, msg := laufLassen(t, s, betrieb, "Review ohne Text",
		`[mock:action covey/write_review {"agent":"kollege"}]`); !strings.Contains(msg, "summary") {
		t.Fatalf("ohne summary muss es abgelehnt werden: %s", msg)
	}

	// And the assessed agent reaches it by no path: there is no
	// action that reads reviews.
	if _, msg := laufLassen(t, s, betrieb, "Reviews lesen versuchen",
		`[mock:action covey/read_reviews {"agent":"kollege"}]`); !strings.Contains(msg, "unknown covey action") {
		t.Fatalf("es darf keine Aktion geben, die Reviews liest: %s", msg)
	}
}

// TestPlattformRepoStehtImPrompt: the third layer (spec/21).
//
// An agent who may only WRITE issues reports symptoms. With read access
// to the source the same finding becomes a diagnosis — but only if he
// reads the state that is also running. The test holds three things: that the
// address comes from the organisation's configuration and not from the
// model, that the prompt points at the running commit, and that the
// section has a SECOND condition — the target system must stand in the agent's
// ACCESS.md. Without it he would read „check it out" in the prompt and at the
// checkout run into the broker's dismissal: ability by hint.
func TestPlattformRepoStehtImPrompt(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	andere := reviewAgent(t, s, "kollege", "")

	prompt := func(a agents.Agent) string {
		_, out := laufLassen(t, s, a, "Prompt zeigen", "[mock:prompt]")
		return out
	}

	// Without an entry none of this stands in the prompt — nobody reads about a
	// repository nobody connected.
	if p := prompt(betrieb); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne Konfiguration darf der Abschnitt nicht erscheinen")
	}

	// Half addresses are dismissed: a half address in the prompt is
	// worse than none.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "gitlab"}, http.StatusBadRequest)
	// And a target system the organisation did not connect, likewise.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "erfunden", "project": "x/y"}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "gitlab", "project": "covey/covey"}, http.StatusOK)

	// The master data alone is NOT enough. covey Doctor does not have GitLab
	// in his ACCESS.md, so the prompt would name a repository that the
	// broker refuses him right after.
	if p := prompt(betrieb); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne Zugang in der ACCESS.md gehoert der Abschnitt nicht in den Prompt")
	}

	// The second half of the setup: the same access as for every other
	// repository, as an ordinary line in the ACCESS.md.
	if _, err := s.registry.SaveConfig(context.Background(), betrieb.ID, map[string]string{
		"SOUL.md":   "# betrieb\n\n## Rolle\nTest.",
		"ACCESS.md": "- system: covey scope: agents:review\n- system: gitlab",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	p := prompt(betrieb)
	if !strings.Contains(p, "The platform you run on") || !strings.Contains(p, "covey/covey") {
		t.Fatalf("die Adresse gehoert in den Prompt: %.400s", p)
	}
	if c := buildinfo.Get().Commit; c != "" && !strings.Contains(p, c) {
		t.Fatalf("der Prompt muss auf den laufenden Commit zeigen (%s)", c)
	}
	// It reports, it does not repair — the boundary stands in the prompt and not
	// only in the bundle's playbook.
	if !strings.Contains(p, "you do not fix") {
		t.Fatal("die Grenze „melden statt reparieren\" gehoert in den Abschnitt")
	}

	// A colleague without the review scope reads none of it: the third layer
	// belongs to this role and not to everyone who has GitLab.
	if p := prompt(andere); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne agents:review gehoert der Abschnitt nicht in den Prompt")
	}
}

// TestPlatformIssueFilingStandsWithoutTheAccessLine: since covey#200 the two
// halves of that section hang on different conditions. Filing goes through the
// control plane and needs only a stored account for the system; reading the
// source still needs the target system in ACCESS.md, because a checkout runs
// with the agent's own credential.
//
// The agent here has no gitlab line at all — and still has to learn how to file,
// because that was the dead end: told to file, no way to do it.
func TestPlatformIssueFilingStandsWithoutTheAccessLine(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	doctor := reviewAgent(t, s, "doctor", "agents:review")
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "gitlab", "project": "covey/covey"}, http.StatusOK)

	prompt := func(a agents.Agent) string {
		_, out := laufLassen(t, s, a, "Prompt zeigen", "[mock:prompt]")
		return out
	}

	// Without a stored account nothing is promised: the platform could not
	// file, so the section says so instead of naming an action that fails.
	if p := prompt(doctor); strings.Contains(p, "The platform you run on") {
		t.Fatalf("ohne hinterlegtes Konto und ohne Lesezugang gehoert der Abschnitt nicht in den Prompt: %.400s", p)
	}

	if err := s.secrets.Put(ctx, s.orgID, "gitlab_token", "bot-token"); err != nil {
		t.Fatal(err)
	}
	p := prompt(doctor)
	if !strings.Contains(p, "The platform you run on") || !strings.Contains(p, "covey/create_issue") {
		t.Fatalf("mit Konto gehoert das Einreichen in den Prompt: %.600s", p)
	}
	if strings.Contains(p, "check it out") {
		t.Fatalf("ohne die ACCESS.md-Zeile darf nichts einen Checkout versprechen: %.600s", p)
	}
	if !strings.Contains(p, "could not read the code") {
		t.Fatalf("und der Bericht soll sagen, was er nicht sehen konnte: %.600s", p)
	}
}
