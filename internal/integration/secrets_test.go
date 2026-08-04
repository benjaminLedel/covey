package integration

import (
	"context"
	"errors"
	"testing"

	"covey/internal/secrets"
)

// TestSecretScoping checks the SecretStore's resolution rules against real
// Postgres: without an assignment an org secret reaches nobody → an explicit
// assignment opens access → an agent-owned secret takes precedence.
func TestSecretScoping(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")

	// Org-wide secret without an assignment: reaches no agent.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "org-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("alice must not resolve anything without an assignment: %v", err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob must not resolve anything without an assignment: %v", err)
	}

	// Explicit assignment to alice: only she gets the secret.
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("alice with an assignment: %q, %v", v, err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob must not resolve a secret assigned to alice: %v", err)
	}

	// Agent-owned secret at bob: takes precedence and applies only to him.
	if err := s.secrets.PutAgent(ctx, s.orgID, bob.ID, "zammad_token", "bob-token"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); err != nil || v != "bob-token" {
		t.Fatalf("bob's agent-owned secret: %q, %v", v, err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("alice still sees the org secret: %q, %v", v, err)
	}

	// Previews: the org secret lists its assignment, bob's secret stays agent-owned.
	// Without the sensitive marker a secret is a variable: full plain text.
	previews, err := s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 {
		t.Fatalf("previews: %+v, %v", previews, err)
	}
	if len(previews[0].AgentIDs) != 1 || previews[0].AgentIDs[0] != alice.ID.String() {
		t.Fatalf("the assignment is missing from the preview: %+v", previews[0])
	}
	if previews[0].Sensitive || previews[0].Value != "org-token" {
		t.Fatalf("a non-sensitive org secret has to be viewable: %+v", previews[0])
	}
	ownPreviews, err := s.secrets.AgentPreviews(ctx, s.orgID, bob.ID)
	if err != nil || len(ownPreviews) != 1 || ownPreviews[0].Key != "zammad_token" {
		t.Fatalf("agent-previews: %+v, %v", ownPreviews, err)
	}
	if ownPreviews[0].Sensitive || ownPreviews[0].Value != "bob-token" {
		t.Fatalf("a non-sensitive agent secret has to be viewable: %+v", ownPreviews[0])
	}

	// Marked as sensitive: write-only, prefix only — on both levels.
	if err := s.secrets.MarkSensitive(ctx, s.orgID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.MarkAgentSensitive(ctx, s.orgID, bob.ID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	previews, err = s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || !previews[0].Sensitive || previews[0].Value != "" {
		t.Fatalf("a sensitive org secret must not deliver plain text: %+v, %v", previews, err)
	}
	ownPreviews, err = s.secrets.AgentPreviews(ctx, s.orgID, bob.ID)
	if err != nil || len(ownPreviews) != 1 || !ownPreviews[0].Sensitive || ownPreviews[0].Value != "" {
		t.Fatalf("a sensitive agent secret must not deliver plain text: %+v, %v", ownPreviews, err)
	}
	// The broker still resolves sensitive secrets — the protection is about the UI/API.
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("resolve after markSensitive: %q, %v", v, err)
	}
	// Overwriting keeps the flag — once sensitive, always sensitive.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "org-token-neu-lang"); err != nil {
		t.Fatal(err)
	}
	previews, err = s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || !previews[0].Sensitive {
		t.Fatalf("overwriting must not reset sensitive: %+v, %v", previews, err)
	}
	if previews[0].Value != "" || previews[0].Prefix != "org-" {
		t.Fatalf("sensitive secret: only the prefix is expected: %+v", previews[0])
	}
	if err := s.secrets.MarkSensitive(ctx, s.orgID, "gibt_es_nicht"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("markSensitive on an unknown secret: %v", err)
	}

	// Remove the assignment: alice loses access again; bob keeps only his
	// agent-owned secret until it is deleted.
	if err := s.secrets.Unassign(ctx, s.orgID, "zammad_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("alice after unassign: %v", err)
	}
	if err := s.secrets.DeleteAgent(ctx, s.orgID, bob.ID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob after deleting the agent-owned secret: %v", err)
	}

	// Protective edges: assigning unknown secrets/agents fails cleanly.
	if err := s.secrets.Assign(ctx, s.orgID, "gibt_es_nicht", alice.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("assign on an unknown secret: %v", err)
	}
	if err := s.secrets.PutAgent(ctx, s.orgID, s.orgID /* not an agent id */, "x", "y"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("put for a foreign/unknown agent: %v", err)
	}
}
