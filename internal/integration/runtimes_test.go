package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

	first, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
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
	other, err := s.runtimes.Pick(ctx, s.orgID, bob.ID, rt.ID, "", noUsage)
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
	got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
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

	home, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	// The hard signal: the provider rejected this credential.
	if err := s.runtimes.Cooldown(ctx, rt.ID, home.Ord, time.Now().Add(time.Hour), runtimes.ReasonError); err != nil {
		t.Fatal(err)
	}
	dodged, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
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
	back, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
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

	first, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", usage)
	if err != nil {
		t.Fatal(err)
	}
	// Burn its seat empty — booked exactly as a run would book it.
	if err := s.obs.AddCost(ctx, alice.ID, nil, 12.00, observability.Tokens{},
		"claude-opus-5", rt.ID, first.Ord); err != nil {
		t.Fatal(err)
	}
	second, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", usage)
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
	_, err = s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", usage)
	if !errors.Is(err, runtimes.ErrExhausted) {
		t.Fatalf("an exhausted runtime has to say so: %v", err)
	}
	var ex *runtimes.Exhausted
	if !errors.As(err, &ex) || ex.Until.IsZero() {
		t.Fatalf("the refusal has to carry the moment it frees up: %v", err)
	}

	// Without a limit check the same runtime is usable again — that is what
	// distinguishes the soft signal from the hard one.
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage); err != nil {
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

	seat, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.RemoveCredential(ctx, s.orgID, rt.ID, seat.Ord); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.runtimes.Bindings(ctx, rt.ID); len(b) != 0 {
		t.Fatalf("the seat of removed capacity has to go with it: %+v", b)
	}
	next, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage)
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
// used up, and went back to it every fifteen minutes because nothing in covey
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
	if got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", noUsage); err != nil || got.Ord != full {
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
	got, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", reported)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ord != free {
		t.Fatalf("a seat the provider reports as full must not be used (got ord %d)", got.Ord)
	}

	// Both full: the runtime refuses, so the wake is postponed instead of a run
	// being burnt on a certain failure.
	bothFull := runtimes.Signals{Reported: func(uuid.UUID, int) (float64, bool) { return 100, true }}
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", bothFull); !errors.Is(err, runtimes.ErrExhausted) {
		t.Fatalf("with every seat full the runtime has to refuse: %v", err)
	}

	// A STALE figure is not acted on — it is up to an hour old, and a window
	// that has since reset would lose us the seat for nothing. The orchestrator
	// withholds those, so here it simply reports nothing.
	unknown := runtimes.Signals{Reported: func(uuid.UUID, int) (float64, bool) { return 0, false }}
	if _, err := s.runtimes.Pick(ctx, s.orgID, alice.ID, rt.ID, "", unknown); err != nil {
		t.Fatalf("without a usable figure the runtime has to deliver: %v", err)
	}
}

// TestFreshSetupNeedsNoWorkplaceStep walks the path of somebody installing
// covey: deposit one token, create one agent, done. No workplace is created by
// hand, because the contract model only earns its keep with the SECOND
// credential and has to carry the first one silently.
//
// This is a regression test in the literal sense. The runtime layer broke
// exactly this: the migration builds workplaces from EXISTING agents, so a
// fresh database got none, a new agent got no assignment, and its first run
// failed on a missing credential — with the token sitting right there under
// Secrets.
func TestFreshSetupNeedsNoWorkplaceStep(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Step 1 of the checklist: the credential.
	admin.expect(http.MethodPut, "/api/v1/secrets/claude_code_oauth_token",
		map[string]any{"value": "sk-ant-oat-erstes-token", "sensitive": true}, http.StatusOK)

	// Step 2: the agent. Nothing else.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "erster", "display_name": "Erster Agent", "runtime": "claude-code"},
		http.StatusCreated)
	agentID, _ := uuid.Parse(created["id"].(string))

	// It has to be able to work now — that means: a workplace with capacity,
	// and the agent sitting on it.
	list, err := s.runtimes.List(ctx, s.orgID)
	if err != nil || len(list) != 1 {
		t.Fatalf("exactly one workplace should have appeared: %+v, %v", list, err)
	}
	if len(list[0].Credentials) != 1 {
		t.Fatalf("the deposited token has to become capacity: %+v", list[0].Credentials)
	}
	got, err := s.registry.Get(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID == nil || *got.RuntimeID != list[0].ID {
		t.Fatalf("the agent has to be assigned: %+v", got.RuntimeID)
	}

	// And the decisive one: a credential can actually be picked for it.
	p, err := s.runtimes.Pick(ctx, s.orgID, agentID, list[0].ID, "", noUsage)
	if err != nil {
		t.Fatalf("the agent has to reach a credential: %v", err)
	}
	if p.Value != "sk-ant-oat-erstes-token" || p.EnvVar != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("wrong credential brokered: %+v", p)
	}
}

// TestSecondTokenNeedsNoSecondPlace: adding a seat later must also not require
// visiting a second screen. Whoever deposits a further value gets further
// capacity.
func TestSecondTokenNeedsNoSecondPlace(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	admin.expect(http.MethodPut, "/api/v1/secrets/claude_code_oauth_token",
		map[string]any{"value": "sk-ant-oat-erstes", "sensitive": true}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "erster", "display_name": "Erster", "runtime": "claude-code"},
		http.StatusCreated)

	// The second seat, deposited the obvious way: another value under the same
	// key.
	admin.expect(http.MethodPost, "/api/v1/secrets/claude_code_oauth_token/values",
		map[string]any{"value": "sk-ant-oat-zweites"}, http.StatusOK)

	list, err := s.runtimes.List(ctx, s.orgID)
	if err != nil || len(list) != 1 {
		t.Fatalf("workplaces: %+v, %v", list, err)
	}
	if len(list[0].Credentials) != 2 {
		t.Fatalf("the second token has to become capacity by itself: %+v", list[0].Credentials)
	}

	// Idempotent: depositing again must not pile up duplicates.
	admin.expect(http.MethodPut, "/api/v1/secrets/claude_code_oauth_token",
		map[string]any{"value": "sk-ant-oat-erstes-korrigiert", "sensitive": true}, http.StatusOK)
	again, _ := s.runtimes.List(ctx, s.orgID)
	if len(again[0].Credentials) != 2 {
		t.Fatalf("a correction must not add capacity: %+v", again[0].Credentials)
	}
}

// TestAgentWithoutWorkplaceIsTakenIn: an agent that did not come through the
// API — `covey bootstrap` creates one, so does a bundle import — must not stay
// without a workplace. Until this was fixed, exactly the agent that a fresh
// installation is handed as its entry point was the one that could not work:
// the token lay under Secrets, the run failed at the login, and nothing in the
// interface said why.
func TestAgentWithoutWorkplaceIsTakenIn(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Created past the HTTP layer, exactly as bootstrap does it.
	agent, err := s.registry.Create(ctx, s.orgID, "demo", "Demo agent", "claude-code", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.RuntimeID != nil {
		t.Fatalf("test premise gone: this route assigns nothing (%v)", agent.RuntimeID)
	}

	// The one step the checklist asks for.
	admin.expect(http.MethodPut, "/api/v1/secrets/claude_code_oauth_token",
		map[string]any{"value": "sk-ant-oat-token", "sensitive": true}, http.StatusOK)

	list, err := s.runtimes.List(ctx, s.orgID)
	if err != nil || len(list) != 1 {
		t.Fatalf("exactly one workplace should have appeared: %+v, %v", list, err)
	}
	got, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID == nil || *got.RuntimeID != list[0].ID {
		t.Fatalf("the agent standing there without a workplace has to be taken in: %+v", got.RuntimeID)
	}
	if _, err := s.runtimes.Pick(ctx, s.orgID, agent.ID, list[0].ID, "", noUsage); err != nil {
		t.Fatalf("and reach a credential: %v", err)
	}
}

// TestDeliberateAssignmentSurvives: taking in orphans must not turn into
// redistribution. Whoever puts an agent on a particular contract has made a
// commercial decision, and the next deposited token does not overrule it.
func TestDeliberateAssignmentSurvives(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	special, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Team contract", "")
	if err != nil {
		t.Fatal(err)
	}
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "erster", "display_name": "Erster", "runtime": "claude-code"},
		http.StatusCreated)
	agentID, _ := uuid.Parse(created["id"].(string))
	if err := s.runtimes.Assign(ctx, s.orgID, agentID, special.ID); err != nil {
		t.Fatal(err)
	}

	admin.expect(http.MethodPut, "/api/v1/secrets/claude_code_oauth_token",
		map[string]any{"value": "sk-ant-oat-token", "sensitive": true}, http.StatusOK)

	got, err := s.registry.Get(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeID == nil || *got.RuntimeID != special.ID {
		t.Fatalf("the chosen contract has to stand: %+v", got.RuntimeID)
	}
}

// TestASeatOfAnotherEngineIsRefusedRatherThanBrokered: the seat carries the
// credentials of ITS engine, so an agent whose engine and seat disagree does
// not get a bad error — it gets somebody else's token, under somebody else's
// variable. The engine at the far end then reports "not logged in", which
// points at the credential, where nothing is wrong.
//
// Found on the live instance: an agent switched to a second engine kept its
// Claude Code seat and was handed a Claude Code subscription token for an
// educa-ai run. Fail closed, and name both sides.
func TestASeatOfAnotherEngineIsRefusedRatherThanBrokered(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	rt, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	if err != nil {
		t.Fatal(err)
	}
	s.seat(t, rt.ID, daemon.CredSubscription, "claude_code_oauth_token", "abo-token", "Abo A")
	agent := s.newSupportAgent("wanderer")

	// On its own engine the seat serves, as before.
	if _, err := s.runtimes.Pick(ctx, s.orgID, agent.ID, rt.ID, "claude-code", noUsage); err != nil {
		t.Fatalf("the matching engine has to be served: %v", err)
	}

	// On another engine it must not — and above all it must not hand over the
	// value it holds.
	p, err := s.runtimes.Pick(ctx, s.orgID, agent.ID, rt.ID, "educa-ai", noUsage)
	if err == nil {
		t.Fatal("a seat of another engine must not be brokered")
	}
	if p.Value != "" {
		t.Fatal("the credential must not leave the store on a refused pick")
	}
	var wrong *runtimes.WrongEngine
	if !errors.As(err, &wrong) {
		t.Fatalf("the caller has to be able to tell this apart from a missing credential: %T %v", err, err)
	}
	for _, want := range []string{"educa-ai", "claude-code"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message has to name %q so the reader looks at the seat: %v", want, err)
		}
	}

	// An empty claim stays permissive: callers that do not know an engine
	// (tools, older code paths) are not broken by the check.
	if _, err := s.runtimes.Pick(ctx, s.orgID, agent.ID, rt.ID, "", noUsage); err != nil {
		t.Fatalf("without a claim the pick has to work as before: %v", err)
	}
}

// TestTheSeatFollowsTheEngine: an agent's engine and its seat are two fields,
// and they can drift. Changing the engine already resets the reasoning-effort
// level for the same reason ("a level of the old engine that nobody reads any
// more"), but the seat weighed more and stayed put — and the seat is what
// carries the credentials.
//
// Both halves of the rule are here: the change takes the seat with it, and an
// assignment that would recreate the mismatch is refused where somebody is
// looking rather than at the next wake.
func TestTheSeatFollowsTheEngine(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	c := login(t, s, "admin@test.local", "admin-passwort")

	claude, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	if err != nil {
		t.Fatal(err)
	}
	s.seat(t, claude.ID, daemon.CredSubscription, "claude_code_oauth_token", "abo-token", "Abo A")
	educa, err := s.runtimes.Create(ctx, s.orgID, "educa-ai", "Internal Inference", "")
	if err != nil {
		t.Fatal(err)
	}
	s.seat(t, educa.ID, daemon.CredAPIKey, "educa_api_token", "educa-token", "covey Key")

	// newSupportAgent runs on the mock; the first engine change is already the
	// first half of the rule — it has to put the agent on a Claude Code seat.
	agent := s.newSupportAgent("umsteiger")
	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime",
		map[string]string{"runtime": "claude-code"}, http.StatusOK)

	seatOf := func() uuid.UUID {
		t.Helper()
		a, err := s.registry.Get(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if a.RuntimeID == nil {
			t.Fatal("the agent has no seat")
		}
		return *a.RuntimeID
	}
	if got := seatOf(); got != claude.ID {
		t.Fatalf("the engine change has to seat the agent on the Claude Code seat, got %s", got)
	}

	// The engine changes — and the seat has to come along, or the next wake
	// brokers a Claude Code subscription token into an educa run.
	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime",
		map[string]string{"runtime": "educa-ai"}, http.StatusOK)
	if got := seatOf(); got != educa.ID {
		t.Fatalf("the seat has to follow the engine: agent sits on %s, expected the educa seat %s", got, educa.ID)
	}

	// And the mismatch cannot be re-created by hand.
	c.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/runtime-instance",
		map[string]string{"runtime_id": claude.ID.String()}, http.StatusBadRequest)
	if got := seatOf(); got != educa.ID {
		t.Fatalf("a refused assignment must not move the agent: %s", got)
	}

	// A deliberate choice within the SAME engine survives, so that saving the
	// engine does not undo somebody's decision. A second educa seat, chosen by
	// hand, stays chosen.
	second, err := s.runtimes.Create(ctx, s.orgID, "educa-ai", "Internal Inference 2", "")
	if err != nil {
		t.Fatal(err)
	}
	s.seat(t, second.ID, daemon.CredAPIKey, "educa_api_token", "educa-token-2", "covey Key 2")
	c.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/runtime-instance",
		map[string]string{"runtime_id": second.ID.String()}, http.StatusOK)
	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime",
		map[string]string{"runtime": "educa-ai"}, http.StatusOK)
	if got := seatOf(); got != second.ID {
		t.Fatalf("a seat of the right engine must not be taken away: %s, expected %s", got, second.ID)
	}
}
