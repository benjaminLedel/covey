package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/observability"
	"covey/internal/secrets"
)

// noUsage is the choice without a limit check — cooldowns still apply.
var noUsage secrets.UsageFunc

// TestPoolStickiness: an agent keeps its value. That is the whole point of the
// distribution — a value swapped at every wake would throw away the prompt
// cache each time and cost more than the limit saves.
func TestPoolStickiness(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "claude_code_oauth_token", "sitz-b", "Abo B"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []uuid.UUID{alice.ID, bob.ID} {
		if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", a); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", noUsage)
		if err != nil {
			t.Fatal(err)
		}
		if again.Slot != first.Slot {
			t.Fatalf("alice moved seat without cause: %d → %d", first.Slot, again.Slot)
		}
	}

	// The second agent lands on the other value: without consumption the least
	// loaded one wins, and that is the one nobody is sitting on.
	other, err := s.secrets.Pick(ctx, s.orgID, bob.ID, "claude_code_oauth_token", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if other.Slot == first.Slot {
		t.Fatalf("both agents on slot %d — the pool is not distributing", first.Slot)
	}

	// Both seats are documented, with a reason.
	bindings, err := s.secrets.Bindings(ctx, s.orgID, "claude_code_oauth_token")
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings: %+v, %v", bindings, err)
	}
	for _, b := range bindings {
		if b.Reason != secrets.ReasonInitial {
			t.Fatalf("unexpected reason %q", b.Reason)
		}
	}
}

// TestPoolDodgesAndReturns: a parked value pushes the agent onto another — and
// once its home seat is healthy again it goes back, instead of the pool
// redistributing at every choice.
func TestPoolDodgesAndReturns(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "claude_code_oauth_token", "sitz-b", "Abo B"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", alice.ID); err != nil {
		t.Fatal(err)
	}

	home, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	// The hard signal: the API rejected this value.
	if err := s.secrets.Cooldown(ctx, s.orgID, "claude_code_oauth_token", home.Slot,
		time.Now().Add(time.Hour), secrets.ReasonError); err != nil {
		t.Fatal(err)
	}
	dodged, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if dodged.Slot == home.Slot {
		t.Fatal("a parked value must not be handed out again")
	}
	bindings, _ := s.secrets.Bindings(ctx, s.orgID, "claude_code_oauth_token")
	if len(bindings) != 1 || bindings[0].Reason != secrets.ReasonError ||
		bindings[0].HomeSlot == nil || *bindings[0].HomeSlot != home.Slot {
		t.Fatalf("the dodge must record its reason and home seat: %+v", bindings)
	}

	// Free again → back to the home seat.
	if err := s.secrets.Cooldown(ctx, s.orgID, "claude_code_oauth_token", home.Slot, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	back, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if back.Slot != home.Slot {
		t.Fatalf("alice did not return to her home seat: %d instead of %d", back.Slot, home.Slot)
	}
}

// TestPoolLimitAndExhaustion: the soft limit moves the agent onward, and once
// every value is used up the pool refuses — with the moment it frees up again,
// so the control plane can postpone the run instead of failing it.
func TestPoolLimitAndExhaustion(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "schluessel-a"); err != nil {
		t.Fatal(err)
	}
	slotB, err := s.secrets.AddValue(ctx, s.orgID, "anthropic_api_key", "schluessel-b", "Konto B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "anthropic_api_key", alice.ID); err != nil {
		t.Fatal(err)
	}
	limit := secrets.Limit{Amount: 10, Unit: "usd", WindowSecs: 3600}
	for _, slot := range []int{0, slotB} {
		if err := s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", slot, limit); err != nil {
			t.Fatal(err)
		}
	}

	usage := secrets.UsageFunc(s.obs.SlotUsage)
	first, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "anthropic_api_key", usage)
	if err != nil {
		t.Fatal(err)
	}

	// Burn its seat empty — booked exactly as a run would book it.
	if err := s.obs.AddCost(ctx, alice.ID, nil, 12.00, observability.Tokens{},
		"claude-opus-5", "anthropic_api_key", first.Slot); err != nil {
		t.Fatal(err)
	}
	second, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "anthropic_api_key", usage)
	if err != nil {
		t.Fatal(err)
	}
	if second.Slot == first.Slot {
		t.Fatalf("slot %d is over its limit and must not be picked again", first.Slot)
	}

	// The second one too → nothing left.
	if err := s.obs.AddCost(ctx, alice.ID, nil, 12.00, observability.Tokens{},
		"claude-opus-5", "anthropic_api_key", second.Slot); err != nil {
		t.Fatal(err)
	}
	_, err = s.secrets.Pick(ctx, s.orgID, alice.ID, "anthropic_api_key", usage)
	if !errors.Is(err, secrets.ErrPoolExhausted) {
		t.Fatalf("an exhausted pool has to say so: %v", err)
	}
	var pe *secrets.PoolExhausted
	if !errors.As(err, &pe) || pe.Until.IsZero() {
		t.Fatalf("the refusal has to carry the moment it frees up: %v", err)
	}

	// Without a limit check the same pool is usable again — that is what
	// distinguishes the soft signal from the hard one: Resolve books nothing
	// and is therefore not stopped by a limit.
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "anthropic_api_key"); err != nil {
		t.Fatalf("without a limit check the pool has to deliver: %v", err)
	}
}

// TestPoolAdminRefusals covers the paths that only run when something is wrong.
// They matter more than they look: every one of them is a case where the store
// could plausibly do nothing and report success, and a pool that silently
// ignores a limit is worse than one that has none.
func TestPoolAdminRefusals(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// A pool grows out of an existing secret — never out of nothing.
	if _, err := s.secrets.AddValue(ctx, s.orgID, "gibtsnicht", "wert", ""); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("AddValue on an unknown key: %v", err)
	}

	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "schluessel-a"); err != nil {
		t.Fatal(err)
	}

	// Every administrative operation on a slot that does not exist has to say
	// so rather than quietly affect nothing.
	limit := secrets.Limit{Amount: 5, Unit: "usd", WindowSecs: 3600}
	for _, c := range []struct {
		name string
		err  error
	}{
		{"SetLimit", s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", 7, limit)},
		{"SetLabel", s.secrets.SetLabel(ctx, s.orgID, "anthropic_api_key", 7, "x")},
		{"Cooldown", s.secrets.Cooldown(ctx, s.orgID, "anthropic_api_key", 7, time.Now().Add(time.Hour), "error")},
		{"SetLimit/unknown key", s.secrets.SetLimit(ctx, s.orgID, "gibtsnicht", 0, limit)},
		{"SetLabel/unknown key", s.secrets.SetLabel(ctx, s.orgID, "gibtsnicht", 0, "x")},
		{"Cooldown/unknown key", s.secrets.Cooldown(ctx, s.orgID, "gibtsnicht", 0, time.Time{}, "")},
	} {
		if !errors.Is(c.err, secrets.ErrNotFound) {
			t.Fatalf("%s on a missing value: %v", c.name, c.err)
		}
	}

	// An unknown unit must not be storable — a limit in a unit nothing measures
	// against would stand in the view looking valid.
	if err := s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", 0,
		secrets.Limit{Amount: 5, Unit: "bananen", WindowSecs: 3600}); err == nil {
		t.Fatal("an unknown limit unit has to be refused")
	}
	// An empty unit is filled in rather than refused — the caller meant money.
	if err := s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", 0,
		secrets.Limit{Amount: 5, WindowSecs: 3600}); err != nil {
		t.Fatalf("an empty unit has to default: %v", err)
	}

	// The last value only goes with the key.
	if err := s.secrets.DeleteValue(ctx, s.orgID, "anthropic_api_key", 0); !errors.Is(err, secrets.ErrLastValue) {
		t.Fatalf("deleting the last value: %v", err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "anthropic_api_key", "schluessel-b", "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.DeleteValue(ctx, s.orgID, "anthropic_api_key", 7); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("deleting a value that does not exist: %v", err)
	}

	// Lifting a limit also lifts a cooldown the limit itself imposed —
	// otherwise the value stays parked over a rule that no longer exists.
	if err := s.secrets.Cooldown(ctx, s.orgID, "anthropic_api_key", 1, time.Now().Add(time.Hour), "limit"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", 1, secrets.Limit{}); err != nil {
		t.Fatal(err)
	}
	if v := poolValue(t, s, "anthropic_api_key", 1); v.CooldownUntil != nil {
		t.Fatalf("lifting the limit has to free the value it parked: %+v", v)
	}
	// A cooldown from the HARD signal survives it — that one was not the
	// limit's doing, and clearing it would hand out a rejected credential.
	if err := s.secrets.Cooldown(ctx, s.orgID, "anthropic_api_key", 1,
		time.Now().Add(time.Hour), secrets.ReasonError); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.SetLimit(ctx, s.orgID, "anthropic_api_key", 1, secrets.Limit{}); err != nil {
		t.Fatal(err)
	}
	if v := poolValue(t, s, "anthropic_api_key", 1); v.CooldownUntil == nil {
		t.Fatal("a cooldown from a rejection must survive a limit being lifted")
	}
}

// TestDeleteSecretClearsBindings: a binding left behind would send the next
// pool of the same name to a seat that no longer exists.
func TestDeleteSecretClearsBindings(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "claude_code_oauth_token", "sitz-b", "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.secrets.Bindings(ctx, s.orgID, "claude_code_oauth_token"); len(b) != 1 {
		t.Fatalf("a seat should have been recorded: %+v", b)
	}

	if err := s.secrets.Delete(ctx, s.orgID, "claude_code_oauth_token"); err != nil {
		t.Fatal(err)
	}
	if b, err := s.secrets.Bindings(ctx, s.orgID, "claude_code_oauth_token"); err != nil || len(b) != 0 {
		t.Fatalf("bindings have to go with the key: %+v, %v", b, err)
	}
	// The assignment too — otherwise a new secret of the same name would reach
	// an agent nobody assigned it to.
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "neu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "claude_code_oauth_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("a recreated secret must not inherit the old assignment: %v", err)
	}
}

// TestDeleteValueDropsItsSeats: whoever sat on the removed value gets a new
// seat, and a home seat pointing at it is dropped rather than left dangling.
func TestDeleteValueDropsItsSeats(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "sitz-a"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"sitz-b", "sitz-c"} {
		if _, err := s.secrets.AddValue(ctx, s.orgID, "claude_code_oauth_token", v, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	seat, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.secrets.DeleteValue(ctx, s.orgID, "claude_code_oauth_token", seat.Slot); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.secrets.Bindings(ctx, s.orgID, "claude_code_oauth_token"); len(b) != 0 {
		t.Fatalf("the seat of a removed value has to go with it: %+v", b)
	}
	// And the agent simply gets another one on its next choice.
	next, err := s.secrets.Pick(ctx, s.orgID, alice.ID, "claude_code_oauth_token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Slot == seat.Slot {
		t.Fatalf("the removed value must not be handed out (slot %d)", next.Slot)
	}
}

// TestPoolGetSkipsParkedValues: Get has no agent whose seat it could keep — it
// takes the lowest HEALTHY value. That path carries the org's own LLM calls
// (config copilot, dream), and handing them a value the API has just rejected
// would let them fail for a reason the pool already knows about.
func TestPoolGetSkipsParkedValues(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "schluessel-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.AddValue(ctx, s.orgID, "anthropic_api_key", "schluessel-b", "Konto B"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Get(ctx, s.orgID, "anthropic_api_key"); err != nil || v != "schluessel-a" {
		t.Fatalf("without a cooldown the lowest value applies: %q, %v", v, err)
	}

	if err := s.secrets.Cooldown(ctx, s.orgID, "anthropic_api_key", 0,
		time.Now().Add(time.Hour), secrets.ReasonError); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Get(ctx, s.orgID, "anthropic_api_key"); err != nil || v != "schluessel-b" {
		t.Fatalf("a parked value must be skipped: %q, %v", v, err)
	}

	// Everything parked: the lowest one applies anyway. A caller without an
	// agent has no run it could postpone, and refusing would help nobody.
	if err := s.secrets.Cooldown(ctx, s.orgID, "anthropic_api_key", 1,
		time.Now().Add(time.Hour), secrets.ReasonError); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Get(ctx, s.orgID, "anthropic_api_key"); err != nil || v != "schluessel-a" {
		t.Fatalf("with everything parked Get still has to deliver: %q, %v", v, err)
	}
}

// TestPoolLeavesSingleValueBehaviourAlone: a key with one value must behave
// exactly as before — that is the case for practically every target-system
// token, and Resolve runs through the choice on every brokered credential.
func TestPoolLeavesSingleValueBehaviourAlone(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "org-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("resolve: %q, %v", v, err)
	}
	// No seat is recorded for a single value — there is no choice to document,
	// and writing one would mean a write on every read path.
	if b, err := s.secrets.Bindings(ctx, s.orgID, "zammad_token"); err != nil || len(b) != 0 {
		t.Fatalf("a single value needs no binding: %+v, %v", b, err)
	}
	// The preview carries the pool, and it holds exactly one value.
	previews, err := s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || len(previews[0].Values) != 1 {
		t.Fatalf("previews: %+v, %v", previews, err)
	}
	if previews[0].Values[0].Value != "org-token" {
		t.Fatalf("the value of a variable belongs in the preview: %+v", previews[0].Values[0])
	}
}
