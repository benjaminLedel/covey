package integration

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/push"
)

// TestDecisionsReachWhoMayDecide (#622): an approval, an open point of a
// review and a workflow's approval step are pushed to the people who may
// decide them — not to whoever else has a device —, once, with their own
// words and the question's sound. An open point that touches access goes to
// the admins and security, not to the owner whose role may not accept it;
// a proposal drafted in a conversation is not announced a second time to
// that conversation's members.
func TestDecisionsReachWhoMayDecide(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("push-entscheidung")
	lead := s.mitglied(t, "lead@test.local", "Lead", "agent_owner", "lead-passwort")
	sec := s.mitglied(t, "sec@test.local", "Sec", "security", "sec-passwort")
	decider := s.mitglied(t, "decider@test.local", "Decider", "auditor", "decider-passwort")
	s.mitglied(t, "bystander@test.local", "Bystander", "agent_owner", "bystander-passwort")
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, agent.ID, lead); err != nil {
		t.Fatal(err)
	}
	var bystander uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM humans WHERE email='bystander@test.local'`).Scan(&bystander); err != nil {
		t.Fatal(err)
	}
	for tok, h := range map[string]uuid.UUID{"tok-admin": s.adminID, "tok-lead": lead, "tok-sec": sec, "tok-decider": decider, "tok-bystander": bystander} {
		if err := push.Register(ctx, s.pool, h, tok, "ios", "production", "en", "bot"); err != nil {
			t.Fatal(err)
		}
	}
	sender := &fakeSender{gone: map[string]bool{}}
	n := &push.Notifier{Pool: s.pool, Sender: sender, Lag: time.Millisecond}
	round := func() map[string]push.Message {
		t.Helper()
		time.Sleep(20 * time.Millisecond)
		if _, err := n.Round(ctx); err != nil {
			t.Fatal(err)
		}
		out := map[string]push.Message{}
		for _, m := range sender.take() {
			if !m.Valid() {
				t.Fatalf("a message no sender accepts: %+v", m)
			}
			out[m.Token] = m
		}
		return out
	}
	tokens := func(got map[string]push.Message) []string {
		var out []string
		for k := range got {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	round()

	// An approval: the admins and the owner, whose role may decide.
	if _, err := s.pool.Exec(ctx, `INSERT INTO approvals (id, org_id, agent_id, action, params)
		VALUES ($1, $2, $3, 'zammad:reply', '{}')`, uuid.New(), s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	got := round()
	if k := tokens(got); len(k) != 2 || k[0] != "tok-admin" || k[1] != "tok-lead" {
		t.Fatalf("an approval goes to the admin and the owner: %v", k)
	}
	if m := got["tok-lead"]; m.Title != "Support-Agent waits for an approval" || m.Sound != "covey-bot-question.caf" ||
		m.AgentID != agent.ID.String() || m.Body != "" {
		t.Fatalf("approval: %+v", m)
	}
	if again := round(); len(again) != 0 {
		t.Fatalf("an approval is announced once: %v", tokens(again))
	}

	// An open point that touches access: the admins and security.
	if _, err := s.pool.Exec(ctx, `INSERT INTO improvement_items (id, org_id, agent_id, kind, title, files)
		VALUES ($1, $2, $3, 'proposal', 'Mehr Zugang', '{"ACCESS.md": "- system: zammad scope: admin"}')`,
		uuid.New(), s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	got = round()
	if k := tokens(got); len(k) != 2 || k[0] != "tok-admin" || k[1] != "tok-sec" {
		t.Fatalf("access is the admins' and security's: %v", k)
	}
	if got["tok-sec"].Title != "Support-Agent has an open point from a review" {
		t.Fatalf("open point: %+v", got["tok-sec"])
	}

	// One that does not: the admins and the owner.
	if _, err := s.pool.Exec(ctx, `INSERT INTO improvement_items (id, org_id, agent_id, kind, title, files)
		VALUES ($1, $2, $3, 'finding', 'Zu oft', '{}')`, uuid.New(), s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	if k := tokens(round()); len(k) != 2 || k[0] != "tok-admin" || k[1] != "tok-lead" {
		t.Fatalf("an open point goes to the admin and the owner: %v", k)
	}

	// A proposal drafted from the admin's message: the admin hears the
	// conversation's line, the owner the proposal.
	store := chat.New(s.pool)
	conv := direkt(t, s, s.adminID, agent.ID)
	m, err := sagt(ctx, store, conv, chat.Agent(agent.ID), "Ich schlage vor: nur werktags.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO improvement_items (id, org_id, agent_id, kind, title, files, origin_message_id)
		VALUES ($1, $2, $3, 'proposal', 'Nur werktags', '{"HEARTBEAT.md": "x"}', $4)`,
		uuid.New(), s.orgID, agent.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	got = round()
	if k := tokens(got); len(k) != 2 || k[0] != "tok-admin" || k[1] != "tok-lead" {
		t.Fatalf("proposal from the chat: %v", k)
	}
	if got["tok-admin"].Title != "Support-Agent replied" || got["tok-lead"].Title != "Support-Agent proposes a configuration change" {
		t.Fatalf("the conversation's line to its member, the proposal to the owner: %q, %q",
			got["tok-admin"].Title, got["tok-lead"].Title)
	}

	// A workflow's approval step: the person it names, with the question
	// when the organisation shows a preview.
	if _, err := s.pool.Exec(ctx, `UPDATE organizations SET push_preview=true WHERE id=$1`, s.orgID); err != nil {
		t.Fatal(err)
	}
	wf := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO workflows (id, org_id, slug, version) VALUES ($1, $2, 'release', 1)`, wf, s.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, source) VALUES ($1, 1, $2)`, wf, decisionWorkflow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO workflow_runs (id, workflow_id, case_ref, seq, step, state, since, vars)
		VALUES ($1, $2, 'v1.2', 1, 'h', 'waiting', now(), '{"b": {"mr": "!9"}}')`, uuid.New(), wf); err != nil {
		t.Fatal(err)
	}
	got = round()
	if k := tokens(got); len(k) != 1 || k[0] != "tok-decider" {
		t.Fatalf("a workflow step goes to the person it names: %v", k)
	}
	if m := got["tok-decider"]; m.Title != "Release · Decision" || m.Body != "Ship !9?" || m.AgentID != "" || m.ConversationID != "" {
		t.Fatalf("workflow step: %+v", m)
	}
	if again := round(); len(again) != 0 {
		t.Fatalf("a step is announced once: %v", tokens(again))
	}
}
