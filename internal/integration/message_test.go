package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/guardrails"
)

// TestAgentMessagesSupervisor checks the meta action covey/message (#537):
// an agent that is unsure tells its supervisor, into their direct
// conversation — and the run goes on, instead of parking or burying the
// question in the result.
func TestAgentMessagesSupervisor(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("fragender")
	if err := s.registry.SetSupervisor(ctx, agent.ID, &s.adminID); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Unklarer Branch",
		`[mock:action covey/message {"text":"Welcher Branch: A oder B? Ich nehme bis dahin keinen."}]
[mock:result weitergearbeitet]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	store := chat.New(s.pool)
	convID, ok, err := store.FindDirect(ctx, s.orgID, chat.Agent(agent.ID), chat.Human(s.adminID))
	if err != nil || !ok {
		t.Fatalf("the direct conversation agent↔supervisor must exist: ok=%v err=%v", ok, err)
	}
	msgs, err := store.Page(ctx, convID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found *chat.Message
	for i := range msgs {
		if msgs[i].Text == "Welcher Branch: A oder B? Ich nehme bis dahin keinen." {
			found = &msgs[i]
		}
	}
	if found == nil {
		t.Fatalf("the message must stand in the conversation, got %d messages", len(msgs))
	}
	if found.AuthorKind != chat.MemberAgent || found.AuthorID == nil || *found.AuthorID != agent.ID {
		t.Fatalf("the message must be the agent's, is %s/%v", found.AuthorKind, found.AuthorID)
	}
	if found.TaskID == nil || *found.TaskID != task.ID {
		t.Fatalf("the message must carry the running task, has %v", found.TaskID)
	}
}

// TestAgentMessagesNamedPersonOrNobody: a person is named by display name or
// e-mail and has to be unambiguous; an unknown name is refused rather than
// guessed, and a guard rail can forbid the action altogether.
func TestAgentMessagesNamedPersonOrNobody(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("namensnenner")
	store := chat.New(s.pool)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "An eine Person",
		`[mock:action covey/message {"to":"admin@test.local","text":"Per Adresse."}]
[mock:action covey/message {"to":"Admin","text":"Per Name."}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	// An unknown name is an error of the action — the mock runtime treats
	// that as a hard failure, as a real runtime would see an error result.
	// What matters: nothing lands anywhere, and the error says what to do.
	nobody, err := s.backlog.Create(ctx, s.orgID, agent.ID, "An niemanden",
		`[mock:action covey/message {"to":"Niemand Bekanntes","text":"Ins Leere."}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(nobody.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if e := taskError(t, s, nobody.ID); !strings.Contains(e, "no person") {
		t.Fatalf("the refusal must name the problem, error is %q", e)
	}
	convID, ok, err := store.FindDirect(ctx, s.orgID, chat.Agent(agent.ID), chat.Human(s.adminID))
	if err != nil || !ok {
		t.Fatalf("the conversation with the named person must exist: ok=%v err=%v", ok, err)
	}
	msgs, err := store.Page(ctx, convID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]bool{}
	for _, m := range msgs {
		texts[m.Text] = true
	}
	if !texts["Per Adresse."] || !texts["Per Name."] {
		t.Fatalf("both the e-mail and the display name must reach the person, got %v", texts)
	}
	if texts["Ins Leere."] {
		t.Fatalf("a message to an unknown person must not land anywhere")
	}

	// Forbidden by a guard rail: nothing is written.
	gagged := s.newSupportAgent("stumm")
	if err := s.registry.SetSupervisor(ctx, gagged.ID, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rails.Create(ctx, railRule(s.orgID, guardrails.RuleDenyAction, "covey:message")); err != nil {
		t.Fatal(err)
	}
	task2, err := s.backlog.Create(ctx, s.orgID, gagged.ID, "Darf nicht schreiben",
		`[mock:action covey/message {"text":"Hallo?"}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(task2.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if _, ok, _ := store.FindDirect(ctx, s.orgID, chat.Agent(gagged.ID), chat.Human(s.adminID)); ok {
		t.Fatalf("a forbidden message must not open a conversation")
	}
}
