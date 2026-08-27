package integration

import (
	"context"
	"testing"
	"time"

	"covey/internal/backlog"
)

// Zwischen „die letzte Aufgabe ist fertig" und „die Sandbox ist weg" passiert
// noch etwas: der Container geht runter und das Home wird in den Store
// geschrieben. Bei einem kleinen Home ist das eine Sekunde, bei einem
// gewachsenen eine halbe Minute — und der Agent stand die ganze Zeit auf
// `working`, während in seinem Backlog nichts mehr in Arbeit war.
//
// Der Status ist das, was Oberfläche, Org-Chart und Mensch als „ist der gerade
// beschäftigt" lesen. Ihn für die Hausarbeit der Plattform mit „arbeitet" zu
// beantworten, verdeckt genau den Moment, in dem jemand wissen wollte, was los
// ist.
func TestDerAgentSagtWennDiePlattformSeinHomeSichert(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)

	agent := s.newSupportAgent("securing-agent")
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas erledigen",
		"[mock:result erledigt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Der Statuswechsel steht in der Aufzeichnung — dort liest ihn auch der
	// Mensch, der wissen will, warum sein Agent noch beschäftigt aussieht.
	waitFor(t, "der Sicherungs-Status fehlt in der Aufzeichnung", 20*time.Second, func() bool {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='securing'`,
			agent.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	})

	// Und er bleibt nicht darin hängen: danach schläft der Agent.
	waitFor(t, "der Agent kommt nicht zur Ruhe", 20*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})
}
