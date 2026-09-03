package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// A tight profile: the generic text below lands far outside every band.
const styleToneMD = "## Voice\n\n1. Konkret, mit Zahl oder Namen in jedem Absatz.\n\n" +
	"```style-profile\n" +
	`{"schema":"covey-style/1","language":"de","documents":3,"words":7000,` +
	`"bands":{"nominalisation_rate":[0,4],"para_without_anchor_share":[0,25],"hedge_rate":[0,2],"stretch_verb_rate":[0,3]},` +
	`"corpus":{"para_len_mean":45}}` + "\n```\n"

const styleGenericBody = "Die Digitalisierung stellt für mittelständische Unternehmen eine zentrale Herausforderung dar, deren Bewältigung eine ganzheitliche Betrachtung der bestehenden Prozesse sowie eine nachhaltige Strategieentwicklung erfordert. Grundsätzlich ist die Umsetzung digitaler Transformationsprozesse ein vielschichtiges Unterfangen, das nicht nur technologische, sondern auch organisatorische und kulturelle Dimensionen umfasst.\n\n" +
	"In diesem Zusammenhang ist es entscheidend, dass die Implementierung entsprechender Maßnahmen unter Berücksichtigung der individuellen Rahmenbedingungen erfolgt. Die Optimierung der Wertschöpfungskette kann letztlich nur dann gelingen, wenn eine umfassende Analyse der Ausgangssituation vorgenommen wird."

// TestStyleGateReturnsFindingsThenEscalates: a style_gate rule in deny mode
// hands the findings back to the agent as the reason; when the agent keeps
// sending the same text, the gate stops denying after max_denials and puts the
// text in front of a human with the findings attached (spec/06).
func TestStyleGateReturnsFindingsThenEscalates(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("schreiber")

	cfg, err := s.registry.CurrentConfig(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	files := cfg.Files
	files["TONE.md"] = styleToneMD
	if _, err := s.registry.SaveConfig(ctx, agent.ID, files, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleStyleGate,
		Pattern: "covey:create_task", Enabled: true,
		Params: json.RawMessage(`{"mode":"deny","min_words":40,"max_denials":1}`),
	}); err != nil {
		t.Fatal(err)
	}

	params, _ := json.Marshal(map[string]any{"title": "Konzept Digitalisierung", "body": styleGenericBody})
	action := "[mock:action covey/create_task " + string(params) + "]"
	// The mock runtime does not read the reason; it tries the same action twice.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Konzept schreiben",
		action+"\n"+action+"\n[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The first attempt is denied with the findings; the second, over
	//    max_denials, waits for a human.
	waitFor(t, "the task waits for the approval", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	var denied, gated int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='guardrail' AND payload->>'rule'='style_gate' AND payload->>'decision'='denied'
		  AND payload ? 'findings' AND payload ? 'paragraphs'`, agent.ID).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='guardrail' AND payload->>'rule'='style_gate' AND payload->>'decision'='require_approval'`,
		agent.ID).Scan(&gated); err != nil {
		t.Fatal(err)
	}
	if denied != 1 || gated != 1 {
		t.Fatalf("expected one denial with findings and one escalation, got %d/%d", denied, gated)
	}

	// 2. The reviewer sees what the gate found, beside the action's own params.
	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 || approvals[0]["action"] != "covey:create_task" {
		t.Fatalf("exactly one approval on the action expected: %v", approvals)
	}
	p, _ := approvals[0]["params"].(map[string]any)
	findings, _ := p["style_findings"].(map[string]any)
	if p["title"] != "Konzept Digitalisierung" || findings == nil || findings["score"] == nil {
		t.Fatalf("the approval has to carry the params and the findings: %v", p)
	}
	if summary, _ := findings["summary"].(string); !strings.Contains(summary, "HIGH") {
		t.Fatalf("the summary names the severity: %v", findings["summary"])
	}

	// 3. Nothing was created while the gate held the text back.
	var created int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM backlog_tasks WHERE org_id=$1 AND title='Konzept Digitalisierung'`,
		s.orgID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("the subtask must not exist before a decision, found %d", created)
	}
}

// TestStyleGateWithoutProfileRecordsAndPasses: no profile in the config means
// the rule records that it did not apply and the action runs. The gate is a
// measurement, not a boundary.
func TestStyleGateWithoutProfileRecordsAndPasses(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("ohne-profil")
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleStyleGate,
		Pattern: "covey:*", Enabled: true, Params: json.RawMessage(`{"mode":"deny","min_words":40}`),
	}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"title": "Ohne Profil", "body": styleGenericBody})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Schreiben",
		"[mock:action covey/create_task "+string(params)+"]\n[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	var skipped, created int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='guardrail' AND payload->>'rule'='style_gate' AND payload->>'decision'='skipped'`,
		agent.ID).Scan(&skipped); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM backlog_tasks WHERE org_id=$1 AND title='Ohne Profil'`, s.orgID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || created != 1 {
		t.Fatalf("expected the gate to record 'skipped' once and the task to be created, got %d/%d", skipped, created)
	}
}
