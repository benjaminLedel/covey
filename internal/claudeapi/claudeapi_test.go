package claudeapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"covey/internal/secrets"
)

// captured is what the fake API saw.
type captured struct {
	header http.Header
	body   map[string]any
}

func serve(t *testing.T, status int, answer string) *captured {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })
	return got
}

// An API key goes into x-api-key, and nothing else comes along: the oauth beta
// header and the Claude Code identity block belong to the other mechanism.
func TestMessagesWithAnAPIKey(t *testing.T) {
	got := serve(t, http.StatusOK, `{"content":[{"type":"text","text":"Antwort"}]}`)

	out, err := Messages(context.Background(), "sk-ant-test", false,
		Call{Model: "claude-opus-5", MaxTokens: 64},
		"du bist knapp", []Message{{Role: "user", Content: "frage"}})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Antwort" {
		t.Errorf("answer = %q", out)
	}
	if got.header.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("x-api-key = %q", got.header.Get("x-api-key"))
	}
	if got.header.Get("Authorization") != "" {
		t.Error("an API key was additionally sent as a Bearer token")
	}
	if got.header.Get("anthropic-beta") != "" {
		t.Error("the oauth beta header went out with an API key")
	}
	if got.header.Get("anthropic-version") == "" {
		t.Error("anthropic-version is missing")
	}
	sys, _ := got.body["system"].([]any)
	if len(sys) != 1 {
		t.Fatalf("system has %d blocks, expected only the caller's own", len(sys))
	}
}

// For an OAuth token Anthropic requires the Claude Code identity block as the
// FIRST system segment. That order is the subtle part this package exists for.
func TestMessagesWithASubscriptionToken(t *testing.T) {
	got := serve(t, http.StatusOK, `{"content":[{"type":"text","text":"ok"}]}`)

	if _, err := Messages(context.Background(), "oauth-token", true,
		Call{Model: "claude-opus-5", MaxTokens: 64},
		"du bist knapp", []Message{{Role: "user", Content: "frage"}}); err != nil {
		t.Fatal(err)
	}
	if got.header.Get("Authorization") != "Bearer oauth-token" {
		t.Errorf("Authorization = %q", got.header.Get("Authorization"))
	}
	if got.header.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q", got.header.Get("anthropic-beta"))
	}
	if got.header.Get("x-api-key") != "" {
		t.Error("the token additionally went out as an API key")
	}
	sys, _ := got.body["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system has %d blocks, expected the identity block plus the caller's", len(sys))
	}
	first, _ := sys[0].(map[string]any)
	if text, _ := first["text"].(string); text != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Errorf("the identity block is not first: %q", text)
	}
}

func TestMessagesCarriesEffortAndThinking(t *testing.T) {
	got := serve(t, http.StatusOK, `{"content":[]}`)
	if _, err := Messages(context.Background(), "k", false,
		Call{Model: "m", MaxTokens: 8, Effort: "low", NoThinking: true}, "s", nil); err != nil {
		t.Fatal(err)
	}
	oc, _ := got.body["output_config"].(map[string]any)
	if oc == nil || oc["effort"] != "low" {
		t.Errorf("effort did not reach the request: %v", got.body["output_config"])
	}
	th, _ := got.body["thinking"].(map[string]any)
	if th == nil || th["type"] != "disabled" {
		t.Errorf("thinking was not switched off: %v", got.body["thinking"])
	}

	// Without either, neither block goes out — an empty effort is the model's
	// default, not a value.
	got = serve(t, http.StatusOK, `{"content":[]}`)
	if _, err := Messages(context.Background(), "k", false, Call{Model: "m", MaxTokens: 8}, "s", nil); err != nil {
		t.Fatal(err)
	}
	if _, set := got.body["output_config"]; set {
		t.Error("output_config went out although no effort was asked for")
	}
	if _, set := got.body["thinking"]; set {
		t.Error("thinking went out although nothing was asked for")
	}
}

// Several text blocks are one answer; anything that is not text (a tool call,
// a thinking block) does not belong in it.
func TestMessagesJoinsTextBlocksOnly(t *testing.T) {
	serve(t, http.StatusOK, `{"content":[
	  {"type":"thinking","text":"nachgedacht"},
	  {"type":"text","text":"erst "},
	  {"type":"text","text":"dann"}]}`)
	out, err := Messages(context.Background(), "k", false, Call{Model: "m", MaxTokens: 8}, "s", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "erst dann" {
		t.Errorf("answer = %q", out)
	}
}

// The API's own message is what a caller can act on; "HTTP 400" is not.
func TestMessagesPassesTheAPIErrorOn(t *testing.T) {
	serve(t, http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"max_tokens too large"}}`)
	_, err := Messages(context.Background(), "k", false, Call{Model: "m", MaxTokens: 1 << 30}, "s", nil)
	if err == nil || err.Error() != "max_tokens too large" {
		t.Fatalf("err = %v, expected the API's message", err)
	}
}

func TestMessagesFallsBackToTheStatusLine(t *testing.T) {
	serve(t, http.StatusBadGateway, `<html>der Proxy war es</html>`)
	_, err := Messages(context.Background(), "k", false, Call{Model: "m", MaxTokens: 8}, "s", nil)
	if err == nil {
		t.Fatal("a 502 without a JSON body was taken as success")
	}
	if got := err.Error(); got[:5] != "HTTP " {
		t.Errorf("err = %q, expected an HTTP status", got)
	}
}

// fakeSecrets answers Get and nothing else — the rest of the interface is not
// reached from here, and a fake that implemented it would say otherwise.
type fakeSecrets struct {
	secrets.Store
	values map[string]string
}

func (f fakeSecrets) Get(_ context.Context, _ uuid.UUID, key string) (string, error) {
	v, ok := f.values[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

// The order lives in one place, because the config copilot and the dream must
// arrive at the same credential.
func TestResolveOrgPrefersTheAPIKey(t *testing.T) {
	org := uuid.New()
	store := fakeSecrets{values: map[string]string{
		"anthropic_api_key":       " sk-ant-key ",
		"claude_code_oauth_token": "oauth",
	}}
	cred, oauth, ok := ResolveOrg(context.Background(), store, org)
	if !ok || oauth || cred != "sk-ant-key" {
		t.Fatalf("ResolveOrg = %q, oauth=%v, ok=%v", cred, oauth, ok)
	}
}

func TestResolveOrgFallsBackToTheSubscriptionToken(t *testing.T) {
	store := fakeSecrets{values: map[string]string{
		"anthropic_api_key":       "   ", // present but empty
		"claude_code_oauth_token": "oauth-token",
	}}
	cred, oauth, ok := ResolveOrg(context.Background(), store, uuid.New())
	if !ok || !oauth || cred != "oauth-token" {
		t.Fatalf("ResolveOrg = %q, oauth=%v, ok=%v", cred, oauth, ok)
	}
}

func TestResolveOrgWithoutAnyCredential(t *testing.T) {
	store := fakeSecrets{values: map[string]string{}}
	if cred, oauth, ok := ResolveOrg(context.Background(), store, uuid.New()); ok || oauth || cred != "" {
		t.Fatalf("ResolveOrg = %q, oauth=%v, ok=%v — expected nothing", cred, oauth, ok)
	}
}
