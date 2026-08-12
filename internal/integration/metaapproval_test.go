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

// TestMetaActionWartetAufFreigabe: eine require_approval-Regel auf einer
// Meta-Action hält den Agenten an, statt ihn abzuweisen.
//
// Vorher lehnte dieser Zweig hart ab — „requires an approval and cannot be
// performed unattended". Das ist eine Leitplanke, die für eine Klasse von
// Aktionen still zu einem Verbot wird: wer die Regel setzt, meint „jemand
// schaut drauf" und bekommt „geht nicht" (spec/21). Der Test hält den ganzen
// Weg fest: blockieren, im Posteingang erscheinen, freigeben, wiederholen.
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

	// 1. Die Aufgabe wartet — sie ist nicht fehlgeschlagen.
	waitFor(t, "the task waits for the approval", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "neuer-kollege"); err == nil {
		t.Fatal("solange niemand entschieden hat, darf der Kollege nicht existieren")
	}

	// 2. Im Posteingang steht, WAS entschieden werden soll — mit den
	//    Parametern, sonst entscheidet ein Mensch über eine Zeichenkette.
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

	// 3. Freigeben weckt die Aufgabe, der Agent wiederholt die Aktion.
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

	// 4. Und die Freigabe ist verbraucht: eine Antwort auf eine Handlung, keine
	//    Lizenz auf die Aktion.
	var used bool
	if err := s.pool.QueryRow(ctx, "SELECT used FROM approvals WHERE id=$1",
		approvals[0]["id"].(string)).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("die erteilte Freigabe muss beim Wiederholen verbraucht worden sein")
	}
}

// TestMetaActionAbgelehnteFreigabe: auch die Ablehnung erreicht den Agenten,
// und die Aktion hat nicht stattgefunden.
//
// Was der Agent DANACH tut, ist seine Sache — der Prompt sagt ihm, er soll die
// Aktion nicht wiederholen. Die Mock-Runtime kann das nicht wissen und
// wiederholt stumpf; geprüft wird deshalb, was die Plattform verantwortet: die
// Entscheidung kommt an, und ohne sie ist nichts passiert.
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

	// Die Entscheidung weckt die Aufgabe — sie geht ueber den
	// Korrelationsschluessel zurueck in den Backlog, mit der Ablehnung als
	// Wiederaufnahme-Text.
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

// TestMetaActionOhneRegelUnveraendert: ohne require_approval bleibt alles, wie
// es war — die Leitplanke bezahlt sich nicht mit einem Dialog pro Entwurf.
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
