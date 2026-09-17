package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestMetaActionWartetAufFreigabe: a require_approval rule on a meta-action
// holds the agent waiting, instead of rejecting it.
//
// Before, this branch refused hard — `requires an approval and cannot be
// performed unattended`. That is a guard rail that quietly turns into a ban
// for a class of actions: whoever sets the rule means "someone looks at
// this" and gets "not possible" (spec/21). The test holds the whole
// path: block, show up in the inbox, approve, repeat.
func TestMetaActionWartetAufFreigabe(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

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
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleRequireApproval,
		Pattern: "covey:create_agent", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, "Kollegen entwerfen",
		`[mock:action covey/create_agent {"display_name":"Neuer Kollege","slug":"neuer-kollege","runtime":"mock"}]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The task waits — it did not fail.
	waitFor(t, "the task waits for the approval", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "neuer-kollege"); err == nil {
		t.Fatal("solange niemand entschieden hat, darf der Kollege nicht existieren")
	}

	// 2. The inbox says WHAT is to be decided — with the
	//    parameters, otherwise a human decides over a string.
	page := getInbox(t, admin, "?status=open&type=approval")
	if page.Total != 1 || page.Items[0].Title != "covey:create_agent" {
		t.Fatalf("die Meta-Action gehoert in den Posteingang: %+v", page)
	}
	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 {
		t.Fatalf("genau eine Freigabe erwartet: %v", approvals)
	}
	params, _ := approvals[0]["params"].(map[string]any)
	if params["op"] != "create_agent" || params["slug"] != "neuer-kollege" {
		t.Fatalf("die Freigabe muss tragen, worueber entschieden wird: %v", params)
	}

	// 3. Approving wakes the task, the agent repeats the action.
	admin.expect(http.MethodPost, "/api/v1/approvals/"+approvals[0]["id"].(string)+"/decide",
		map[string]any{"approve": true}, http.StatusOK)
	waitFor(t, "the task finishes after the approval", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if got, err := s.registry.GetBySlug(ctx, s.orgID, "neuer-kollege"); err != nil {
		t.Fatalf("nach der Freigabe muss der Entwurf entstanden sein: %v", err)
	} else if !got.Draft() {
		t.Fatal("was ein Agent anlegt, bleibt ein Entwurf — die Freigabe stellt niemanden ein")
	}

	// 4. And the approval is spent: an answer to one act, not a
	//    licence for the action.
	var used bool
	if err := s.pool.QueryRow(ctx, "SELECT used FROM approvals WHERE id=$1",
		approvals[0]["id"].(string)).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("die erteilte Freigabe muss beim Wiederholen verbraucht worden sein")
	}
}

// TestMetaActionAbgelehnteFreigabe: the rejection also reaches the agent,
// and the action did not take place.
//
// What the agent does AFTERWARDS is its own affair — the prompt tells it not
// to repeat the action. The mock runtime cannot know that and repeats
// blindly; what is checked is therefore what the platform is answerable for:
// the decision arrives, and without it nothing happened.
func TestMetaActionAbgelehnteFreigabe(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	drafter, err := s.registry.Create(ctx, s.orgID, "personal", "Personal", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleRequireApproval,
		Pattern: "covey:*", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, "Entwerfen",
		`[mock:action covey/create_agent {"display_name":"Nie entstanden","slug":"nie-entstanden","runtime":"mock"}]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task waits", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})

	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 {
		t.Fatalf("genau eine Freigabe erwartet: %v", approvals)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+approvals[0]["id"].(string)+"/decide",
		map[string]any{"approve": false}, http.StatusOK)

	// The decision wakes the task — it goes back into the
	// backlog via the correlation key, with the denial as the
	// resumption text.
	waitFor(t, "the denial wakes the task", 30*time.Second, func() bool {
		trs, err := s.backlog.Transitions(ctx, task.ID)
		if err != nil {
			return false
		}
		for _, tr := range trs {
			if strings.Contains(tr.Note, "correlated event: approval:") {
				return true
			}
		}
		return false
	})
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "nie-entstanden"); err == nil {
		t.Fatal("eine abgelehnte Freigabe darf die Aktion nicht doch ausfuehren")
	}
}

// TestMetaActionOhneRegelUnveraendert: without require_approval everything stays as
// it was — the guard rail does not pay for itself with a dialog per draft.
func TestMetaActionOhneRegelUnveraendert(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	drafter, err := s.registry.Create(ctx, s.orgID, "personal", "Personal", "mock", &s.adminID)
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
		`[mock:action covey/create_agent {"display_name":"Ohne Dialog","slug":"ohne-dialog","runtime":"mock"}]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task runs through", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "ohne-dialog"); err != nil {
		t.Fatalf("ohne Regel entsteht der Entwurf sofort: %v", err)
	}
	if page := getInbox(t, admin, "?status=open"); page.Total != 0 {
		t.Fatalf("ohne Regel darf nichts im Posteingang landen: %+v", page)
	}
}
