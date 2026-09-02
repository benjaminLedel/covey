package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"covey/internal/secrets"
)

// TestSecretLifetime checks what the store keeps about a value beyond the
// value: a rejection is news once, a working probe clears it, a new value
// starts with nothing known, and Lookup names the row Resolve would use.
func TestSecretLifetime(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")

	if err := s.secrets.Put(ctx, s.orgID, "jira_token", "org-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "jira_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.PutAgent(ctx, s.orgID, bob.ID, "jira_token", "bob-token"); err != nil {
		t.Fatal(err)
	}

	// Lookup follows Resolve: alice gets the org row, bob his own.
	st, err := s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if err != nil || st.AgentID != nil || st.Slot != 0 {
		t.Fatalf("alice's row: %+v, %v", st, err)
	}
	st, err = s.secrets.Lookup(ctx, s.orgID, bob.ID, "jira_token")
	if err != nil || st.AgentID == nil || *st.AgentID != bob.ID {
		t.Fatalf("bob's row: %+v, %v", st, err)
	}
	if _, err := s.secrets.Lookup(ctx, s.orgID, alice.ID, "nothing_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}

	// The first rejection is news, the second is not.
	st, news, err := s.secrets.MarkRejected(ctx, s.orgID, alice.ID, "jira_token", "HTTP 401")
	if err != nil || !news || st.RejectedAt == nil || st.RejectedReason != "HTTP 401" {
		t.Fatalf("first rejection: %+v news=%v %v", st, news, err)
	}
	first := *st.RejectedAt
	st, news, err = s.secrets.MarkRejected(ctx, s.orgID, alice.ID, "jira_token", "HTTP 401 again")
	if err != nil || news || !st.RejectedAt.Equal(first) || st.RejectedReason != "HTTP 401 again" {
		t.Fatalf("second rejection keeps the first date: %+v news=%v %v", st, news, err)
	}
	// Bob's own row is untouched by alice's rejection.
	if st, _ := s.secrets.Lookup(ctx, s.orgID, bob.ID, "jira_token"); st.RejectedAt != nil {
		t.Fatal("bob's row was not rejected")
	}

	// The org row is what the previews show — with the state on it.
	previews, err := s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || previews[0].RejectedAt == nil || previews[0].Values[0].RejectedAt == nil {
		t.Fatalf("previews carry the lifetime: %+v, %v", previews, err)
	}

	// A working probe clears the rejection and keeps what it learned.
	org := secrets.Ref{OrgID: s.orgID, Key: "jira_token"}
	exp := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := s.secrets.RecordProbe(ctx, org, secrets.Probe{At: time.Now(), Identity: "Covey Bot", ExpiresAt: &exp, CredentialID: "7", Rotatable: true}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.RejectedAt != nil || st.ProbeIdentity != "Covey Bot" || st.ExpiresAt == nil || !st.ExpiresAt.Equal(exp) || st.CredentialID != "7" || !st.Rotatable {
		t.Fatalf("after a good probe: %+v", st)
	}
	// A probe that learns nothing new does not unsay what it knew.
	if err := s.secrets.RecordProbe(ctx, org, secrets.Probe{At: time.Now(), Identity: "Covey Bot", Rotatable: true}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.ExpiresAt == nil || st.CredentialID != "7" {
		t.Fatalf("a probe without news keeps the old news: %+v", st)
	}
	// A failed probe that is not a rejection leaves the credential standing.
	if err := s.secrets.RecordProbe(ctx, org, secrets.Probe{At: time.Now(), Err: "dial tcp: no route"}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.RejectedAt != nil || st.ProbeError != "dial tcp: no route" {
		t.Fatalf("a network error is not a rejection: %+v", st)
	}
	// A rejecting probe is.
	if err := s.secrets.RecordProbe(ctx, org, secrets.Probe{At: time.Now(), Err: "HTTP 401", Rejected: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.MarkWarned(ctx, org); err != nil {
		t.Fatal(err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.RejectedAt == nil || st.WarnedAt == nil {
		t.Fatalf("a rejecting probe rejects, and the warning is noted: %+v", st)
	}

	// A new value is a new credential: nothing known.
	if err := s.secrets.Put(ctx, s.orgID, "jira_token", "fresh-token"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.RejectedAt != nil || st.ExpiresAt != nil || st.ProbedAt != nil || st.WarnedAt != nil || st.CredentialID != "" || st.Rotatable {
		t.Fatalf("a new value starts clean: %+v", st)
	}

	// A person enters the date; List sees both rows; Replace rotates in place.
	if err := s.secrets.SetExpiry(ctx, org, &exp); err != nil {
		t.Fatal(err)
	}
	all, err := s.secrets.List(ctx, s.orgID, "_token")
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %+v, %v", all, err)
	}
	if all[0].AgentID != nil || all[0].ExpiresAt == nil || all[1].AgentID == nil {
		t.Fatalf("org row first, with its date; then bob's: %+v", all)
	}
	if _, err := s.secrets.List(ctx, s.orgID, "_tok"); err != nil {
		t.Fatal(err)
	} else if n, _ := s.secrets.List(ctx, s.orgID, "_tok"); len(n) != 0 {
		t.Fatal("the suffix is exact — LIKE's underscore does not widen it")
	}
	later := exp.AddDate(1, 0, 0)
	if err := s.secrets.Replace(ctx, org, "rotated-token", secrets.Lifetime{ExpiresAt: &later, CredentialID: "8", Rotatable: true}); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Open(ctx, org); err != nil || v != "rotated-token" {
		t.Fatalf("open after replace: %q, %v", v, err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "jira_token"); err != nil || v != "rotated-token" {
		t.Fatalf("resolve after replace: %q, %v", v, err)
	}
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "jira_token")
	if st.CredentialID != "8" || st.ExpiresAt == nil || !st.ExpiresAt.Equal(later) {
		t.Fatalf("replace carries the successor's lifetime: %+v", st)
	}
	if err := s.secrets.Replace(ctx, secrets.Ref{OrgID: s.orgID, Key: "jira_token", Slot: 9}, "x", secrets.Lifetime{}); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("replace does not create: %v", err)
	}
}
