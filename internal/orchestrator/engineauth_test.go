package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/engines"
	"covey/internal/secrets"
)

// Only Resolve is answered; anything else the orchestrator might one day ask a
// secret store is a nil-interface panic in the test, which is a louder answer
// than a stub that quietly returns nothing.
type oneSecretStore struct {
	secrets.Store
	value string
	err   error
}

func (s oneSecretStore) Resolve(context.Context, uuid.UUID, uuid.UUID, string) (string, error) {
	return s.value, s.err
}

// The catalogue the question is asked of: one engine whose artefact sits behind a
// login that `sevencode_api_token` opens, one that needs nothing.
func catalogueWith(t *testing.T, body string) *engines.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return engines.NewSource(srv.URL+"/engine-catalog.json", nil, nil)
}

// One engine whose artefact sits behind a login that `sevencode_api_token`
// opens, one that needs nothing.
func authedCatalogue() string {
	digest := func(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }
	return `{"schema":1,"engines":[{"name":"sevencode","versions":[{
		"version":"1.0.27","kind":"file","binary":"sevencode",
		"url":"https://host/cli/latest","integrity":"` + digest('b') + `",
		"auth_header":"Authorization","auth_secret":"sevencode_api_token"}]},
	{"name":"plain","versions":[{"version":"1.0.0","kind":"file","binary":"plain",
		"url":"https://host/plain","integrity":"` + digest('a') + `"}]}]}`
}

// The whole point of #289: the secret that opens an engine's artefact is the
// agent's, read at the moment its sandbox starts, so that nothing has to be
// replicated onto the hosts that could carry it.
func TestEngineDownloadSecretComesFromTheAgent(t *testing.T) {
	org, agent := uuid.New(), uuid.New()
	on := func(secret string) *Orchestrator {
		o := &Orchestrator{}
		o.Engines = catalogueWith(t, authedCatalogue())
		o.Secrets = oneSecretStore{value: secret}
		return o
	}
	who := agents.Agent{ID: agent, OrgID: org, Runtime: "sevencode"}

	if got := on("sc-der-agent").engineDownloadSecret(t.Context(), who); got != "sc-der-agent" {
		t.Fatalf("the start carries what the store holds for this agent, got %q", got)
	}

	// An agent that has no such secret is not a failure and not a warning here:
	// the download refuses with both names, and this is the side that cannot tell
	// a missing secret from an engine that needs none.
	if got := on("").engineDownloadSecret(t.Context(), who); got != "" {
		t.Fatalf("nothing to offer has to stay empty rather than become an error: %q", got)
	}
	if got := on("sc-x").engineDownloadSecret(t.Context(), agents.Agent{ID: agent, OrgID: org, Runtime: "plain"}); got != "" {
		t.Fatalf("an engine behind no login is offered nothing: %q", got)
	}
	if got := on("sc-x").engineDownloadSecret(t.Context(), agents.Agent{ID: agent, OrgID: org}); got != "" {
		t.Fatalf("an agent with no engine is asked nothing: %q", got)
	}

	// No catalogue wired at all — the state of an installation that never set a
	// URL. Its hosts keep the old way, and this side stays out of the way.
	bare := &Orchestrator{}
	bare.Secrets = oneSecretStore{value: "sc-x"}
	if got := bare.engineDownloadSecret(t.Context(), who); got != "" {
		t.Fatalf("without a catalogue there is nothing to ask: %q", got)
	}
}
