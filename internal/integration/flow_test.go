package integration

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestBlockedLoopEndToEnd is the acceptance checklist from spec/11 in code:
// wake → triage (memory) → working (brokered Zammad actions) → blocked (pending
// + correlation key) → wake-on-correlation (webhook) → done (memory).
func TestBlockedLoopEndToEnd(t *testing.T) {
	s := newStack(t)
	zammad := newFakeZammad(t)
	ctx := context.Background()

	// Broker secrets: Zammad token + URL, AES-GCM encrypted in Postgres.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "zammad-api-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "zammad_url", zammad.srv.URL); err != nil {
		t.Fatal(err)
	}

	agent := s.newSupportAgent("support")

	// Org secrets only take effect through an explicit assignment to the agent.
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_token", agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_url", agent.ID); err != nil {
		t.Fatal(err)
	}

	// Feed in prior knowledge so that the triage finds something.
	if err := s.mem.Ingest(ctx, agent.ID, "Kunde 7 hatte schon einmal ein Login-Problem, Lösung: Passwort-Reset.", nil); err != nil {
		t.Fatal(err)
	}

	// Incoming Zammad ticket via webhook (wake source event, no polling).
	newTicket := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login funktioniert nicht","state":"open"},
		"article":{"id":1,"sender":"Customer","body":"Ich kann mich nicht einloggen. [mock:action zammad/get_ticket {\"ticket_id\":42}] [mock:action zammad/reply {\"ticket_id\":42,\"body\":\"Welchen Browser nutzen Sie?\",\"internal\":true}] [mock:action zammad/set_state {\"ticket_id\":42,\"state\":\"pending reminder\"}] [mock:block key=zammad:ticket:42 question=Warte auf Kundenantwort] [mock:result Ticket gelöst] [mock:memory Ticket 42: Login-Problem, Browser erfragt]","internal":false}}`)
	postWebhook(t, s, "support", newTicket)

	// The agent wakes up, works and parks the task.
	var taskID string
	waitFor(t, "task blocked", 15*time.Second, func() bool {
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
		t.Fatalf("correlation key missing: %+v", task.CorrelationKey)
	}
	if task.RuntimeSessionID == nil || *task.RuntimeSessionID == "" {
		t.Fatalf("runtime_session_id (for --resume) missing")
	}
	if zammad.replyCount() != 1 {
		t.Fatalf("expected exactly one (internal) follow-up question, got %d", zammad.replyCount())
	}
	if upd := zammad.lastUpdate(); upd == nil || upd["state"] != "pending reminder" {
		t.Fatalf("the ticket must be on pending reminder: %+v", upd)
	}
	waitFor(t, "agent sleeps after blocked", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})

	// Idempotence: the same webhook (a Zammad retry) must not trigger anything new.
	resp := postWebhookRaw(t, s, "support", newTicket)
	if resp != `{"outcome":"duplicate"}` {
		t.Fatalf("a retry must be deduplicated, got %s", resp)
	}

	// Customer reply → wake-on-correlation → resume → done.
	answer := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login funktioniert nicht","state":"open"},
		"article":{"id":2,"sender":"Customer","body":"Ich nutze Chrome 126.","internal":false}}`)
	postWebhook(t, s, "support", answer)

	waitFor(t, "task done after correlation", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done := mustTask(t, s, taskID)
	if done.Result == nil || *done.Result != "Ticket gelöst" {
		t.Fatalf("result missing: %+v", done.Result)
	}

	// Wiki ingest on the done step (spec/05): the insight lands as its own page
	// or is appended to an existing one — hence Contains, not ==.
	entries, err := s.mem.Query(ctx, agent.ID, "Login-Problem Ticket", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Content, "Ticket 42: Login-Problem, Browser erfragt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wiki page missing, got %+v", entries)
	}

	// A gapless recording: lifecycle + runtime + action + credential.
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
			t.Fatalf("recording without %s events: %+v", want, kinds)
		}
	}

	// Costs are visible (from the runtime's cost events).
	cost, err := s.obs.CostByAgent(ctx, agent.ID)
	if err != nil || cost.TotalUSD <= 0 {
		t.Fatalf("costs must be booked: %+v err=%v", cost, err)
	}

	waitFor(t, "agent sleeps at the end", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})
}

// TestApprovalGate: an external reply requires approval → the task parks on
// approval:<id>; the human clearance wakes it, the action runs through.
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

	waitFor(t, "task blocked on approval", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	if zammad.replyCount() != 0 {
		t.Fatal("nothing may go out before the clearance (fail-closed)")
	}

	approvals, err := s.obs.ListApprovals(ctx, s.orgID, "pending")
	if err != nil || len(approvals) != 1 {
		t.Fatalf("expected exactly one pending approval: %v %d", err, len(approvals))
	}
	if approvals[0].Action != "zammad:reply_external" {
		t.Fatalf("wrong approval subject: %s", approvals[0].Action)
	}

	// A human clears it → wake-on-correlation → the action runs, the task is done.
	appr, err := s.obs.DecideApproval(ctx, s.orgID, approvals[0].ID, true, &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	s.orch.OnApprovalDecided(ctx, appr)

	waitFor(t, "task done after clearance", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if zammad.replyCount() != 1 {
		t.Fatalf("after the clearance exactly one external reply, got %d", zammad.replyCount())
	}
}

// TestGuardrailDeny: deny_action blocks hard, the agent carries on without the
// action, the trigger is in the recording.
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

	waitFor(t, "task done despite the deny", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if zammad.replyCount() != 0 {
		t.Fatal("the forbidden escalation must never reach Zammad")
	}
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	found := false
	for _, e := range events {
		if e.Kind == "guardrail" {
			found = true
		}
	}
	if !found {
		t.Fatal("a guardrail trigger must be in the recording")
	}
}

// TestAgentSetsStage checks the agent path for custom stages: the runtime calls
// covey/set_stage; the control plane creates the stage automatically and moves
// the task there — purely for display, the lifecycle state stays done.
func TestAgentSetsStage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("stage-agent")
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Stage-Test",
		`[mock:action covey/set_stage {"stage":"Recherche"}]
[mock:result fertig]`, "manual", 3)

	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil || len(stages) != 1 || stages[0].Name != "Recherche" {
		t.Fatalf("the agent must have created the stage 'Recherche', got %+v (err=%v)", stages, err)
	}
	got, _ := s.backlog.Get(ctx, task.ID)
	if got.StageID == nil || *got.StageID != stages[0].ID {
		t.Fatalf("the task must sit in stage 'Recherche', got stage_id=%v", got.StageID)
	}
	if got.State != backlog.StateDone {
		t.Fatalf("set_stage must not change the lifecycle state, got %q", got.State)
	}
}

// TestAgentNotesAndStageCleanup checks proactive notes and auto-cleanup:
// covey/add_note attaches an interim status to the task, covey/remember feeds
// generally valid knowledge into memory immediately, and a column "invented" by
// the agent disappears automatically as soon as it empties — the one emptied by
// moving on immediately, the last one when the task is archived.
func TestAgentNotesAndStageCleanup(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("notiz-agent")
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Notiz-Test",
		`[mock:action covey/add_note {"content":"Logs zeigen einen Timeout im Payment-Service"}]
[mock:action covey/remember {"content":"Kunde ACME erreicht man nur telefonisch"}]
[mock:action covey/set_stage {"stage":"Recherche"}]
[mock:action covey/set_stage {"stage":"Warten auf Kunde"}]
[mock:result fertig]`, "manual", 3)

	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// A task-related interim status hangs off the task as a note.
	notes, err := s.backlog.ListNotes(ctx, task.ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("expected exactly one note on the task, got %+v (err=%v)", notes, err)
	}
	if notes[0].Author != "agent" || !strings.Contains(notes[0].Content, "Timeout") {
		t.Fatalf("unexpected note: %+v", notes[0])
	}

	// Generally valid knowledge lands in memory immediately — not only on completion.
	mems, _ := s.mem.List(ctx, agent.ID, 10)
	foundMem := false
	for _, m := range mems {
		if strings.Contains(m.Content, "ACME") {
			foundMem = true
		}
	}
	if !foundMem {
		t.Fatalf("remember must land in memory immediately, got %+v", mems)
	}

	// "Recherche" emptied when moving on → cleared away automatically;
	// "Warten auf Kunde" holds the task and stays.
	stages, _ := s.backlog.ListStages(ctx, agent.ID)
	if len(stages) != 1 || stages[0].Name != "Warten auf Kunde" {
		t.Fatalf("an empty agent column must be cleared away, got %+v", stages)
	}
	if stages[0].CreatedBy != "agent" {
		t.Fatalf("a column invented by the agent must carry created_by=agent, got %q", stages[0].CreatedBy)
	}

	// Columns created by humans survive the cleanup — even when empty.
	if _, err := s.backlog.CreateStage(ctx, agent.ID, "Manuell", ""); err != nil {
		t.Fatal(err)
	}

	// Archiving empties the last agent column → it disappears with it.
	if _, err := s.backlog.ArchiveTerminal(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	stages, _ = s.backlog.ListStages(ctx, agent.ID)
	if len(stages) != 1 || stages[0].Name != "Manuell" {
		t.Fatalf("after archiving only the human column may remain, got %+v", stages)
	}
}

// TestAgentWiki checks the wiki tools (spec/05) end-to-end: the agent creates a
// page via covey/wiki_write (daemon round trip → brokerWiki → store), which is
// then readable by slug and findable by vector search. (The [[wikilink]]
// extraction is checked by a store unit test — the mock directive parser cannot
// carry a ']' inside action params.)
func TestAgentWiki(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("wiki-agent")

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Wiki-Test",
		`[mock:action covey/wiki_write {"slug":"kunde-acme","title":"Kunde ACME","body":"Erreichbar nur telefonisch, reagiert nicht auf E-Mail."}]
[mock:result fertig]`, "manual", 3)
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Readable by slug.
	page, err := s.mem.Read(ctx, agent.ID, "kunde-acme")
	if err != nil {
		t.Fatalf("the created wiki page is not readable: %v", err)
	}
	if page.Title != "Kunde ACME" || !strings.Contains(page.Content, "telefonisch") {
		t.Fatalf("unexpected page: %+v", page)
	}

	// Findable by vector search.
	hits, err := s.mem.Search(ctx, agent.ID, "ACME telefonisch erreichbar", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.Slug == "kunde-acme" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the page must be findable by search, got %+v", hits)
	}

	// Home working copy (spec/05): a second task materializes the page created
	// earlier as ~/wiki/<slug>.md into the persistent home at the start.
	task2, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Zweite Aufgabe",
		"[mock:result ok]", "manual", 3)
	waitFor(t, "second task done", 15*time.Second, func() bool {
		return s.taskState(task2.ID) == backlog.StateDone
	})
	wikiFile := filepath.Join(s.homeBase, agent.ID.String(), "wiki", "kunde-acme.md")
	waitFor(t, "wiki file materialized", 5*time.Second, func() bool {
		_, err := os.Stat(wikiFile)
		return err == nil
	})
	raw, err := os.ReadFile(wikiFile)
	if err != nil || !strings.Contains(string(raw), "telefonisch") {
		t.Fatalf("unexpected materialized wiki file: %q (err=%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(s.homeBase, agent.ID.String(), "wiki", "index.md")); err != nil {
		t.Fatalf("index.md must be materialized: %v", err)
	}
}

// TestBacklogCleanupAndRetry checks the cleanup logic: terminal tasks can be
// archived (hidden, not deleted), failed tasks can be scheduled again via retry
// — and the agent really does pick them up again.
func TestBacklogCleanupAndRetry(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("cleanup-agent")

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert",
		"[mock:fail kaputt]", "manual", 3)
	waitFor(t, "task failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})

	// Open tasks are not archivable (fail-closed for anything active).
	open, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Bleibt offen",
		"[mock:sleep 30s]", "manual", 9)
	if _, err := s.backlog.Archive(ctx, open.ID); err == nil {
		t.Fatalf("an open task must not be archivable")
	}

	// The cleanup archives exactly the terminal task.
	n, err := s.backlog.ArchiveTerminal(ctx, agent.ID)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: n=%d err=%v, want 1", n, err)
	}
	active, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
	for _, a := range active {
		if a.ID == task.ID {
			t.Fatalf("an archived task must not show up in the active backlog")
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
		t.Fatalf("an archived task must appear in the full backlog with archived_at")
	}

	// Retry brings the failed (and archived) task back to open …
	re, err := s.backlog.Retry(ctx, task.ID, "test-retry")
	if err != nil || re.State != backlog.StateOpen || re.Error != nil || re.ArchivedAt != nil {
		t.Fatalf("retry: %+v err=%v, want state=open without error/archived_at", re, err)
	}
	// … and the agent works it off again (failing deterministically once more).
	waitFor(t, "retry processed again", 15*time.Second, func() bool {
		got, _ := s.backlog.Get(ctx, task.ID)
		return got.State == backlog.StateFailed && got.UpdatedAt.After(re.UpdatedAt)
	})
}

// TestStageFollowsState checks the auto-follow of the default columns: as long
// as a task sits in Backlog/In Arbeit/Erledigt (or in no column), the column
// follows the lifecycle state; a deliberately chosen own column wins.
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

	// A successful task: Backlog → (In Arbeit) → Erledigt.
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Läuft durch", "[mock:result ok]", "manual", 3)
	if got := stageName(task.StageID); got != "Backlog" {
		t.Fatalf("a new task must start in 'Backlog', got %q", got)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done, _ := s.backlog.Get(ctx, task.ID)
	if got := stageName(done.StageID); got != "Erledigt" {
		t.Fatalf("a finished task must sit in 'Erledigt', got %q", got)
	}

	// An own column wins: manually placed tasks do not follow the state.
	fail, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert", "[mock:fail kaputt]", "manual", 3)
	waitFor(t, "task failed", 15*time.Second, func() bool {
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
		t.Fatalf("a manually placed task must not move on retry, got %q", name)
	}
}

// TestKillSwitch stops a working agent immediately; the task it had started
// goes back into the backlog.
func TestKillSwitch(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Lange Aufgabe",
		"[mock:sleep 30s]", "manual", 3)

	waitFor(t, "agent working", 15*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "working"
	})
	if err := s.orch.Kill(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task back in the backlog", 10*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateOpen
	})
	waitFor(t, "agent is killed", 10*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "killed"
	})

	// A killed agent is not woken again by the dispatcher.
	time.Sleep(time.Second)
	if got := s.agentStatus(agent.ID); got != "killed" {
		t.Fatalf("killed must stay killed, got %s", got)
	}
}

// TestBudgetGuardrail pauses the agent as soon as the costs break the cap.
func TestBudgetGuardrail(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")
	// The mock runtime reports 0.0123 USD per run; the cap is below that.
	if err := s.registry.SetBudget(ctx, agent.ID, 0.01); err != nil {
		t.Fatal(err)
	}

	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Teuer", "", "manual", 3)

	waitFor(t, "agent paused because of the budget", 15*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "killed"
	})
	waitFor(t, "task open again", 10*time.Second, func() bool {
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
		t.Fatal("a budget trigger must be in the recording")
	}
}

// TestFleetKill: the fleet-wide emergency stop prevents every dispatch.
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
		t.Fatalf("with a fleet kill nothing may start up, got %s", got)
	}

	// Release the emergency stop → the work starts.
	s.registry.SetFleetKilled(ctx, s.orgID, false)
	waitFor(t, "work starts after resume", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		return len(tasks) == 1 && tasks[0].State == backlog.StateDone
	})
}

// --- Helpers ---

func postWebhook(t *testing.T, s *stack, slug string, body []byte) {
	t.Helper()
	if got := postWebhookRaw(t, s, slug, body); got == "" {
		t.Fatal("webhook without a response")
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
