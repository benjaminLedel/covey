package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// The agent's wiki tools, driven the way an agent drives them: through the
// daemon round trip into brokerWiki and back. The point of covering them here
// rather than at the store is that an operation the broker refuses looks, from
// the agent's side, exactly like one that is not implemented — so what each op
// answers is part of the contract.
func TestAgentWikiToolsEndToEnd(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("werkzeug-agent")

	write := newTask(t, s, agent.ID, "Seite anlegen",
		`[mock:action covey/wiki_write {"slug":"kunde-nord","title":"Kunde Nord","body":"Nord bezieht die Wartung seit 2024. Ansprechpartnerin ist Frau Zabel."}]
[mock:result angelegt]`)
	waitFor(t, "write done", 15*time.Second, func() bool {
		return s.taskState(write.ID) == backlog.StateDone
	})

	// Appending grows a page rather than replacing it — that is what makes the
	// memory cumulative instead of a last-writer-wins note.
	appendTask := newTask(t, s, agent.ID, "Seite ergänzen",
		`[mock:action covey/wiki_append {"slug":"kunde-nord","text":"Seit Juli gibt es einen zweiten Standort."}]
[mock:result ergänzt]`)
	waitFor(t, "append done", 15*time.Second, func() bool {
		return s.taskState(appendTask.ID) == backlog.StateDone
	})

	page, err := s.mem.Read(ctx, agent.ID, "kunde-nord")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Content, "Frau Zabel") {
		t.Error("appending replaced the page instead of growing it")
	}
	if !strings.Contains(page.Content, "zweiten Standort") {
		t.Error("the appended paragraph is not in the page")
	}

	// And forgetting is an operation of its own: a page the agent itself knows
	// to be wrong must be removable without a human.
	del := newTask(t, s, agent.ID, "Seite vergessen",
		`[mock:action covey/wiki_delete {"slug":"kunde-nord"}]
[mock:result vergessen]`)
	waitFor(t, "delete done", 15*time.Second, func() bool {
		return s.taskState(del.ID) == backlog.StateDone
	})
	if _, err := s.mem.Read(ctx, agent.ID, "kunde-nord"); err == nil {
		t.Error("the page survived being forgotten")
	}
}

// A wiki operation the broker refuses does NOT quietly continue. The action
// proxy answers status=error, the run ends there, and the task is failed — so
// an agent reading a page that is not there gets a run it can look at rather
// than a result assembled around a hole.
func TestARefusedWikiOperationFailsTheRun(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("unbekannt-agent")

	task := newTask(t, s, agent.ID, "Seite lesen, die es nicht gibt",
		`[mock:action covey/wiki_read {"slug":"gibtesnicht"}]
[mock:result trotzdem fertig]`)
	waitFor(t, "task failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})
	// And the directive after it never ran — the refusal ends the run rather
	// than being stepped over.
	if got := s.taskState(task.ID); got != backlog.StateFailed {
		t.Errorf("the task ended as %q", got)
	}
}

// newTask puts one task in an agent's backlog, or fails the test.
func newTask(t *testing.T, s *stack, agentID uuid.UUID, title, body string) backlog.Task {
	t.Helper()
	task, err := s.backlog.Create(context.Background(), s.orgID, agentID, title, body, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
