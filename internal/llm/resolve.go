package llm

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/claudeapi"
	"covey/internal/secrets"
)

// Resolve finds the provider an organisation can use for control-plane calls.
//
// The order is the order of what the organisation actually operates on, and it
// lives in one place so that copilot, dream and setup arrive at the same
// answer. Anthropic first, because its credential is the one every existing
// installation has.
//
// Not found is ErrNoCredential and not an error to display: whoever has no
// credential should not be offered the feature at all.
func Resolve(ctx context.Context, store secrets.Store, orgID uuid.UUID) (Provider, error) {
	if cred, oauth, ok := claudeapi.ResolveOrg(ctx, store, orgID); ok {
		return anthropic{cred: cred, oauth: oauth}, nil
	}
	return nil, ErrNoCredential
}

// Available: is there a provider at all? For the status endpoints that decide
// whether a feature appears in the interface.
func Available(ctx context.Context, store secrets.Store, orgID uuid.UUID) bool {
	_, err := Resolve(ctx, store, orgID)
	return err == nil
}

// anthropic is the Messages API path. It stays a thin wrapper around
// internal/claudeapi — that package keeps the auth mechanics (API key via
// x-api-key, subscription token via Bearer plus the Claude Code identity
// block), which are subtle enough to have exactly one home.
type anthropic struct {
	cred  string
	oauth bool
}

func (anthropic) Name() string { return "anthropic" }

// Models per tier. Here and nowhere else: the callers say what kind of job it
// is, this table says which model does it. „Best" means the latest Opus — the
// dream ran on it before this table existed, and the config copilot has always
// wanted it.
const (
	anthropicBest = "claude-opus-5"
	anthropicFast = "claude-haiku-4-5-20251001"
)

func (a anthropic) Complete(ctx context.Context, req Request) (string, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = anthropicBest
		if req.Tier == TierFast {
			model = anthropicFast
		}
	}
	return claudeapi.Messages(ctx, a.cred, a.oauth,
		claudeapi.Call{
			Model:      model,
			MaxTokens:  req.MaxTokens,
			Effort:     req.Effort,
			NoThinking: req.NoThinking,
		},
		req.System, toClaudeMessages(req.Messages))
}

func toClaudeMessages(in []Message) []claudeapi.Message {
	out := make([]claudeapi.Message, 0, len(in))
	for _, m := range in {
		out = append(out, claudeapi.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// An OpenAI implementation belongs beside this one and is the open half of D15
// ([`spec/07-open-decisions.md`]): until it exists, an organisation that set
// covey up with Codex has agents that work and no control-plane features. That
// is a gap with a name, not an oversight — every caller here already asks
// Resolve and handles ErrNoCredential, so the implementation is the only thing
// missing, not a change to the callers.
