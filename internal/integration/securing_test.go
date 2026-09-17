package integration

import (
	"context"
	"testing"
	"time"

	"covey/internal/backlog"
)

// Between "the last task is done" and "the sandbox is gone" something still
// happens: the container goes down and the home is written into the store.
// For a small home that is a second, for a grown one half a minute — and the
// agent stood on `working` the whole time, while nothing in its backlog was
// in progress anymore.
//
// The status is what the interface, the org chart and the human read as "is it
// busy right now". Answering it with `working` for the platform's housekeeping
// hides exactly the moment in which someone wanted to know what was going
// on.
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

	// The status change stands in the recording — that is where the human who
	// wants to know why his agent still looks busy reads it too.
	waitFor(t, "der Sicherungs-Status fehlt in der Aufzeichnung", 20*time.Second, func() bool {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='securing'`,
			agent.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	})

	// And it does not stay stuck in there: after that the agent sleeps.
	waitFor(t, "der Agent kommt nicht zur Ruhe", 20*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})
}
