package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/egress"
	"covey/internal/runner"
)

// The egress proxy used to read its allowlist from Postgres itself, which meant
// the database URL had to be handed to every host that runs sandboxes. It now
// asks the control plane with its runner token (spec/16, "Trust boundary").
//
// What these tests hold onto is the boundary, not the plumbing: a runner speaks
// for exactly one organisation, and everything it can ask for or write ends at
// that organisation. If that ever slips, one tenant's proxy could read another
// tenant's allowlist and write into their decision log — and neither would show
// up anywhere until someone went looking.

// runnerGET calls the runner API with a token.
func (s *stack) runnerGET(t *testing.T, path, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.http.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
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

// builtinToken gives an organisation's built-in runner a fresh token, the way
// the control plane does at every start.
func (s *stack) builtinToken(t *testing.T, orgID uuid.UUID) (runner.Runner, string) {
	t.Helper()
	ctx := context.Background()
	rn, err := s.runners.EnsureBuiltin(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := runner.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runners.SetTokenHash(ctx, rn.ID, runner.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	return rn, token
}

func TestRunnerAllowlistIsScopedToItsOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("runner-egress-agent")

	if _, err := s.egress.AddAgentHost(ctx, agent.ID, "eigener-host.example.com", ""); err != nil {
		t.Fatal(err)
	}
	// The per-sandbox token: the proxy checks it locally, so its hash has to
	// travel with the answer — otherwise the proxy would have to ask back on
	// every single request.
	sandboxToken := "sandbox-token-fuer-den-test"
	if err := s.egress.SetAgentToken(ctx, agent.ID, egress.HashToken(sandboxToken)); err != nil {
		t.Fatal(err)
	}

	_, token := s.builtinToken(t, s.orgID)

	status, body := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), token)
	if status != http.StatusOK {
		t.Fatalf("allowlist: status %d: %s", status, body)
	}
	var answer egress.AllowlistResponse
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.TokenHash != egress.HashToken(sandboxToken) {
		t.Errorf("the per-sandbox token hash has to travel along, got %q", answer.TokenHash)
	}
	found := false
	for _, p := range answer.Patterns {
		if p == "eigener-host.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("the agent's own host is missing from the allowlist: %v", answer.Patterns)
	}

	// Without a token, and with a wrong one: 401. A runner that has none is not
	// a runner.
	if status, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), ""); status != http.StatusUnauthorized {
		t.Errorf("without a token the answer has to be 401, got %d", status)
	}
	if status, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), "falsch"); status != http.StatusUnauthorized {
		t.Errorf("with a wrong token the answer has to be 401, got %d", status)
	}

	// A runner of a foreign organisation: to it, this agent does not exist. 404
	// and not 403 — the difference between "not there" and "not yours" is one it
	// has no business learning.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Runner')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	_, fremdesToken := s.builtinToken(t, fremdeOrg)
	if status, _ := s.runnerGET(t, "/api/runner/v1/egress/allowlist?agent="+agent.ID.String(), fremdesToken); status != http.StatusNotFound {
		t.Errorf("a foreign runner must not get the allowlist, got status %d", status)
	}
}

func TestRunnerDecisionsStayInsideTheOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("runner-log-agent")

	// A second organisation with an agent of its own — the one whose records
	// must stay untouched.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Log')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremderAgent, err := s.registry.Create(ctx, fremdeOrg, "fremd-agent", "Fremd", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, token := s.builtinToken(t, s.orgID)
	body, _ := json.Marshal(egress.DecisionsRequest{Decisions: []egress.Decision{
		{AgentID: agent.ID, Host: "erlaubt.example.com", Method: "GET", Allowed: true},
		{AgentID: agent.ID, Host: "geblockt.example.com", Method: "CONNECT", Allowed: false},
		// The one that matters: our runner reports a decision about a foreign
		// agent. It must not land anywhere.
		{AgentID: fremderAgent.ID, Host: "untergeschoben.example.com", Method: "GET", Allowed: true},
	}})
	req, err := http.NewRequest(http.MethodPost, s.http.URL+"/api/runner/v1/egress/decisions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decisions: status %d", resp.StatusCode)
	}

	eintraege, err := s.egress.ListLog(ctx, s.orgID, uuid.Nil, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(eintraege) != 2 {
		t.Fatalf("the organisation's own two decisions belong in the log, got %d", len(eintraege))
	}

	var fremde int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM egress_log WHERE agent_id = $1`, fremderAgent.ID).Scan(&fremde); err != nil {
		t.Fatal(err)
	}
	if fremde != 0 {
		t.Errorf("a runner must not be able to write into a foreign organisation's log: %d entries", fremde)
	}
}
