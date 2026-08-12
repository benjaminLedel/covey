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

// reviewAgent legt einen Agenten mit dem angegebenen covey-Scope an.
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

// laufLassen gibt dem Agenten eine Aufgabe und wartet, bis sie fertig ist —
// mit dem Ergebnis bzw. der Fehlermeldung als Rückgabe.
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

// TestReviewScopeTrenntDieBeidenHaelften: `agents:review` schaltet Lesen und
// Vorschlagen frei und NICHT das Entwerfen; `agents:write` umgekehrt. Die
// Personalabteilung stellt ein, der Betriebsingenieur liest und schlägt vor,
// und keiner von beiden kann mit den Zugängen des anderen dessen Arbeit machen
// (spec/21).
func TestReviewScopeTrenntDieBeidenHaelften(t *testing.T) {
	s := newStack(t)
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	personal := reviewAgent(t, s, "personal", "agents:write")
	reviewAgent(t, s, "kollege", "")

	// Der Betriebsingenieur darf lesen …
	if _, msg := laufLassen(t, s, betrieb, "Akte lesen",
		`[mock:action covey/work_record {"agent":"kollege","days":30}]`); strings.Contains(msg, "no access") {
		t.Fatalf("agents:review muss die Arbeitsakte freischalten: %s", msg)
	}
	// … und nicht entwerfen.
	if _, msg := laufLassen(t, s, betrieb, "Entwerfen versuchen",
		`[mock:action covey/create_agent {"display_name":"Heimlich","slug":"heimlich","runtime":"mock"}]`); !strings.Contains(msg, "agents:write") {
		t.Fatalf("agents:review darf nicht entwerfen duerfen: %s", msg)
	}
	if _, err := s.registry.GetBySlug(context.Background(), s.orgID, "heimlich"); err == nil {
		t.Fatal("und es darf auch kein Entwurf entstanden sein")
	}

	// Die Personalabteilung umgekehrt: entwerfen ja, Arbeitsakte nein.
	if _, msg := laufLassen(t, s, personal, "Akte lesen versuchen",
		`[mock:action covey/work_record {"agent":"kollege"}]`); !strings.Contains(msg, "agents:review") {
		t.Fatalf("agents:write darf keine Arbeitsakte lesen: %s", msg)
	}

	// Die Config eines Kollegen zu LESEN brauchen beide Seiten — wer entwirft,
	// fuer den Hausstil, wer begutachtet, um zu wissen, worueber er urteilt.
	for _, a := range []agents.Agent{betrieb, personal} {
		if _, msg := laufLassen(t, s, a, "Config lesen",
			`[mock:action covey/get_agent_config {"agent":"kollege"}]`); strings.Contains(msg, "no access") {
			t.Fatalf("get_agent_config gehoert beiden Scopes (%s): %s", a.Slug, msg)
		}
	}
}

// TestReviewLiestDieEigenenZahlenNicht: die eine Hälfte von Regel 2, die
// stehen bleibt. Der Grund ist derselbe, aus dem die KPIS.md nicht in den
// Systemprompt kompiliert wird: wer weiß, woran er gemessen wird, arbeitet auf
// das Maß hin statt auf die Sache.
//
// Der VORSCHLAG an sich selbst ist dagegen offen — von dort läuft nichts, ein
// Mensch entscheidet ihn ohnehin (spec/20, der offene Punkt).
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

// TestSelbstvorschlagBrauchtKeinenReviewScope: der offene Punkt aus spec/20.
// Wer entwerfen darf, darf nach seinem Self-Onboarding die EIGENE Konfiguration
// vorschlagen — und nur die. Für die eines Kollegen braucht es den zweiten
// Scope, sonst wäre die Trennung über die Hintertür aufgehoben.
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

// TestReviewVorschlagLaeuftNicht: propose_agent_config schreibt eine inaktive
// Version. Regel 4 aus spec/20 bleibt unangetastet — die neue Aktion ist
// strikt schwächer als set_agent_config, nicht dessen Erweiterung.
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

	// Die laufende Config des Kollegen ist unveraendert — es gibt von hier
	// keinen Weg zu einer Config, die laeuft.
	cfg, err := s.registry.CurrentConfig(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Files["PLAYBOOKS.md"] != "" {
		t.Fatalf("ein Vorschlag darf keine Version erzeugen: v%d %v", cfg.Version, sortedFileNames(cfg.Files))
	}

	// Er liegt im Posteingang, mit Absender, Betroffenem und Begruendung.
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
	// Herkunft: aus welcher Aufgabe er kam, schreibt die Plattform.
	if it["task_id"] != task.ID.String() {
		t.Fatalf("die Herkunft muss die Aufgabe sein, aus der er kam: %v", it["task_id"])
	}

	// Ohne Begruendung geht es nicht — ein Vorschlag ohne die Beobachtung
	// dahinter ist einer, ueber den ein Mensch nicht entscheiden kann.
	if _, msg := laufLassen(t, s, betrieb, "Ohne Begruendung",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Einfach so","files":{"SOUL.md":"# Anders"}}]`); !strings.Contains(msg, "rationale") {
		t.Fatalf("ohne Begruendung muss der Vorschlag abgelehnt werden: %s", msg)
	}

	// Und er kann keinem Kollegen das System der Plattform verschaffen: sonst
	// waere der Weg um Regel 2 herum ein angenommener Vorschlag.
	if _, msg := laufLassen(t, s, betrieb, "Selbstvermehrung",
		`[mock:action covey/propose_agent_config {"agent":"kollege","title":"Mehr Zugang","rationale":"Weil.","files":{"ACCESS.md":"- system: covey scope: agents:write"}}]`); !strings.Contains(msg, "`covey`") {
		t.Fatalf("ein Vorschlag darf das eigene System nicht weiterreichen: %s", msg)
	}
}

// TestReviewRecordingNurMitFreigabe: er liest Fakten. Ein Gespräch ist nur über
// eine Freigabe erreichbar, ein Lauf auf einmal — und die Freigabe ist an genau
// diesen Lauf gebunden (spec/21, Regel 3).
func TestReviewRecordingNurMitFreigabe(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	kollege := reviewAgent(t, s, "kollege", "")

	// Zwei Läufe beim Kollegen, damit die Bindung prüfbar ist.
	laufA, _ := laufLassen(t, s, kollege, "Lauf A", "[mock:result A fertig.]")
	laufB, _ := laufLassen(t, s, kollege, "Lauf B", "[mock:result B fertig.]")

	task, _ := laufLassen(t, s, betrieb, "Lauf A lesen",
		`[mock:action covey/read_recording {"agent":"kollege","task":"`+laufA.ID.String()+`"}]`)
	if task.State != backlog.StateBlocked {
		t.Fatalf("das Lesen eines Gespraechs muss auf einen Menschen warten: %v", task.State)
	}

	// Der Mensch sieht, WELCHEN Lauf er freigibt.
	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 || approvals[0]["action"] != "covey:read_recording" {
		t.Fatalf("genau eine Freigabe fuer das Lesen erwartet: %v", approvals)
	}
	params := approvals[0]["params"].(map[string]any)
	if params["agent"] != "kollege" || params["binding"] != laufA.ID.String() {
		t.Fatalf("die Freigabe muss den Lauf benennen: %v", params)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+approvals[0]["id"].(string)+"/decide",
		map[string]any{"approve": true}, http.StatusOK)

	// Nach der Freigabe kommt das Recording — und das Lesen selbst steht im
	// Recording des LESENDEN.
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

	// Die Freigabe war fuer Lauf A. Lauf B fragt neu — sie ist keine Lizenz
	// auf die Aktion, sondern die Antwort auf eine Frage.
	zweite, _ := laufLassen(t, s, betrieb, "Lauf B lesen",
		`[mock:action covey/read_recording {"agent":"kollege","task":"`+laufB.ID.String()+`"}]`)
	if zweite.State != backlog.StateBlocked {
		t.Fatalf("die Freigabe fuer Lauf A darf Lauf B nicht oeffnen: %v", zweite.State)
	}
	offen := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(offen) != 1 || offen[0]["params"].(map[string]any)["binding"] != laufB.ID.String() {
		t.Fatalf("die zweite Freigabe muss den zweiten Lauf benennen: %v", offen)
	}
}

// TestReviewPromptFolgtDemScope: der Abschnitt steht im Prompt dessen, der den
// Scope hat — und in keinem anderen. Ein Agent, der von einer Aktion liest und
// dann abgewiesen wird, ist Fähigkeit durch Andeutung (spec/20).
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

// TestBetriebsingenieurBundle nimmt das ausgelieferte Bundle den Weg, den ein
// Mensch es nehmen lässt: Import über die API, und danach muss es das können,
// wofür es gebaut ist.
//
// Der Test steht hier und nicht bei den Vorlagen, weil er das Zusammenspiel
// prüft und nicht die Datei: der Scope in der ACCESS.md schaltet genau die drei
// Aktionen frei, der Prompt-Abschnitt folgt ihm, und der Review-Zyklus ist
// wöchentlich — jede dieser drei Eigenschaften ist anderswo entschieden und
// hier zusammen sichtbar.
func TestBetriebsingenieurBundle(t *testing.T) {
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
		t.Fatal("das ausgelieferte Bundle des Betriebsingenieurs fehlt")
	}
	imported := admin.expect(http.MethodPost, "/api/v1/agents/import?slug=betrieb",
		bundle, http.StatusCreated)
	id := imported["agent"].(map[string]any)["id"].(string)
	// Das Bundle liefert claude-code aus; der Test hat keine Engine dafuer.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+id+"/runtime",
		map[string]any{"runtime": "mock"}, http.StatusOK)

	// Ein Import erzeugt einen Entwurf — auch dieser. Eingestellt wird von
	// einem Menschen (spec/20).
	admin.expect(http.MethodPost, "/api/v1/agents/"+id+"/hire", nil, http.StatusOK)

	// Der Takt: woechentlich, ein Auftrag je Zyklus und nicht einer je Kollege.
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
	// Und er kann, wofuer er gebaut ist: lesen und vorschlagen.
	if task, msg := laufLassen(t, s, agent, "Akte lesen",
		`[mock:action covey/work_record {"agent":"kollege"}]`); task.State != backlog.StateDone {
		t.Fatalf("das Bundle muss die Arbeitsakte lesen koennen: %v %s", task.State, msg)
	}
	// Aber nicht entwerfen — der Scope traegt die andere Haelfte nicht.
	if _, msg := laufLassen(t, s, agent, "Entwerfen versuchen",
		`[mock:action covey/create_agent {"display_name":"X","slug":"x","runtime":"mock"}]`); !strings.Contains(msg, "agents:write") {
		t.Fatalf("das Bundle darf nicht entwerfen koennen: %s", msg)
	}
}

// TestReviewLandetAufDemProfil ist der Lauf, wie spec/21 ihn beschreibt: Akte
// lesen, Ursache bestimmen, und dann das Review schreiben — datiert, auf der
// Seite des Kollegen, mit den Punkten daneben, die nur ein Mensch erledigen
// kann.
//
// Der Test haelt vor allem die Trennung fest: das Review wartet auf niemanden
// und steht deshalb NICHT im Posteingang; der Befund und das Issue schon.
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

	// Auf dem Profil des KOLLEGEN, datiert, mit Zeitraum und Herkunft.
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
	// Und beim SCHREIBER steht keines — es ist eine Aussage ueber den anderen.
	if own := admin.expectList(http.MethodGet,
		"/api/v1/agents/"+betrieb.ID.String()+"/reviews", nil, http.StatusOK); len(own) != 0 {
		t.Fatalf("das Review gehoert dem Beurteilten, nicht dem Autor: %v", own)
	}

	// Der Befund und das Issue warten auf einen Menschen — das Review nicht.
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

	// Ohne Text kein Review: ein Kollege mit drei Vorschlaegen und ohne
	// Beurteilung ist ein Diff ohne Diagnose.
	if _, msg := laufLassen(t, s, betrieb, "Review ohne Text",
		`[mock:action covey/write_review {"agent":"kollege"}]`); !strings.Contains(msg, "summary") {
		t.Fatalf("ohne summary muss es abgelehnt werden: %s", msg)
	}

	// Und der beurteilte Agent erreicht es auf keinem Weg: es gibt keine
	// Aktion, die Reviews liest.
	if _, msg := laufLassen(t, s, betrieb, "Reviews lesen versuchen",
		`[mock:action covey/read_reviews {"agent":"kollege"}]`); !strings.Contains(msg, "unknown covey action") {
		t.Fatalf("es darf keine Aktion geben, die Reviews liest: %s", msg)
	}
}

// TestPlattformRepoStehtImPrompt: die dritte Schicht (spec/21).
//
// Ein Agent, der nur Issues SCHREIBEN darf, meldet Symptome. Mit Lesezugriff
// auf den Quelltext wird aus demselben Befund eine Diagnose — aber nur, wenn er
// den Stand liest, der auch laeuft. Der Test haelt drei Dinge fest: dass die
// Adresse aus der Konfiguration der Organisation kommt und nicht aus dem
// Modell, dass der Prompt auf den laufenden Commit zeigt, und dass der
// Abschnitt eine ZWEITE Bedingung hat — das Zielsystem muss in der ACCESS.md
// des Agenten stehen. Ohne sie laese er im Prompt „check it out" und liefe beim
// Checkout in die Abweisung des Brokers: Faehigkeit durch Andeutung.
func TestPlattformRepoStehtImPrompt(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	betrieb := reviewAgent(t, s, "betrieb", "agents:review")
	andere := reviewAgent(t, s, "kollege", "")

	prompt := func(a agents.Agent) string {
		_, out := laufLassen(t, s, a, "Prompt zeigen", "[mock:prompt]")
		return out
	}

	// Ohne Eintrag steht davon nichts im Prompt — niemand liest ueber ein
	// Repository, das niemand angeschlossen hat.
	if p := prompt(betrieb); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne Konfiguration darf der Abschnitt nicht erscheinen")
	}

	// Halbe Adressen werden abgewiesen: eine halbe Adresse im Prompt ist
	// schlimmer als keine.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "gitlab"}, http.StatusBadRequest)
	// Und ein Zielsystem, das die Organisation nicht angeschlossen hat, auch.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "erfunden", "project": "x/y"}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "gitlab", "project": "covey/covey"}, http.StatusOK)

	// Das Stammdatum allein reicht NICHT. Der Betriebsingenieur hat GitLab
	// nicht in seiner ACCESS.md, also stuende im Prompt ein Repository, das der
	// Broker ihm gleich darauf verweigert.
	if p := prompt(betrieb); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne Zugang in der ACCESS.md gehoert der Abschnitt nicht in den Prompt")
	}

	// Die zweite Haelfte der Einrichtung: derselbe Zugang wie fuer jedes andere
	// Repository auch, als gewoehnliche Zeile in der ACCESS.md.
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
	// Er berichtet, er repariert nicht — die Grenze steht im Prompt und nicht
	// nur im Playbook des Bundles.
	if !strings.Contains(p, "you do not fix") {
		t.Fatal("die Grenze „melden statt reparieren\" gehoert in den Abschnitt")
	}

	// Ein Kollege ohne Review-Scope liest davon nichts: die dritte Schicht
	// gehoert zu dieser Rolle und nicht zu jedem, der GitLab hat.
	if p := prompt(andere); strings.Contains(p, "The platform you run on") {
		t.Fatal("ohne agents:review gehoert der Abschnitt nicht in den Prompt")
	}
}
