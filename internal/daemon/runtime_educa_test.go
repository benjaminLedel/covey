package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaudeWithEnv is fakeClaude plus a dump of the environment the harness was
// started with — this engine's whole contribution to a run is environment, so
// that is what has to be observable.
func fakeClaudeWithEnv(t *testing.T, script string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args.txt\"\n" +
		"env > \"" + dir + "/env.txt\"\n" + script + "\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func newTestEduca(bin string) *Educa {
	e := &Educa{cc: &ClaudeCode{Binary: bin}, BaseURL: educaDefaultBaseURL}
	e.cc.CredentialHint = e.credentialHint
	return e
}

func TestEducaIsRegistered(t *testing.T) {
	d, ok := Describe("educa-ai")
	if !ok {
		t.Fatal("educa-ai is not registered")
	}
	if !d.NeedsCredential() || len(d.Credentials) != 2 {
		t.Fatalf("credentials: %+v", d.Credentials)
	}
	// Both contracts are the same opaque bearer token and reach the run through
	// the same variable — what they are NOT is the same kind of capacity.
	for _, kind := range []string{CredAPIKey, CredSubscription} {
		c, ok := d.Credential(kind)
		if !ok {
			t.Fatalf("credential %s is missing", kind)
		}
		if c.EnvVar != "ANTHROPIC_AUTH_TOKEN" || c.Path != "" {
			t.Fatalf("%s has to arrive as ANTHROPIC_AUTH_TOKEN: %+v", kind, c)
		}
	}
	if api, _ := d.Credential(CredAPIKey); api.Secret != "educa_api_token" {
		t.Fatalf("the metered token has its own secret name: %+v", api)
	}
	if seat, _ := d.Credential(CredSubscription); seat.Secret != "educa_seat_token" {
		t.Fatalf("the seat has its own secret name: %+v", seat)
	}
	// The credential is picked in declaration order — money on purpose, not by
	// accident.
	if d.Credentials[0].Kind != CredAPIKey {
		t.Fatalf("API token stands before the seat: %+v", d.Credentials)
	}
	if !d.Capabilities.Resume {
		t.Fatal("resume belongs to the harness and survives the change of endpoint")
	}
	if d.Capabilities.SkillsDir != ".claude/skills" {
		t.Fatalf("skills dir: %q", d.Capabilities.SkillsDir)
	}
	if d.AcceptsEffort("max") {
		t.Fatal("educa documents no level above xhigh — it must not be offered")
	}
	if !d.AcceptsEffort("xhigh") || !d.AcceptsEffort("") {
		t.Fatal("xhigh and the engine default have to be accepted")
	}
}

// The endpoint is the whole point of this engine, and clearing the inherited
// Anthropic credentials is not cosmetic: they would otherwise travel to a third
// party's server.
func TestEducaPointsTheHarnessAtItsEndpoint(t *testing.T) {
	bin, dir := fakeClaudeWithEnv(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s1","result":"COVEY_STATUS: {\"status\":\"done\",\"result\":\"fertig\"}","total_cost_usd":1.23,"usage":{"input_tokens":10,"output_tokens":5}}
EOF`)
	e := newTestEduca(bin)

	res, err := e.Run(context.Background(), RunSpec{
		TaskID: "t1", Title: "Aufgabe", Body: "Text",
		Model:   "gpt-oss-120b",
		HomeDir: dir,
		Env:     []string{"ANTHROPIC_AUTH_TOKEN=brokered", "ANTHROPIC_API_KEY=leftover"},
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" {
		t.Fatalf("result: %+v", res)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(raw)
	if !strings.Contains(env, "ANTHROPIC_BASE_URL="+educaDefaultBaseURL) {
		t.Fatalf("base URL missing:\n%s", env)
	}
	if !strings.Contains(env, "ANTHROPIC_AUTH_TOKEN=brokered") {
		t.Fatalf("the brokered token has to reach the harness:\n%s", env)
	}
	if strings.Contains(env, "ANTHROPIC_API_KEY=leftover") {
		t.Fatalf("an inherited Anthropic key must not travel to a third-party endpoint:\n%s", env)
	}
}

// A gateway prices a different contract than the harness knows. Tokens are
// measured and stay; the dollar figure is not ours to inherit.
func TestEducaDoesNotInheritTheHarnessPrice(t *testing.T) {
	bin, dir := fakeClaudeWithEnv(t, `
cat <<'EOF'
{"type":"assistant","message":{"model":"gpt-oss-120b"}}
{"type":"result","subtype":"success","session_id":"s1","result":"fertig","total_cost_usd":4.20,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":900}}
EOF`)
	e := newTestEduca(bin)

	res, err := e.Run(context.Background(), RunSpec{
		TaskID: "t1", Body: "Text", Model: "gpt-oss-120b", HomeDir: dir,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.CostUSD != 0 {
		t.Fatalf("the harness figure prices the wrong contract: %v", res.CostUSD)
	}
	if res.InputTokens != 100 || res.OutputTokens != 50 || res.CacheReadTokens != 900 {
		t.Fatalf("the measured tokens have to stay: %+v", res)
	}
}

// An entered price list is the seam: it prices the same token counts without
// anything else changing.
func TestEducaPricesFromItsOwnListWhenThereIsOne(t *testing.T) {
	e := &Educa{}
	if len(e.Prices()) != 0 {
		t.Fatal("without a known contract the list stays empty")
	}
	priced := PriceList{"some-educa": {Input: 1, Output: 2}}
	usd, ok := priced.Price("some-educa-model")
	if !ok {
		t.Fatal("a model id is matched by prefix")
	}
	if got := usd.Cost(1_000_000, 1_000_000, 0, 0); got != 3 {
		t.Fatalf("cost: %v", got)
	}
}

// The engine carries the ids that were put to work, and nothing else. Being
// listed by the instance is not the same as running: one of educa's listed
// models answers 500 on every request, another cannot hold the prompt.
func TestEducaCarriesOnlyTheModelsThatWereExercised(t *testing.T) {
	d, ok := Describe("educa-ai")
	if !ok {
		t.Fatal("educa-ai is not registered")
	}
	for _, want := range []string{"gpt-oss-120b", "gemma-4-26B-A4B-it", "gemma-4-E4B-it"} {
		if !d.AcceptsModel(want) {
			t.Errorf("%s solved the trial task and has to be selectable", want)
		}
	}
	for _, refuse := range []string{"Qwen-AgentWorld-35B-A3B", "EuroLLM-9B-Instruct", "claude-opus-4-8"} {
		if d.AcceptsModel(refuse) {
			t.Errorf("%s must not be offered", refuse)
		}
	}
	// A declared list means the engine has no default either.
	if d.AcceptsModel("") {
		t.Error("educa-ai has no default model — the empty id has to be refused")
	}
	// The engines in front of a single provider stay unrestricted: their model
	// list is the provider's to publish, and pinning it here would age badly.
	for _, engine := range []string{"claude-code", "codex"} {
		e, _ := Describe(engine)
		if !e.AcceptsModel("") || !e.AcceptsModel("whatever-the-provider-ships-next") {
			t.Errorf("%s declares no models and must accept anything", engine)
		}
	}
}

// No model, no run — the second door behind the validation, and the message has
// to name the fix rather than the symptom.
func TestEducaRefusesToRunWithAModelItDoesNotCarry(t *testing.T) {
	bin, dir := fakeClaudeWithEnv(t, `echo '{"type":"result","result":"should not happen"}'`)
	e := newTestEduca(bin)

	for _, model := range []string{"", "EuroLLM-9B-Instruct"} {
		res, err := e.Run(context.Background(),
			RunSpec{TaskID: "t1", Body: "Text", Model: model, HomeDir: dir},
			func(string, json.RawMessage) {})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "failed" || !strings.Contains(res.Error, "gpt-oss-120b") {
			t.Fatalf("model %q: the refusal has to name what the engine does carry: %+v", model, res)
		}
		if _, err := os.Stat(filepath.Join(dir, "args.txt")); err == nil {
			t.Fatalf("model %q: the harness must not be started at all", model)
		}
	}
}

// Wrong advice costs more time than none: nobody fixes an educa token by
// running `claude setup-token`.
func TestEducaWordsCredentialFailuresInItsOwnTerms(t *testing.T) {
	e := &Educa{BaseURL: educaDefaultBaseURL}

	hint := e.credentialHint("API Error: 401 Unauthorized Access")
	if !strings.Contains(hint, "educa_api_token") || !strings.Contains(hint, "educa_seat_token") {
		t.Fatalf("the hint has to name the engine's own secrets: %q", hint)
	}
	if strings.Contains(hint, "setup-token") {
		t.Fatalf("Anthropic's advice does not apply here: %q", hint)
	}
	if h := e.credentialHint("model 'nope' not found"); !strings.Contains(h, "/v1/models") {
		t.Fatalf("an unknown model has its own hint: %q", h)
	}
	// Anything else stays as it is — a rewrite on the wrong error hides the real one.
	if h := e.credentialHint("disk full"); h != "" {
		t.Fatalf("unrelated errors must not be rewritten: %q", h)
	}
}

// The harness reports utilisation by asking ANTHROPIC's account endpoint. Behind
// a gateway that answers about the wrong account, so this engine must not
// declare the capability — which is why it holds its ClaudeCode instead of
// embedding it.
func TestEducaReportsNoUtilisation(t *testing.T) {
	var rt Runtime = NewEduca()
	if _, ok := rt.(UsageReporter); ok {
		t.Fatal("educa-ai has no utilisation source and must not pretend to have one")
	}
	// The counter-check, so the test fails if the harness ever loses it.
	var cc Runtime = NewClaudeCode()
	if _, ok := cc.(UsageReporter); !ok {
		t.Fatal("claude-code does report utilisation")
	}
}

func TestEducaBaseURLOverride(t *testing.T) {
	t.Setenv("COVEY_EDUCA_BASE_URL", "https://ai.intern.example/")
	if got := NewEduca().BaseURL; got != "https://ai.intern.example" {
		t.Fatalf("an on-premise instance is reached by override, without a trailing slash: %q", got)
	}
}
