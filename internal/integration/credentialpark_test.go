package integration

import (
	"context"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/secrets"
)

// poolValue reads one value of an org-wide pool back out.
func poolValue(t *testing.T, s *stack, key string, slot int) secrets.PoolValue {
	t.Helper()
	previews, err := s.secrets.Previews(context.Background(), s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kp := range previews {
		if kp.Key != key {
			continue
		}
		for _, v := range kp.Values {
			if v.Slot == slot {
				return v
			}
		}
	}
	t.Fatalf("value %s#%d not found", key, slot)
	return secrets.PoolValue{}
}

// TestRejectedCredentialGetsParked drives the hard signal through the whole
// chain: the run picks a credential, the runtime fails with a text the API
// would produce, and the value the run went out on is parked.
//
// End to end on purpose. The rule that reads the text is unit-tested
// (orchestrator.TestRejectionCooldown); what this covers is everything around
// it — that a waking phase remembers WHICH value it ran on, that the failure
// reaches that memory, and that the park lands on the right slot. Each of those
// is a place where the wiring can be right in isolation and wrong together.
func TestRejectedCredentialGetsParked(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a-lang-genug"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "claude_code_oauth_token", "sitz-b-lang-genug", "Abo B"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", agent.ID); err != nil {
		t.Fatal(err)
	}

	// Which seat the agent takes is the pool's decision — read it back rather
	// than assuming, so the test does not silently depend on the order.
	picked, err := s.secrets.Pick(ctx, s.orgID, agent.ID, "claude_code_oauth_token", nil)
	if err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert am Token",
		"[mock:fail Invalid bearer token]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})

	// The value the run went out on is parked, with the reason recorded.
	parked := poolValue(t, s, "claude_code_oauth_token", picked.Slot)
	if parked.CooldownUntil == nil || !parked.CooldownUntil.After(time.Now()) {
		t.Fatalf("the rejected value has to be parked: %+v", parked)
	}
	if parked.CooldownReason != secrets.ReasonError {
		t.Fatalf("cooldown reason %q, expected %q", parked.CooldownReason, secrets.ReasonError)
	}

	// And only that one. Parking the whole pool over one rejected value would
	// stop the fleet on a fault that belongs to a single seat.
	other := 1 - picked.Slot
	if v := poolValue(t, s, "claude_code_oauth_token", other); v.CooldownUntil != nil {
		t.Fatalf("the untouched value must stay free: %+v", v)
	}

	// The next choice dodges to the free seat by itself.
	next, err := s.secrets.Pick(ctx, s.orgID, agent.ID, "claude_code_oauth_token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Slot == picked.Slot {
		t.Fatalf("a parked value must not be handed out again (slot %d)", next.Slot)
	}
}

// TestOrdinaryFailureLeavesCredentialAlone is the counter-test, and it is the
// one that matters for the fleet: a run that failed at its TASK must not take a
// working credential out of circulation. Without it a wrong match in the rule
// would park seats all day and nobody would connect the two.
func TestOrdinaryFailureLeavesCredentialAlone(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a-lang-genug"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", agent.ID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert an der Aufgabe",
		"[mock:fail das Repository ließ sich nicht klonen]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})

	if v := poolValue(t, s, "claude_code_oauth_token", 0); v.CooldownUntil != nil {
		t.Fatalf("an ordinary task failure must not park a credential: %+v", v)
	}
}

// TestRunBooksCostAgainstItsCredential: the attribution that makes a per-value
// limit measurable at all. Without it there is no utilisation and no answer to
// whether a seat is too few or too many (spec/18).
func TestRunBooksCostAgainstItsCredential(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a-lang-genug"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", agent.ID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Läuft durch",
		"[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The mock books a cost per run; it has to land on the value the run used.
	usd, tokens, err := s.obs.SlotUsage(ctx, s.orgID, "claude_code_oauth_token", 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if usd <= 0 || tokens <= 0 {
		t.Fatalf("the run's cost has to be attributed to the credential: %.4f USD, %d tokens", usd, tokens)
	}

	// And it shows up in the org breakdown per credential, which is what the
	// view reads.
	rep, err := s.obs.OrgCostReport(ctx, s.orgID, "day", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Credentials) != 1 || rep.Credentials[0].SecretKey != "claude_code_oauth_token" {
		t.Fatalf("credential breakdown: %+v", rep.Credentials)
	}
	if rep.Credentials[0].Slot != 0 || rep.Credentials[0].TotalUSD <= 0 {
		t.Fatalf("credential breakdown carries no figures: %+v", rep.Credentials[0])
	}
}
