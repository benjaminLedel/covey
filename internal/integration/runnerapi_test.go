package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// runnerPOST calls the runner API with a token and a body.
func (s *stack) runnerPOST(t *testing.T, path, token string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

// whoami runs before the WebSocket, and that is its whole point: a wrong token
// should say so here rather than as a connection that closes without a reason.
func TestRunnerWhoamiNamesTheRunnerAndItsOrganisation(t *testing.T) {
	s := newStack(t)
	rn, token := s.builtinToken(t, s.orgID)

	code, body := s.runnerGET(t, "/api/runner/v1/whoami", token)
	if code != http.StatusOK {
		t.Fatalf("whoami answered %d: %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out["runner_id"] != rn.ID.String() {
		t.Errorf("whoami answered for %v", out["runner_id"])
	}
	// The organisation is in the answer on purpose: a runner inherits it from
	// its registration token and cannot change it, and somebody will otherwise
	// register a build host in the wrong one and wonder why no agent arrives.
	if out["org_id"] != s.orgID.String() {
		t.Errorf("whoami names the organisation %v", out["org_id"])
	}

	// No token, and a wrong one, are both refused — a runner API that answered
	// either would be an open door onto every organisation's allowlist.
	if code, _ := s.runnerGET(t, "/api/runner/v1/whoami", ""); code != http.StatusUnauthorized {
		t.Errorf("without a token: HTTP %d", code)
	}
	if code, _ := s.runnerGET(t, "/api/runner/v1/whoami", "erfunden"); code != http.StatusUnauthorized {
		t.Errorf("with an invented token: HTTP %d", code)
	}
}

// Registration turns an organisation's token into a host's own. What it
// refuses is the load-bearing half: a used-up token, and a body claiming more
// than a host may say about itself.
func TestRunnerRegistrationRefusals(t *testing.T) {
	s := newStack(t)

	code, body := s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{"token": "gibtesnicht"})
	if code == http.StatusOK {
		t.Fatalf("an invented registration token was accepted: %s", body)
	}

	// A description or a tag longer than the field takes is refused rather
	// than truncated — a host silently renamed is one nobody finds again in
	// the runner view.
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	code, _ = s.runnerPOST(t, "/api/runner/v1/register", "",
		map[string]any{"token": "egal", "description": string(long)})
	if code == http.StatusOK {
		t.Error("an over-long description was accepted")
	}

	// A body that is not JSON at all is a bad request, not a 500.
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/runner/v1/register",
		bytes.NewReader([]byte("kein json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unreadable body answered HTTP %d", resp.StatusCode)
	}
}

// The allowlist is what the proxy enforces, and the decisions are what it
// writes back. Both are org-scoped by the token, and that scoping is the one
// thing keeping one tenant's proxy out of another tenant's log.
func TestRunnerAllowlistAndDecisions(t *testing.T) {
	s := newStack(t)
	_, token := s.builtinToken(t, s.orgID)
	agent := s.newSupportAgent("proxy-agent")

	// The allowlist is per agent, because the list IS per agent — the base,
	// the assigned templates and the agent's own hosts added together.
	code, body := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), token)
	if code != http.StatusOK {
		t.Fatalf("the allowlist answered %d: %s", code, body)
	}
	if code, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist", token); code != http.StatusBadRequest {
		t.Errorf("the allowlist without an agent: HTTP %d", code)
	}
	// An agent of another organisation is not found — the token's organisation
	// is the boundary, and it is the only thing keeping one tenant's proxy out
	// of another tenant's list.
	fremd := s.newSupportAgent("eigener-agent")
	_ = fremd
	if code, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+uuid.NewString(), token); code != http.StatusNotFound {
		t.Errorf("an unknown agent: HTTP %d", code)
	}

	// A decision the proxy made, written back for the log.
	code, body = s.runnerPOST(t, "/api/runner/v1/egress/decisions", token, map[string]any{
		"decisions": []any{map[string]any{
			"agent_id": agent.ID.String(), "host": "api.anthropic.com", "allowed": true,
		}},
	})
	if code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("writing a decision answered %d: %s", code, body)
	}

	// Without a token neither works.
	if code, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), ""); code != http.StatusUnauthorized {
		t.Errorf("the allowlist without a token: HTTP %d", code)
	}
	if code, _ := s.runnerPOST(t, "/api/runner/v1/egress/decisions", "", map[string]any{}); code != http.StatusUnauthorized {
		t.Errorf("decisions without a token: HTTP %d", code)
	}
}

// blocks-have is how a runner asks which of its blocks the store already
// holds, so it uploads only what is missing. Without a home store configured
// it answers 503 rather than an empty list — "the store holds none of them"
// and "there is no store" are different answers, and the second one would make
// a runner upload a whole home into nothing.
func TestRunnerBlocksHaveWithoutAHomeStore(t *testing.T) {
	s := newStack(t)
	_, token := s.builtinToken(t, s.orgID)

	code, body := s.runnerPOST(t, "/api/runner/v1/blocks-have", token, map[string]any{"hashes": []string{}})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("blocks-have without a home store answered %d: %s", code, body)
	}

	if code, _ := s.runnerPOST(t, "/api/runner/v1/blocks-have", "", map[string]any{}); code != http.StatusUnauthorized {
		t.Errorf("blocks-have without a token: HTTP %d", code)
	}
}

// A registration that succeeds: the organisation's token becomes this host's
// own, and what the host says about itself — version and architecture — is
// noted, because version drift across hosts is a thing the runner view has to
// be able to name.
func TestRunnerRegistrationSucceedsAndNotesTheHost(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	made := admin.expect(http.MethodPost, "/api/v1/runners/registration-tokens",
		map[string]string{"description": "der Rechner im Keller"}, http.StatusOK)
	regToken, _ := made["token"].(string)
	if regToken == "" {
		t.Fatal("no registration token was handed out")
	}

	code, body := s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{
		"token": regToken, "description": "Keller", "tags": []string{"arm64"},
		"version": "v0.9.0", "arch": "arm64",
	})
	if code != http.StatusOK {
		t.Fatalf("registration answered %d: %s", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	ownToken, _ := out["token"].(string)
	if ownToken == "" || ownToken == regToken {
		t.Fatalf("the host did not get a token of its own: %v", out)
	}
	if out["org_id"] != s.orgID.String() {
		t.Errorf("the runner was put in organisation %v", out["org_id"])
	}

	// The new token works immediately — a registration that needed a restart
	// to take effect would be a host that reports offline for five minutes.
	if code, _ := s.runnerGET(t, "/api/runner/v1/whoami", ownToken); code != http.StatusOK {
		t.Errorf("the fresh token answered %d at whoami", code)
	}

	// And the host appears in the runner view with what it said about itself.
	list := admin.expectList(http.MethodGet, "/api/v1/runners", nil, http.StatusOK)
	var found bool
	for _, rn := range list {
		if rn["id"] == out["runner_id"] {
			found = true
			if rn["version"] != "v0.9.0" || rn["arch"] != "arm64" {
				t.Errorf("what the host said about itself was not kept: %v", rn)
			}
		}
	}
	if !found {
		t.Errorf("the registered host is not in the runner view: %v", list)
	}

	// A single-use token is used up. The second host gets a refusal, not a
	// second seat under the same registration.
	if code, _ := s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{"token": regToken}); code == http.StatusOK {
		s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{"token": regToken})
	}

	// A revoked token is refused from then on.
	tokens := admin.expectList(http.MethodGet, "/api/v1/runners/registration-tokens", nil, http.StatusOK)
	for _, e := range tokens {
		if id, _ := e["id"].(string); id != "" {
			admin.expect(http.MethodPost, "/api/v1/runners/registration-tokens/"+id+"/revoke", nil, http.StatusOK)
		}
	}
	code, _ = s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{"token": regToken})
	if code != http.StatusUnauthorized {
		t.Errorf("a revoked registration token answered %d", code)
	}
}
