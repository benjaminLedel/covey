package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/runtimes"
)

// noUsage is the choice without any outside signal — cooldowns still apply.
var noUsage runtimes.Signals

// seat deposits a secret value and hangs it on a runtime as capacity.
func (s *stack) seat(t *testing.T, rt uuid.UUID, kind, key, value, label string) int {
	t.Helper()
	ctx := context.Background()
	var slot int
	if _, err := s.secrets.Get(ctx, s.orgID, key); err != nil {
		if err := s.secrets.Put(ctx, s.orgID, key, value); err != nil {
			t.Fatal(err)
		}
	} else {
		var aerr error
		if slot, aerr = s.secrets.AddValue(ctx, s.orgID, key, value); aerr != nil {
			t.Fatal(aerr)
		}
	}
	ord, err := s.runtimes.AddCredential(ctx, s.orgID, rt, kind, key, slot, label)
	if err != nil {
		t.Fatal(err)
	}
	return ord
}

// TestRuntimeStickiness: an agent keeps its credential. That is the whole point
// of the distribution — a credential swapped at every wake would throw away the
// prompt cache each time and cost more than the limit saves.
func TestRuntimeStickiness(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	if err != nil {
		t.Fatal(err)
	}
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-a", "Abo A")
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-b", "Abo B")

	alice, bob := s.newSupportAgent("alice"), s.newSupportAgent("bob")

	first, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
		if err != nil {
			t.Fatal(err)
		}
		if again.Ord != first.Ord {
			t.Fatalf("alice moved seat without cause: %d → %d", first.Ord, again.Ord)
		}
		if again.Value != first.Value {
			t.Fatal("the same seat has to resolve to the same value")
		}
	}

	// The second agent lands on the other seat: among like capacity the load is
	// spread, and the emptiest is the one nobody is sitting on.
	other, err := s.runtimes.Pick(ctx, s.orgID, bob.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if other.Ord == first.Ord {
		t.Fatalf("both agents on ord %d — the runtime is not distributing", first.Ord)
	}

	// The engine's declaration decides how the value reaches the sandbox.
	if first.EnvVar != "CLAUDE_CODE_OAUTH_TOKEN" || first.Path != "" {
		t.Fatalf("the delivery form comes from the engine: %+v", first)
	}
}

// TestRuntimeMeritOrder: paid-for capacity before metered. Getting this
// backwards is the expensive mistake — it would leave a subscription seat
// unused, which is money already spent, and pay per token instead.
func TestRuntimeMeritOrder(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Mixed", "")
	if err != nil {
		t.Fatal(err)
	}
	// The metered credential is added FIRST, so position cannot be what decides.
	s.seat(t, rt.ID, daemon.CredAPIKey, "anthropic_api_key", "sk-ant-api-key", "API")
	subOrd := s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sk-ant-oat-seat", "Abo")

	alice := s.newSupportAgent("alice")
	got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ord != subOrd {
		t.Fatalf("the paid-for seat has to be used first, got ord %d", got.Ord)
	}
	if got.Kind != daemon.CredSubscription {
		t.Fatalf("kind = %q", got.Kind)
	}
}

// TestRuntimeDodgesAndReturns: a parked credential pushes the agent onto
// another — and once its home seat is healthy again it goes back, instead of
// the runtime redistributing at every choice.
func TestRuntimeDodgesAndReturns(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, _ := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-a", "A")
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-b", "B")
	alice := s.newSupportAgent("alice")

	home, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	// The hard signal: the provider rejected this credential.
	if err := s.runtimes.Cooldown(ctx, rt.ID, home.Ord, time.Now().Add(time.Hour), runtimes.ReasonError); err != nil {
		t.Fatal(err)
	}
	dodged, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if dodged.Ord == home.Ord {
		t.Fatal("a parked credential must not be handed out again")
	}
	b, _ := s.runtimes.Bindings(ctx, rt.ID)
	if len(b) != 1 || b[0].Reason != runtimes.ReasonError ||
		b[0].HomeOrd == nil || *b[0].HomeOrd != home.Ord {
		t.Fatalf("the dodge must record its reason and home seat: %+v", b)
	}

	// Free again → back to the home seat.
	if err := s.runtimes.Cooldown(ctx, rt.ID, home.Ord, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	back, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if back.Ord != home.Ord {
		t.Fatalf("alice did not return to her home seat: %d instead of %d", back.Ord, home.Ord)
	}
}

// TestRuntimeLimitAndExhaustion: the soft limit moves the agent onward, and
// once every credential is used up the runtime refuses — with the moment it
// frees up again, so the wake can be postponed instead of the run failing.
func TestRuntimeLimitAndExhaustion(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, _ := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	a := s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-a", "A")
	b := s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-b", "B")
	limit := runtimes.Limit{Amount: 10, Unit: "usd", WindowSecs: 3600}
	for _, ord := range []int{a, b} {
		if err := s.runtimes.SetLimit(ctx, s.orgID, rt.ID, ord, limit); err != nil {
			t.Fatal(err)
		}
	}
	alice := s.newSupportAgent("alice")
	usage := runtimes.Signals{Usage: s.obs.CredentialUsage}

	first, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, usage)
	if err != nil {
		t.Fatal(err)
	}
	// Burn its seat empty — booked exactly as a run would book it.
	if err := s.obs.AddCost(ctx, alice.ID, nil, 12.00, observability.Tokens{},
		"claude-opus-5", rt.ID, first.Ord); err != nil {
		t.Fatal(err)
	}
	second, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, usage)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ord == first.Ord {
		t.Fatalf("ord %d is over its limit and must not be picked again", first.Ord)
	}

	if err := s.obs.AddCost(ctx, alice.ID, nil, 12.00, observability.Tokens{},
		"claude-opus-5", rt.ID, second.Ord); err != nil {
		t.Fatal(err)
	}
	_, err = s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, usage)
	if !errors.Is(err, runtimes.ErrExhausted) {
		t.Fatalf("an exhausted runtime has to say so: %v", err)
	}
	var ex *runtimes.Exhausted
	if !errors.As(err, &ex) || ex.Until.IsZero() {
		t.Fatalf("the refusal has to carry the moment it frees up: %v", err)
	}

	// Without a limit check the same runtime is usable again — that is what
	// distinguishes the soft signal from the hard one.
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage); err != nil {
		t.Fatalf("without a limit check the runtime has to deliver: %v", err)
	}
}

// TestRuntimeAssignmentRefusesEngineWithoutResume: an agent whose work blocks
// needs an engine that can continue a session. Finding that out when the first
// customer reply comes back is far too late — the check belongs at the moment
// a person makes the decision.
func TestRuntimeAssignmentAndCapabilities(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	if !runtimes.CanCarryBlocking("claude-code") {
		t.Fatal("claude-code declares resume and has to carry blocking agents")
	}
	if runtimes.CanCarryBlocking("codex") {
		t.Fatal("codex declares no resume — that has to be visible, not assumed away")
	}

	rt, _ := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	alice := s.newSupportAgent("alice")
	if err := s.runtimes.Assign(ctx, s.orgID, alice.ID, rt.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.registry.Get(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID == nil || *got.RuntimeID != rt.ID {
		t.Fatalf("the assignment has to stick: %+v", got.RuntimeID)
	}
	// The engine name follows the contract, so the daemon keeps working off one
	// source rather than two that can disagree.
	if got.Runtime != "claude-code" {
		t.Fatalf("engine = %q", got.Runtime)
	}
}

// TestRuntimeRemoveCredentialDropsSeats: whoever sat on removed capacity gets a
// new seat, and a home seat pointing at it is cleared rather than left dangling.
func TestRuntimeRemoveCredentialDropsSeats(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, _ := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-a", "A")
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-b", "B")
	alice := s.newSupportAgent("alice")

	seat, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.RemoveCredential(ctx, s.orgID, rt.ID, seat.Ord); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.runtimes.Bindings(ctx, rt.ID); len(b) != 0 {
		t.Fatalf("the seat of removed capacity has to go with it: %+v", b)
	}
	next, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if next.Ord == seat.Ord {
		t.Fatalf("removed capacity must not be handed out (ord %d)", next.Ord)
	}
}

// TestRuntimeRefusesUndeclaredCredentialKind: a runtime must not carry capacity
// its engine knows no way to deliver — that would be a credential nothing can
// hand to the sandbox, discovered at the first run.
func TestRuntimeRefusesUndeclaredCredentialKind(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, _ := s.runtimes.Create(ctx, s.orgID, "mock", "Mock", "")
	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-api"); err != nil {
		t.Fatal(err)
	}
	// The mock declares no credentials at all.
	if _, err := s.runtimes.AddCredential(ctx, s.orgID, rt.ID, daemon.CredAPIKey,
		"anthropic_api_key", 0, ""); err == nil {
		t.Fatal("an engine that knows no credentials must not be given one")
	}
	// And an unknown engine cannot be configured in the first place.
	if _, err := s.runtimes.Create(ctx, s.orgID, "gibtsnicht", "X", ""); err == nil {
		t.Fatal("an unknown engine has to be refused")
	}
}

// TestRuntimeSkipsSeatTheProviderReportsFull is the case that actually
// happened in production: an agent sat on a subscription seat whose window was
// used up, and went back to it every fifteen minutes because nothing in Covey
// knew the seat was full.
//
// The knowledge exists — the engine can ask the provider — and this pins that
// it REACHES THE DECISION and not just the interface. It applies without any
// configured limit, because an exhausted seat is a fact and not a policy.
func TestRuntimeSkipsSeatTheProviderReportsFull(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, _ := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	full := s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-voll", "Abo A")
	free := s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "sitz-frei", "Abo B")
	alice := s.newSupportAgent("alice")

	// Pin alice to the seat that is about to be reported as full, so the test
	// checks a MOVE and not a lucky first choice.
	if got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, noUsage); err != nil || got.Ord != full {
		// The distribution picks the emptiest; if it chose the other one,
		// swap the roles so the test still asserts what it means to.
		full, free = free, full
		if err != nil {
			t.Fatal(err)
		}
	}

	reported := runtimes.Signals{Reported: func(_ uuid.UUID, ord int) (float64, bool) {
		if ord == full {
			return 100, true
		}
		return 3, true
	}}
	got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, reported)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ord != free {
		t.Fatalf("a seat the provider reports as full must not be used (got ord %d)", got.Ord)
	}

	// Both full: the runtime refuses, so the wake is postponed instead of a run
	// being burnt on a certain failure.
	bothFull := runtimes.Signals{Reported: func(uuid.UUID, int) (float64, bool) { return 100, true }}
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, bothFull); !errors.Is(err, runtimes.ErrExhausted) {
		t.Fatalf("with every seat full the runtime has to refuse: %v", err)
	}

	// A STALE figure is not acted on — it is up to an hour old, and a window
	// that has since reset would lose us the seat for nothing. The orchestrator
	// withholds those, so here it simply reports nothing.
	unknown := runtimes.Signals{Reported: func(uuid.UUID, int) (float64, bool) { return 0, false }}
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, unknown); err != nil {
		t.Fatalf("without a usable figure the runtime has to deliver: %v", err)
	}
}
