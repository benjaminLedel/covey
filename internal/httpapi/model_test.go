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
		{"educa-ai", "", false},                        // a declared list means: no default
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
// to name what THIS engine carries.
func TestCheckModelNamesTheEnginesModels(t *testing.T) {
	for _, model := range []string{"", "EuroLLM-9B-Instruct"} {
		msg := checkModel("educa-ai", model)
		if msg == "" {
			t.Fatalf("model %q should be refused", model)
		}
		for _, m := range daemon.Models("educa-ai") {
			if !strings.Contains(msg, m) {
				t.Errorf("model %q: the message has to name %q: %s", model, m, msg)
			}
		}
	}
	// The empty id gets its own sentence: nothing is wrong with the value, the
	// engine simply has no default to fall back on.
	if msg := checkModel("educa-ai", ""); !strings.Contains(msg, "no default model") {
		t.Errorf("the empty id needs its own wording: %s", msg)
	}
}
