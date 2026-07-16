package integration

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestBlockedLoopEndToEnd ist die Abnahme-Checkliste aus spec/11 in Code:
// Wake → Triage (Memory) → Working (gebrokerte Zammad-Aktionen) → Blocked
// (pending + Korrelations-Key) → Wake-on-correlation (Webhook) → Done (Memory).
func TestBlockedLoopEndToEnd(t *testing.T) {
	s := newStack(t)
	zammad := newFakeZammad(t)
	ctx := context.Background()

	// Broker-Secrets: Zammad-Token + URL, AES-GCM-verschlüsselt in Postgres.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "zammad-api-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "zammad_url", zammad.srv.URL); err != nil {
		t.Fatal(err)
	}

	agent := s.newSupportAgent("support")

	// Org-Secrets wirken erst durch explizite Zuweisung an den Agenten.
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_token", agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_url", agent.ID); err != nil {
		t.Fatal(err)
	}

	// Vorwissen einspeisen, damit die Triage etwas findet.
	if err := s.mem.Ingest(ctx, agent.ID, "Kunde 7 hatte schon einmal ein Login-Problem, Lösung: Passwort-Reset.", nil); err != nil {
		t.Fatal(err)
	}

	// Eingehendes Zammad-Ticket via Webhook (Wake-Quelle Event, kein Polling).
	newTicket := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login funktioniert nicht","state":"open"},
		"article":{"id":1,"sender":"Customer","body":"Ich kann mich nicht einloggen. [mock:action zammad/get_ticket {\"ticket_id\":42}] [mock:action zammad/reply {\"ticket_id\":42,\"body\":\"Welchen Browser nutzen Sie?\",\"internal\":true}] [mock:action zammad/set_state {\"ticket_id\":42,\"state\":\"pending reminder\"}] [mock:block key=zammad:ticket:42 question=Warte auf Kundenantwort] [mock:result Ticket gelöst] [mock:memory Ticket 42: Login-Problem, Browser erfragt]","internal":false}}`)
	postWebhook(t, s, "support", newTicket)

	// Der Agent wacht auf, arbeitet und parkt die Aufgabe.
	var taskID string
	waitFor(t, "aufgabe blocked", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, task := range tasks {
			if task.State == backlog.StateBlocked {
				taskID = task.ID.String()
				return true
			}
		}
		return false
	})

	task := mustTask(t, s, taskID)
	if task.CorrelationKey == nil || *task.CorrelationKey != "zammad:ticket:42" {
		t.Fatalf("korrelations-key fehlt: %+v", task.CorrelationKey)
	}
	if task.RuntimeSessionID == nil || *task.RuntimeSessionID == "" {
		t.Fatalf("runtime_session_id (für --resume) fehlt")
	}
	if zammad.replyCount() != 1 {
		t.Fatalf("genau eine (interne) Rückfrage erwartet, got %d", zammad.replyCount())
	}
	if upd := zammad.lastUpdate(); upd == nil || upd["state"] != "pending reminder" {
		t.Fatalf("ticket muss auf pending reminder stehen: %+v", upd)
	}
	waitFor(t, "agent schläft nach blocked", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})

	// Idempotenz: derselbe Webhook (Zammad-Retry) darf nichts Neues auslösen.
	resp := postWebhookRaw(t, s, "support", newTicket)
	if resp != `{"outcome":"duplicate"}` {
		t.Fatalf("retry muss dedupliziert werden, got %s", resp)
	}

	// Kundenantwort → Wake-on-correlation → Resume → Done.
	answer := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login funktioniert nicht","state":"open"},
		"article":{"id":2,"sender":"Customer","body":"Ich nutze Chrome 126.","internal":false}}`)
	postWebhook(t, s, "support", answer)

	waitFor(t, "aufgabe done nach korrelation", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done := mustTask(t, s, taskID)
	if done.Result == nil || *done.Result != "Ticket gelöst" {
		t.Fatalf("ergebnis fehlt: %+v", done.Result)
	}

	// Memory-Ingest beim done-Schritt (M7).
	entries, err := s.mem.Query(ctx, agent.ID, "Login-Problem Ticket", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Content == "Ticket 42: Login-Problem, Browser erfragt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory-episode fehlt, got %+v", entries)
	}

	// Recording lückenlos: lifecycle + runtime + action + credential.
	events, err := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	for _, want := range []string{"lifecycle", "runtime", "action", "credential"} {
		if kinds[want] == 0 {
			t.Fatalf("recording ohne %s-Events: %+v", want, kinds)
		}
	}

	// Kosten sichtbar (aus den cost-Events der Runtime).
	cost, err := s.obs.CostByAgent(ctx, agent.ID)
	if err != nil || cost.TotalUSD <= 0 {
		t.Fatalf("kosten müssen verbucht sein: %+v err=%v", cost, err)
	}

	waitFor(t, "agent schläft am ende", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})
}

// TestApprovalGate: externe Antwort ist approval-pflichtig → Aufgabe parkt
// auf approval:<id>; die menschliche Freigabe weckt sie, die Aktion läuft durch.
func TestApprovalGate(t *testing.T) {
	s := newStack(t)
	zammad := newFakeZammad(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "zammad_token", "tok")
	s.secrets.Put(ctx, s.orgID, "zammad_url", zammad.srv.URL)
	agent := s.newSupportAgent("support")
	s.secrets.Assign(ctx, s.orgID, "zammad_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "zammad_url", agent.ID)

	if _, err := s.rails.Create(ctx, railRule(s.orgID, "require_approval", "zammad:reply_external")); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kunden extern antworten",
		`[mock:action zammad/reply {"ticket_id":42,"body":"Hallo Kunde","internal":false}]
[mock:result Extern geantwortet]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "aufgabe blocked auf approval", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	if zammad.replyCount() != 0 {
		t.Fatal("vor der Freigabe darf nichts rausgehen (fail-closed)")
	}

	approvals, err := s.obs.ListApprovals(ctx, s.orgID, "pending")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("genau ein pending approval erwartet: %v %d", err, len(approvals))
	}
	if approvals[0].Action != "zammad:reply_external" {
		t.Fatalf("falsches approval-subjekt: %s", approvals[0].Action)
	}

	// Mensch gibt frei → Wake-on-correlation → Aktion läuft, Aufgabe done.
	appr, err := s.obs.DecideApproval(ctx, s.orgID, approvals[0].ID, true, &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	s.orch.OnApprovalDecided(ctx, appr)

	waitFor(t, "aufgabe done nach freigabe", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if zammad.replyCount() != 1 {
		t.Fatalf("nach Freigabe genau eine externe Antwort, got %d", zammad.replyCount())
	}
}

// TestGuardrailDeny: deny_action blockt hart, der Agent arbeitet ohne die
// Aktion weiter, die Auslösung steht im Recording.
func TestGuardrailDeny(t *testing.T) {
	s := newStack(t)
	zammad := newFakeZammad(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "zammad_token", "tok")
	s.secrets.Put(ctx, s.orgID, "zammad_url", zammad.srv.URL)
	agent := s.newSupportAgent("support")
	s.secrets.Assign(ctx, s.orgID, "zammad_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "zammad_url", agent.ID)

	if _, err := s.rails.Create(ctx, railRule(s.orgID, "deny_action", "zammad:escalate")); err != nil {
		t.Fatal(err)
	}

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Eskalationsversuch",
		`[mock:action zammad/escalate {"ticket_id":42,"note":"bitte übernehmen"}]
[mock:result Trotzdem fertig]`, "manual", 3)

	waitFor(t, "aufgabe done trotz deny", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if zammad.replyCount() != 0 {
		t.Fatal("die verbotene Eskalation darf Zammad nie erreichen")
	}
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	found := false
	for _, e := range events {
		if e.Kind == "guardrail" {
			found = true
		}
	}
	if !found {
		t.Fatal("guardrail-Auslösung muss im Recording stehen")
	}
}

// TestAgentSetsStage prüft den Agent-Pfad für Custom-Stages: die Runtime ruft
// covey/set_stage; die Control Plane legt die Stage automatisch an und verschiebt
// die Aufgabe dorthin — rein anzeigend, der Lifecycle-State bleibt done.
func TestAgentSetsStage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("stage-agent")
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Stage-Test",
		`[mock:action covey/set_stage {"stage":"Recherche"}]
[mock:result fertig]`, "manual", 3)

	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil || len(stages) != 1 || stages[0].Name != "Recherche" {
		t.Fatalf("der Agent muss die Stage 'Recherche' angelegt haben, got %+v (err=%v)", stages, err)
	}
	got, _ := s.backlog.Get(ctx, task.ID)
	if got.StageID == nil || *got.StageID != stages[0].ID {
		t.Fatalf("aufgabe muss in Stage 'Recherche' liegen, got stage_id=%v", got.StageID)
	}
	if got.State != backlog.StateDone {
		t.Fatalf("set_stage darf den Lifecycle-State nicht ändern, got %q", got.State)
	}
}

// TestBacklogCleanupAndRetry prüft die Aufräum-Logik: terminale Aufgaben
// lassen sich archivieren (ausgeblendet, nicht gelöscht), gescheiterte
// Aufgaben per Retry erneut einplanen — der Agent nimmt sie wirklich wieder auf.
func TestBacklogCleanupAndRetry(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("cleanup-agent")

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert",
		"[mock:fail kaputt]", "manual", 3)
	waitFor(t, "aufgabe failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})

	// Offene Aufgaben sind nicht archivierbar (fail-closed für Aktives).
	open, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Bleibt offen",
		"[mock:sleep 30s]", "manual", 9)
	if _, err := s.backlog.Archive(ctx, open.ID); err == nil {
		t.Fatalf("offene Aufgabe darf nicht archivierbar sein")
	}

	// Aufräumen archiviert genau die terminale Aufgabe.
	n, err := s.backlog.ArchiveTerminal(ctx, agent.ID)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: n=%d err=%v, want 1", n, err)
	}
	active, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
	for _, a := range active {
		if a.ID == task.ID {
			t.Fatalf("archivierte Aufgabe darf im aktiven Backlog nicht auftauchen")
		}
	}
	all, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
	found := false
	for _, a := range all {
		if a.ID == task.ID && a.ArchivedAt != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("archivierte Aufgabe muss mit archived_at im Gesamt-Backlog stehen")
	}

	// Retry holt die gescheiterte (und archivierte) Aufgabe zurück auf open …
	re, err := s.backlog.Retry(ctx, task.ID, "test-retry")
	if err != nil || re.State != backlog.StateOpen || re.Error != nil || re.ArchivedAt != nil {
		t.Fatalf("retry: %+v err=%v, want state=open ohne error/archived_at", re, err)
	}
	// … und der Agent arbeitet sie erneut ab (scheitert deterministisch wieder).
	waitFor(t, "retry erneut verarbeitet", 15*time.Second, func() bool {
		got, _ := s.backlog.Get(ctx, task.ID)
		return got.State == backlog.StateFailed && got.UpdatedAt.After(re.UpdatedAt)
	})
}

// TestStageFollowsState prüft das Auto-Follow der Standard-Spalten: solange
// eine Aufgabe in Backlog/In Arbeit/Erledigt (oder keiner Spalte) liegt, folgt
// die Spalte dem Lifecycle-State; eine bewusst gesetzte eigene Spalte gewinnt.
func TestStageFollowsState(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("follow-agent")
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	stageName := func(id *uuid.UUID) string {
		if id == nil {
			return ""
		}
		stages, _ := s.backlog.ListStages(ctx, agent.ID)
		for _, st := range stages {
			if st.ID == *id {
				return st.Name
			}
		}
		return "?"
	}

	// Erfolgreiche Aufgabe: Backlog → (In Arbeit) → Erledigt.
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Läuft durch", "[mock:result ok]", "manual", 3)
	if got := stageName(task.StageID); got != "Backlog" {
		t.Fatalf("neue Aufgabe muss in 'Backlog' starten, got %q", got)
	}
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done, _ := s.backlog.Get(ctx, task.ID)
	if got := stageName(done.StageID); got != "Erledigt" {
		t.Fatalf("erledigte Aufgabe muss in 'Erledigt' liegen, got %q", got)
	}

	// Eigene Spalte gewinnt: manuell platzierte Aufgaben folgen dem State nicht.
	fail, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert", "[mock:fail kaputt]", "manual", 3)
	waitFor(t, "aufgabe failed", 15*time.Second, func() bool {
		return s.taskState(fail.ID) == backlog.StateFailed
	})
	custom, err := s.backlog.EnsureStage(ctx, agent.ID, "Später")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.SetTaskStage(ctx, fail.ID, &custom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Retry(ctx, fail.ID, "test"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.backlog.Get(ctx, fail.ID)
	if name := stageName(got.StageID); name != "Später" {
		t.Fatalf("manuell platzierte Aufgabe darf beim Retry nicht umziehen, got %q", name)
	}
}

// TestKillSwitch stoppt einen arbeitenden Agenten sofort; die angefangene
// Aufgabe geht zurück ins Backlog.
func TestKillSwitch(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Lange Aufgabe",
		"[mock:sleep 30s]", "manual", 3)

	waitFor(t, "agent arbeitet", 15*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "working"
	})
	if err := s.orch.Kill(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe zurück im backlog", 10*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateOpen
	})
	waitFor(t, "agent steht auf killed", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "killed"
	})

	// Ein getöteter Agent wird vom Dispatcher nicht wieder geweckt.
	time.Sleep(time.Second)
	if got := s.agentStatus(agent.ID); got != "killed" {
		t.Fatalf("killed muss killed bleiben, got %s", got)
	}
}

// TestBudgetGuardrail pausiert den Agenten, sobald die Kosten den Deckel reißen.
func TestBudgetGuardrail(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")
	// Mock-Runtime meldet 0.0123 USD pro Lauf; Deckel darunter.
	if err := s.registry.SetBudget(ctx, agent.ID, 0.01); err != nil {
		t.Fatal(err)
	}

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Teuer", "", "manual", 3)

	waitFor(t, "agent wegen budget pausiert", 15*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "killed"
	})
	waitFor(t, "aufgabe wieder offen", 10*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateOpen
	})
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	found := false
	for _, e := range events {
		if e.Kind == "guardrail" {
			found = true
		}
	}
	if !found {
		t.Fatal("budget-Auslösung muss im Recording stehen")
	}
}

// TestFleetKill: der flottenweite Notaus verhindert jeden Dispatch.
func TestFleetKill(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")

	if err := s.registry.SetFleetKilled(ctx, s.orgID, true); err != nil {
		t.Fatal(err)
	}
	s.backlog.Create(ctx, s.orgID, agent.ID, "Sollte liegen bleiben", "", "manual", 3)

	time.Sleep(1500 * time.Millisecond)
	if got := s.agentStatus(agent.ID); got != "sleeping" {
		t.Fatalf("bei fleet-kill darf nichts anlaufen, got %s", got)
	}

	// Notaus lösen → Arbeit läuft an.
	s.registry.SetFleetKilled(ctx, s.orgID, false)
	waitFor(t, "arbeit läuft nach resume an", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		return len(tasks) == 1 && tasks[0].State == backlog.StateDone
	})
}

// --- Hilfen ---

func postWebhook(t *testing.T, s *stack, slug string, body []byte) {
	t.Helper()
	if got := postWebhookRaw(t, s, slug, body); got == "" {
		t.Fatal("webhook ohne antwort")
	}
}

func postWebhookRaw(t *testing.T, s *stack, slug string, body []byte) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/zammad/"+slug, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", signWebhook(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook HTTP %d: %s", resp.StatusCode, buf.String())
	}
	return strings.TrimSpace(buf.String())
}

func mustTask(t *testing.T, s *stack, id string) backlog.Task {
	t.Helper()
	taskID, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Get(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func railRule(orgID uuid.UUID, ruleType, pattern string) guardrails.Rule {
	return guardrails.Rule{OrgID: orgID, ScopeLevel: "global", RuleType: ruleType, Pattern: pattern}
}
