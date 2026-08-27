package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/agents"
	"covey/internal/backlog"
)

/* Ein Agent, dem ein Paket fehlt, hatte keinen Weg, das zu sagen: nirgends
   root, kein apt, und der Arbeitsplatz steht fest, bis jemand ein Image neu
   baut. Was er stattdessen tat, lag in seinem Home — ~/aptroot mit
   sources.list, aufgelösten Paket-URIs und entpackten .debs, zuletzt geändert
   am Tag der Beobachtung (#106).

   Geprüft wird der ganze Weg: die Aktion in der Sandbox, die Meldung über das
   Protokoll, der offene Punkt im Posteingang. */

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
		// Der Beleg gehört dazu: ohne den Befehl, an dem es gefehlt hat, ist
		// die Bitte nicht zu entscheiden.
		if !strings.Contains(p.Rationale, "Class Redis not found") {
			t.Fatalf("der Beleg fehlt: %q", p.Rationale)
		}
		// Und der Arbeitsplatz, denn die Antwort ist eine Zeile in SEINEM
		// Dockerfile — ein Werkzeug im falschen Profil wiegt für alle anderen mit.
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

	// Und der Lauf ist trotzdem durchgelaufen: Die Plattform beschafft nichts,
	// sie meldet — der Agent arbeitet mit dem, was da ist.
	got, _ := s.backlog.Get(ctx, task.ID)
	if got.Result == nil || !strings.Contains(*got.Result, "gemeldet") {
		t.Fatalf("der Lauf endete nicht normal: %+v", got.Result)
	}
}

// Ohne Werkzeugnamen ist es keine Bitte, sondern ein Missverständnis.
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
