package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/agents"
	"covey/internal/backlog"
)

/* An agent that is missing a package had no way to say so: nowhere root, no
   apt, and the workplace stands fixed until somebody rebuilds an image.
   What it did instead lay in its home — ~/aptroot with sources.list,
   resolved package URIs and unpacked .debs, last changed on the day of the
   observation (#106).

   Checked is the whole path: the action in the sandbox, the report over the
   protocol, the open item in the inbox. */

func TestEineWerkzeugBitteLandetImPosteingang(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("braucht-etwas")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas bauen",
		`[mock:action covey/request_tool {"tool":"php8.2-redis","why":"composer test bricht ab: Class Redis not found"}][mock:result gemeldet]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	punkte, err := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, p := range punkte {
		if p.Kind != agents.KindToolRequest {
			continue
		}
		gefunden = true
		if !strings.Contains(p.Title, "php8.2-redis") {
			t.Fatalf("der Titel nennt das Werkzeug nicht: %q", p.Title)
		}
		// The evidence belongs with it: without the command where the thing
		// was missing, the request cannot be decided.
		if !strings.Contains(p.Rationale, "Class Redis not found") {
			t.Fatalf("der Beleg fehlt: %q", p.Rationale)
		}
		// And the workplace, because the answer is a line in ITS Dockerfile
		// — a tool in the wrong profile weighs on all the others as well.
		if !strings.Contains(p.Rationale, "Arbeitsplatz") {
			t.Fatalf("der Arbeitsplatz fehlt im Beleg: %q", p.Rationale)
		}
		if p.Status != agents.ImprovementPending {
			t.Fatalf("die Bitte ist nicht offen, sondern %q", p.Status)
		}
		if p.TaskID == nil || *p.TaskID != task.ID {
			t.Fatalf("die Aufgabe, an der es fehlte, steht nicht daran: %+v", p.TaskID)
		}
	}
	if !gefunden {
		t.Fatalf("keine Werkzeug-Bitte angelegt (%d offene Punkte)", len(punkte))
	}

	// And the run went through all the same: the platform procures nothing,
	// it reports — the agent works with what is there.
	got, _ := s.backlog.Get(ctx, task.ID)
	if got.Result == nil || !strings.Contains(*got.Result, "gemeldet") {
		t.Fatalf("der Lauf endete nicht normal: %+v", got.Result)
	}
}

// Without a tool name this is not a request, but a misunderstanding.
func TestEineBitteOhneWerkzeugWirdAbgelehnt(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("sagt-nicht-was")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Unklar bitten",
		`[mock:action covey/request_tool {"why":"irgendwas"}][mock:result trotzdem fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task settled", 40*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	punkte, _ := s.registry.ListImprovements(ctx, s.orgID, agents.ImprovementFilter{})
	for _, p := range punkte {
		if p.Kind == agents.KindToolRequest {
			t.Fatalf("aus einer Bitte ohne Werkzeug wurde ein offener Punkt: %+v", p)
		}
	}
}
