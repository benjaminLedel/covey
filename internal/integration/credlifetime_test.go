package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/benjaminLedel/covey-plugin-sdk/target"

	"covey/internal/backlog"
	"covey/internal/secrets"
)

// lifetimeTestSystem is a target system whose credential has a life: it can
// say when the token runs out, mint the successor, and refuse a token it does
// not know. What it does is set from the test.
type lifetimeTestSystem struct{}

var lifetimeState struct {
	sync.Mutex
	valid     map[string]time.Time // token → expiry
	rejectAll bool
	minted    int
	revoked   []string
}

func init() {
	lifetimeState.valid = map[string]time.Time{}
	target.Register(target.Descriptor{
		Name: "lifetest", Label: "Lifetest", Kind: "builtin", BaseURLOptional: true,
		System: lifetimeTestSystem{},
	})
}

func (lifetimeTestSystem) Name() string { return "lifetest" }
func (lifetimeTestSystem) ActionSubject(action string, _ json.RawMessage) string {
	return "lifetest:" + action
}
func (lifetimeTestSystem) PromptDoc() string { return "" }

func (s lifetimeTestSystem) Execute(ctx context.Context, action string, _ json.RawMessage, cred target.Credential) (any, error) {
	if _, err := s.Inspect(ctx, cred); err != nil {
		return nil, err
	}
	return map[string]any{"did": action}, nil
}

func (lifetimeTestSystem) Probe(ctx context.Context, cred target.Credential) (string, error) {
	info, err := lifetimeTestSystem{}.Inspect(ctx, cred)
	return info.Identity, err
}

func (lifetimeTestSystem) Inspect(_ context.Context, cred target.Credential) (target.CredentialInfo, error) {
	lifetimeState.Lock()
	defer lifetimeState.Unlock()
	exp, ok := lifetimeState.valid[cred.Token]
	if lifetimeState.rejectAll || !ok {
		return target.CredentialInfo{}, &target.CredentialRejectedError{Status: 401, Err: errors.New("Unauthorized")}
	}
	return target.CredentialInfo{Identity: "bot", ID: "id-" + cred.Token, ExpiresAt: &exp, Rotatable: true}, nil
}

func (lifetimeTestSystem) Rotate(_ context.Context, cred target.Credential) (target.Credential, target.CredentialInfo, error) {
	lifetimeState.Lock()
	defer lifetimeState.Unlock()
	if _, ok := lifetimeState.valid[cred.Token]; !ok {
		return target.Credential{}, target.CredentialInfo{}, errors.New("a dead token mints nothing")
	}
	lifetimeState.minted++
	tok := fmt.Sprintf("minted-%d", lifetimeState.minted)
	exp := time.Now().Add(365 * 24 * time.Hour)
	lifetimeState.valid[tok] = exp
	return target.Credential{Token: tok, BaseURL: cred.BaseURL}, target.CredentialInfo{ID: "id-" + tok, ExpiresAt: &exp, Rotatable: true}, nil
}

func (lifetimeTestSystem) Revoke(_ context.Context, _ target.Credential, id string) error {
	lifetimeState.Lock()
	defer lifetimeState.Unlock()
	lifetimeState.revoked = append(lifetimeState.revoked, id)
	return nil
}

func resetLifetimeState() {
	lifetimeState.Lock()
	defer lifetimeState.Unlock()
	lifetimeState.valid = map[string]time.Time{}
	lifetimeState.rejectAll = false
	lifetimeState.minted = 0
	lifetimeState.revoked = nil
}

// TestCredentialCheckRotatesAndWarns: the daily check probes a stored token,
// rotates one that is about to run out and can be, and — once — tells a
// person about one that is refused.
func TestCredentialCheckRotatesAndWarns(t *testing.T) {
	resetLifetimeState()
	s := newStack(t)
	ctx := context.Background()
	alice := s.newSupportAgent("alice")

	soon := time.Now().Add(10 * 24 * time.Hour)
	lifetimeState.Lock()
	lifetimeState.valid["old-token"] = soon
	lifetimeState.Unlock()
	if err := s.secrets.Put(ctx, s.orgID, "lifetest_token", "old-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "lifetest_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	ref := secrets.Ref{OrgID: s.orgID, Key: "lifetest_token"}

	// Ten days left, rotatable: the check mints the successor, verifies it,
	// stores it, revokes the old one. Nobody needs a mail.
	s.orch.CheckCredentials(ctx)
	if v, _ := s.secrets.Open(ctx, ref); v != "minted-1" {
		t.Fatalf("token after the check = %q, want the successor", v)
	}
	st, _ := s.secrets.Lookup(ctx, s.orgID, alice.ID, "lifetest_token")
	if st.CredentialID != "id-minted-1" || st.ExpiresAt == nil || st.ExpiresAt.Before(soon.Add(24*time.Hour)) || st.ProbeIdentity != "bot" {
		t.Fatalf("the successor's lifetime is on the row: %+v", st)
	}
	lifetimeState.Lock()
	revoked := append([]string(nil), lifetimeState.revoked...)
	lifetimeState.Unlock()
	if len(revoked) != 1 || revoked[0] != "id-old-token" {
		t.Fatalf("the predecessor is revoked by the id the probe reported: %v", revoked)
	}
	if n := s.notificationCount(t); n != 0 {
		t.Fatalf("a rotation that worked is nobody's business: %d notifications", n)
	}
	var rotated int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='credential' AND (payload->>'rotated')::bool`, alice.ID).Scan(&rotated); err != nil {
		t.Fatal(err)
	}
	if rotated != 1 {
		t.Fatalf("the rotation stands in the assignee's recording: %d", rotated)
	}

	// The system starts refusing the token: the check marks it, and one
	// person hears about it — once, however often the check runs.
	lifetimeState.Lock()
	lifetimeState.rejectAll = true
	lifetimeState.Unlock()
	s.orch.CheckCredentials(ctx)
	s.orch.CheckCredentials(ctx)
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "lifetest_token")
	if st.RejectedAt == nil || st.WarnedAt == nil || st.ProbeError == "" {
		t.Fatalf("a refused token is marked and reported: %+v", st)
	}
	if n := s.notificationCount(t); n != 1 {
		t.Fatalf("one warning, not one per check: %d", n)
	}
	var class, kind, title string
	if err := s.pool.QueryRow(ctx, `SELECT class, kind, title FROM notifications LIMIT 1`).Scan(&class, &kind, &title); err != nil {
		t.Fatal(err)
	}
	if class != "ops" || kind != "credential" || title != "Lifetest refused the credential lifetest_token: credential rejected: Unauthorized" {
		t.Fatalf("notification = %s/%s %q", class, kind, title)
	}
	if v, _ := s.secrets.Open(ctx, ref); v != "minted-1" {
		t.Fatal("a refused token is not rotated — the refusal would only repeat")
	}

	// A new value from a person: the check finds it working, the state is
	// clean, and the old warning does not come back.
	lifetimeState.Lock()
	lifetimeState.rejectAll = false
	lifetimeState.valid["fresh"] = time.Now().Add(300 * 24 * time.Hour)
	lifetimeState.Unlock()
	if err := s.secrets.Put(ctx, s.orgID, "lifetest_token", "fresh"); err != nil {
		t.Fatal(err)
	}
	s.orch.CheckCredentials(ctx)
	st, _ = s.secrets.Lookup(ctx, s.orgID, alice.ID, "lifetest_token")
	if st.RejectedAt != nil || st.WarnedAt != nil || st.ProbeError != "" || st.ProbeIdentity != "bot" {
		t.Fatalf("a fresh token that works: %+v", st)
	}
	if n := s.notificationCount(t); n != 1 {
		t.Fatalf("nothing new to say: %d", n)
	}
}

// TestActionRejectionMarksTheSecret: the hard signal. A run's action is
// refused with a 401; the secret the run was given is marked before the event
// is filed under the action, and the person who owns it hears about it.
func TestActionRejectionMarksTheSecret(t *testing.T) {
	resetLifetimeState()
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/lifetest", map[string]any{"enabled": true}, http.StatusOK)

	agent, err := s.registry.Create(ctx, s.orgID, "dev-1", "Developer", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Developer",
		"ACCESS.md": "- system: lifetest scope: read",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	// The agent's own token, and an org-wide one it never sees: only the
	// one the run was given is marked.
	if err := s.secrets.Put(ctx, s.orgID, "lifetest_token", "org-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.PutAgent(ctx, s.orgID, agent.ID, "lifetest_token", "dead-token"); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Touch the system",
		`[mock:action lifetest/do {}] [mock:result tried]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task ended", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	st, err := s.secrets.Lookup(ctx, s.orgID, agent.ID, "lifetest_token")
	if err != nil || st.AgentID == nil || st.RejectedAt == nil || st.RejectedReason == "" {
		t.Fatalf("the agent's own token is marked: %+v, %v", st, err)
	}
	var orgRejected *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT rejected_at FROM secrets WHERE org_id=$1 AND key='lifetest_token' AND agent_id IS NULL`, s.orgID).Scan(&orgRejected); err != nil {
		t.Fatal(err)
	}
	if orgRejected != nil {
		t.Fatal("the org-wide token was not the one refused")
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events WHERE agent_id=$1 AND kind='credential' AND payload->>'reason'='rejected'`, agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the rejection is on the record once: %d", n)
	}
	if n := s.notificationCount(t); n != 1 {
		t.Fatalf("one warning: %d", n)
	}
}

func (s *stack) notificationCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
