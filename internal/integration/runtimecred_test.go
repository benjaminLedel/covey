package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/observability"
)

// credEvents reads the credential entries of the recording — the only place
// from which "which token did this run burn" is answerable. The value is never
// in there, and that is exactly what these tests also check.
func credEvents(t *testing.T, s *stack, agentID uuid.UUID) []map[string]any {
	t.Helper()
	raw, err := s.obs.OrgEventsByKind(context.Background(), s.orgID, observability.KindCredential, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range raw {
		if e.AgentID != agentID {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["system"] == "anthropic" {
			out = append(out, p)
		}
	}
	return out
}

// wakeWithTask starts a session. A wake alone is not enough: without an open
// task runAgent returns before the sandbox exists — and the credential is
// pushed only once the daemon link is up.
func wakeWithTask(t *testing.T, s *stack, agentID uuid.UUID, title string) {
	t.Helper()
	if _, err := s.backlog.Create(context.Background(), s.orgID, agentID, title, "credential test", "manual", 1); err != nil {
		t.Fatal(err)
	}
	s.orch.EnsureRunning(agentID)
}

func lastCredEvent(t *testing.T, s *stack, agentID uuid.UUID) map[string]any {
	t.Helper()
	var got map[string]any
	waitFor(t, "credential event for "+agentID.String(), 15*time.Second, func() bool {
		ev := credEvents(t, s, agentID)
		if len(ev) == 0 {
			return false
		}
		got = ev[0] // OrgEventsByKind liefert neueste zuerst
		return true
	})
	return got
}

// An organization holds several subscription tokens side by side and each agent
// is pinned to its own. Before, both names were hardcoded — so an organization
// held exactly one of each, and giving a second agent a different token meant
// storing the same name twice, per agent.
func TestRuntimeCredentialProAgent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	alice := s.newSupportAgent("cred-alice")
	bob := s.newSupportAgent("cred-bob")

	for _, k := range []string{"claude_code_oauth_token_team_a", "claude_code_oauth_token_team_b"} {
		if err := s.secrets.Put(ctx, s.orgID, k, "sk-ant-oat01-"+k); err != nil {
			t.Fatal(err)
		}
	}

	// Pinning also assigns: without the grant the secret would not reach the
	// agent, and the trap would spring only at the next wake.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+alice.ID.String()+"/runtime-credential",
		map[string]string{"key": "claude_code_oauth_token_team_a"}, http.StatusOK)
	res := admin.expect(http.MethodPatch, "/api/v1/agents/"+bob.ID.String()+"/runtime-credential",
		map[string]string{"key": "claude_code_oauth_token_team_b"}, http.StatusOK)
	if res["assigned"] != true {
		t.Errorf("pinning an org secret must assign it: %+v", res)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "claude_code_oauth_token_team_b"); err != nil || v == "" {
		t.Fatalf("bob does not reach his pinned secret: %q %v", v, err)
	}

	// Each agent gets its own credential on waking.
	wakeWithTask(t, s, alice.ID, "alice arbeitet")
	wakeWithTask(t, s, bob.ID, "bob arbeitet")

	for _, c := range []struct {
		id       uuid.UUID
		wantKey  string
		wantName string
	}{
		{alice.ID, "claude_code_oauth_token_team_a", "alice"},
		{bob.ID, "claude_code_oauth_token_team_b", "bob"},
	} {
		ev := lastCredEvent(t, s, c.id)
		if ev["granted"] != true || ev["key"] != c.wantKey {
			t.Errorf("%s got the wrong credential: %+v", c.wantName, ev)
		}
		if ev["env_var"] != "CLAUDE_CODE_OAUTH_TOKEN" {
			t.Errorf("%s: the name decides the env var, not the token prefix: %+v", c.wantName, ev)
		}
		// The value belongs in the sandbox, never in the recording.
		raw, _ := json.Marshal(ev)
		if strings.Contains(string(raw), "sk-ant-oat01-") {
			t.Errorf("%s: the token value landed in the recording: %s", c.wantName, raw)
		}
	}

	// The read endpoint names the effective credential — this is what the
	// agent's owner sees without being allowed to change it.
	view := admin.expect(http.MethodGet, "/api/v1/agents/"+alice.ID.String()+"/runtime-credential", nil, http.StatusOK)
	if view["pinned"] != "claude_code_oauth_token_team_a" || view["resolvable"] != true ||
		view["kind"] != "oauth" || view["env_var"] != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("read view: %+v", view)
	}
}

// A pin that no longer resolves is stated, not worked around: falling back to
// the next best credential would bill an account nobody chose.
func TestRuntimeCredentialToterPinFaelltNichtZurueck(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("cred-dead")

	// A classic credential lies ready — exactly what must NOT be reached for.
	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-api03-fallback"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "anthropic_api_key", agent.ID); err != nil {
		t.Fatal(err)
	}
	// The pin points at a secret that was withdrawn afterwards.
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token_gone", "sk-ant-oat01-gone"); err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": "claude_code_oauth_token_gone"}, http.StatusOK)

	// Deleting is refused as long as the agent points at it.
	admin.expect(http.MethodDelete, "/api/v1/secrets/claude_code_oauth_token_gone", nil, http.StatusConflict)
	admin.expect(http.MethodDelete,
		"/api/v1/secrets/claude_code_oauth_token_gone/agents/"+agent.ID.String(), nil, http.StatusConflict)

	// Withdrawn past the guard — as it would happen through the store.
	if err := s.secrets.Unassign(ctx, s.orgID, "claude_code_oauth_token_gone", agent.ID); err != nil {
		t.Fatal(err)
	}

	wakeWithTask(t, s, agent.ID, "toter Pin")
	ev := lastCredEvent(t, s, agent.ID)
	if ev["granted"] != false {
		t.Fatalf("a dead pin must not be granted: %+v", ev)
	}
	if ev["key"] != "claude_code_oauth_token_gone" {
		t.Errorf("the refusal has to name the pin: %+v", ev)
	}

	view := admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential", nil, http.StatusOK)
	if view["resolvable"] != false || view["pinned"] != "claude_code_oauth_token_gone" {
		t.Errorf("the read view has to show the dead pin: %+v", view)
	}
}

// Without a pin nothing changes: an installation with only the classic names
// behaves exactly as before.
func TestRuntimeCredentialStandardreihenfolge(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("cred-classic")

	for _, k := range []string{"anthropic_api_key", "claude_code_oauth_token"} {
		if err := s.secrets.Put(ctx, s.orgID, k, "sk-value-"+k); err != nil {
			t.Fatal(err)
		}
		if err := s.secrets.Assign(ctx, s.orgID, k, agent.ID); err != nil {
			t.Fatal(err)
		}
	}
	wakeWithTask(t, s, agent.ID, "klassische Reihenfolge")

	ev := lastCredEvent(t, s, agent.ID)
	if ev["granted"] != true || ev["key"] != "anthropic_api_key" || ev["env_var"] != "ANTHROPIC_API_KEY" {
		t.Fatalf("the API key has to keep winning the fallback: %+v", ev)
	}
}

// The pin decides which account an agent bills — that is a security decision,
// not a manager's, because it grants the secret along the way.
func TestRuntimeCredentialNurSicherheitsrollen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("cred-rbac")
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token_x", "sk-ant-oat01-x"); err != nil {
		t.Fatal(err)
	}

	hash, _ := identbuiltin.HashPassword("owner-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'owner@test.local','Owner',$3,'agent_owner')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	owner := login(t, s, "owner@test.local", "owner-passwort")

	// Reading yes — an owner may see which token their agent burns.
	owner.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential", nil, http.StatusOK)
	// Setting no — otherwise the agent_owner could route any org credential
	// past the securityRoles gate on secret assignment.
	owner.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": "claude_code_oauth_token_x"}, http.StatusForbidden)

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": "claude_code_oauth_token_x"}, http.StatusOK)

	// A name that is not a runtime credential is refused, and one that is not
	// stored anywhere too — a pin into the void would only fail at the next wake.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": "zammad_token"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": "anthropic_api_key_gibt_es_nicht"}, http.StatusConflict)

	// Unpinning goes back to the fallback order and is always allowed.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
		map[string]string{"key": ""}, http.StatusOK)
	a, err := s.registry.Get(ctx, agent.ID)
	if err != nil || a.RuntimeCredentialKey != "" {
		t.Fatalf("unpin: %q %v", a.RuntimeCredentialKey, err)
	}
}

// Re-pinning must not leave the old grant behind. It did at first: every switch
// added one more assignment, so an agent ended up reaching three tokens while
// using one — and the two it did not use could not be withdrawn in an obvious
// way. What an agent does not use it should not reach either.
func TestRuntimeCredentialUmpinnenNimmtZuweisungMit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("cred-umpinnen")

	for _, k := range []string{"claude_code_oauth_token_a", "claude_code_oauth_token_b"} {
		if err := s.secrets.Put(ctx, s.orgID, k, "sk-ant-oat01-"+k); err != nil {
			t.Fatal(err)
		}
	}
	pin := func(key string) map[string]any {
		return admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/runtime-credential",
			map[string]string{"key": key}, http.StatusOK)
	}
	reaches := func(key string) bool {
		t.Helper()
		keys, err := s.secrets.ResolvableKeys(ctx, s.orgID, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if k.Key == key {
				return true
			}
		}
		return false
	}

	pin("claude_code_oauth_token_a")
	if !reaches("claude_code_oauth_token_a") {
		t.Fatal("pinning has to grant the secret")
	}

	res := pin("claude_code_oauth_token_b")
	if res["unassigned"] != "claude_code_oauth_token_a" {
		t.Errorf("the answer has to name the withdrawal: %+v", res)
	}
	if reaches("claude_code_oauth_token_a") {
		t.Error("the old credential still reaches the agent after re-pinning")
	}
	if !reaches("claude_code_oauth_token_b") {
		t.Error("the new credential does not reach the agent")
	}

	// Unpinning is the exception: the fallback order needs something assigned
	// to find, so the grant stays.
	res = pin("")
	if res["unassigned"] != "" {
		t.Errorf("unpinning must not withdraw anything: %+v", res)
	}
	if !reaches("claude_code_oauth_token_b") {
		t.Fatal("after unpinning the agent has nothing left to fall back on")
	}
}

// ResolvableKeys is the basis for the choice — it must show exactly what
// Resolve would find, and nothing of a foreign agent.
func TestResolvableKeys(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	alice := s.newSupportAgent("rk-alice")
	bob := s.newSupportAgent("rk-bob")

	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token_org", "org"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key_unassigned", "nobody"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "claude_code_oauth_token_org", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.PutAgent(ctx, s.orgID, alice.ID, "anthropic_api_key_own", "own"); err != nil {
		t.Fatal(err)
	}
	// Owned AND assigned under the same name: one name, and the owned one.
	if err := s.secrets.PutAgent(ctx, s.orgID, alice.ID, "claude_code_oauth_token_org", "shadow"); err != nil {
		t.Fatal(err)
	}

	keys, err := s.secrets.ResolvableKeys(ctx, s.orgID, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		if got[k.Key] {
			t.Errorf("%s listed twice", k.Key)
		}
		got[k.Key] = k.Owned
	}
	if len(got) != 2 || !got["anthropic_api_key_own"] || !got["claude_code_oauth_token_org"] {
		t.Fatalf("alice: %+v", got)
	}
	if _, ok := got["anthropic_api_key_unassigned"]; ok {
		t.Error("an unassigned org secret reaches nobody")
	}

	bobKeys, err := s.secrets.ResolvableKeys(ctx, s.orgID, bob.ID)
	if err != nil || len(bobKeys) != 0 {
		t.Fatalf("bob must not see any of alice's keys: %+v %v", bobKeys, err)
	}
}
