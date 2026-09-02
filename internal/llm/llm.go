// Package llm is the control plane's own path to a model.
//
// Two features call a model from the control plane rather than from a sandbox:
// the config copilot writes agent configuration, and the dream tidies up the
// memory at night. A third is on the way — the setup personalises the People
// department from the company description (spec/20).
//
// All three used to sit directly on internal/claudeapi, and that wired them to
// one provider: an organisation running covey on Codex has agents that work and
// a control plane that cannot think. This package is the narrow port in front
// of it, in the pattern the platform already uses for IdentityProvider and
// SecretStore — interface here, implementations in subpackages
// ([`spec/10-architecture-stack.md`], principle 10).
//
// What is deliberately NOT here: tools, sessions, streaming. This is the
// tool-less single shot — anything agentic belongs in an engine with a runtime,
// credentials and cost attribution behind it (D14).
//
// Guard rail unchanged: the credential never leaves the control plane. Neither
// into the browser nor into a sandbox.
package llm

import (
	"context"
	"errors"
)

// ErrNoCredential: the organisation has no credential this package could use.
// Its own error because it is not a failure but a state — the caller does not
// offer the feature rather than reporting it as broken.
var ErrNoCredential = errors.New("no control-plane LLM credential configured for this organisation")

// Message is one turn. Content is plain text — every provider takes that.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Tier says what KIND of model is wanted, not which one. A model id is
// provider-specific, and a caller that hardcodes one binds its feature to a
// provider — which is exactly the knot this package unties.
type Tier string

const (
	// TierBest: judgement matters, latency does not. Config proposals, the dream.
	TierBest Tier = "best"
	// TierFast: a short, mechanical job where waiting is the cost.
	TierFast Tier = "fast"
)

// Request is one call.
type Request struct {
	Tier Tier
	// Model pins a concrete model of the provider. Empty = the provider picks
	// by tier, which is what almost every caller should do.
	Model     string
	MaxTokens int
	// Effort steers thinking depth (low | medium | high | xhigh | max);
	// empty = the model's default. Ignored by providers that have no such knob.
	Effort string
	// NoThinking turns thinking off where the provider supports it. Only for
	// calls without tools and with a machine-readable answer.
	NoThinking bool
	System     string
	Messages   []Message
}

// Provider is one configured path to a model, credential included. Created by
// Resolve; callers do not build one themselves.
type Provider interface {
	// Name is the provider, for logs and for the interface: which engine
	// actually answered.
	Name() string
	// Complete performs the single shot and returns the model's text.
	Complete(ctx context.Context, req Request) (string, error)
}
