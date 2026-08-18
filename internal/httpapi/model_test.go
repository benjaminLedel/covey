package httpapi

import (
	"strings"
	"testing"

	"covey/internal/daemon"
)

// The model is validated against the ENGINE, not against a list in this layer.
// The rule is the same as for the reasoning effort, and so is the reason: an
// engine in front of one provider passes the provider's ids through, an engine
// in front of a gateway knows which ids it actually carries — and a model that
// is merely LISTED by an instance is not one that runs.
func TestCheckModelAsksTheEngine(t *testing.T) {
	for _, tc := range []struct {
		runtime, model string
		wantOK         bool
	}{
		{"claude-code", "", true},                      // no declared list: anything goes,
		{"claude-code", "claude-opus-4-8", true},       // including whatever ships next
		{"codex", "gpt-5.3-codex", true},               //
		{"educa-ai", "gpt-oss-120b", true},             // exercised against the instance
		{"educa-ai", "gemma-4-26B-A4B-it", true},       //
		{"educa-ai", "EuroLLM-9B-Instruct", false},     // 2048 tokens of context — cannot hold the prompt
		{"educa-ai", "Qwen-AgentWorld-35B-A3B", false}, // listed by /v1/models, answers 500
		{"educa-ai", "claude-opus-4-8", false},         // another engine's model
		{"educa-ai", "", true},                         // empty = the engine's default
		{"nope", "", true},
		{"nope", "anything", false}, // unknown engine: fail-closed
	} {
		msg := checkModel(tc.runtime, tc.model)
		if gotOK := msg == ""; gotOK != tc.wantOK {
			t.Errorf("checkModel(%q, %q) = %q, wantOK=%v", tc.runtime, tc.model, msg, tc.wantOK)
		}
	}
}

// A message with foreign ids sends the reader in the wrong direction — it has
// to name what THIS engine carries, and the way out through the default.
func TestCheckModelNamesTheEnginesModels(t *testing.T) {
	msg := checkModel("educa-ai", "EuroLLM-9B-Instruct")
	if msg == "" {
		t.Fatal("a model the engine does not carry has to be refused")
	}
	for _, m := range daemon.Models("educa-ai") {
		if !strings.Contains(msg, m) {
			t.Errorf("the message has to name %q: %s", m, msg)
		}
	}
	if !strings.Contains(msg, "empty for its default") {
		t.Errorf("the message has to offer the default as the way out: %s", msg)
	}
}

// The default is the first entry, and it is a declaration rather than an
// accident of ordering: whoever reorders the list changes what every agent
// without an explicit model runs on.
func TestEducaDefaultsToTheModelWithTheMostContext(t *testing.T) {
	if got := daemon.DefaultModel("educa-ai"); got != "gemma-4-26B-A4B-it" {
		t.Errorf("default model = %q — the default carries twice the context of the others "+
			"and reports tool calls correctly", got)
	}
	// An engine in front of a single provider substitutes nothing: there the
	// runtime's own default is the right answer.
	if got := daemon.DefaultModel("claude-code"); got != "" {
		t.Errorf("claude-code must leave the choice to its runtime, got %q", got)
	}
}
