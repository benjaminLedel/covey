package llm

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/claudeapi"
	"covey/internal/secrets"
)

// Seats is where the agents' own engine access lives: the credentials the
// organisation's runtime seats hold (internal/runtimes, spec/18). An interface
// here rather than an import, so that this package keeps knowing nothing about
// the capacity layer beyond the one question it asks of it.
//
// *runtimes.Store implements it, and answers ErrNotFound on a nil receiver —
// a caller without the capacity layer passes nil and gets the named secrets
// only.
type Seats interface {
	ControlPlaneCredential(ctx context.Context, orgID uuid.UUID, engine string) (value string, subscription bool, err error)
}

// claudeEngine is the engine whose seats carry Anthropic access.
const claudeEngine = "claude-code"

// Resolve finds the provider an organisation can use for control-plane calls.
//
// The order is the order of what the organisation actually operates on, and it
// lives in one place so that copilot, dream, setup, the chat triage and the
// narration arrive at the same answer. Anthropic first, because its credential
// is the one every existing installation has — and within Anthropic, first the
// two org secrets by the names the engine declares, then whatever the
// organisation's Claude Code seats hold (#483). An organisation whose agents
// run on a token filed under another name must not have a control plane that
// reports no model.
//
// Not found is ErrNoCredential and not an error to display: whoever has no
// credential should not be offered the feature at all.
func Resolve(ctx context.Context, store secrets.Store, seats Seats, orgID uuid.UUID) (Provider, error) {
	if store != nil {
		if cred, oauth, ok := claudeapi.ResolveOrg(ctx, store, orgID); ok {
			return anthropic{cred: cred, oauth: oauth}, nil
		}
	}
	if seats != nil {
		if v, sub, err := seats.ControlPlaneCredential(ctx, orgID, claudeEngine); err == nil && v != "" {
			return anthropic{cred: v, oauth: claudeapi.IsOAuth(v, sub)}, nil
		}
	}
	return nil, ErrNoCredential
}

// Anthropic is the Messages API provider for a credential already in hand —
// for a tool outside any organisation, like the chat evaluation
// (internal/chat/testdata/eval). Everything inside the platform goes through
// Resolve, so that every feature arrives at the same credential.
func Anthropic(cred string, oauth bool) Provider { return anthropic{cred: cred, oauth: oauth} }

// Available: is there a provider at all? For the status endpoints that decide
// whether a feature appears in the interface.
func Available(ctx context.Context, store secrets.Store, seats Seats, orgID uuid.UUID) bool {
	_, err := Resolve(ctx, store, seats, orgID)
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
	return claudeapi.Messages(ctx, a.cred, a.oauth, call(req), req.System, toClaudeMessages(req.Messages))
}

func (a anthropic) Stream(ctx context.Context, req Request, onText func(string)) (string, error) {
	return claudeapi.MessagesStream(ctx, a.cred, a.oauth, call(req), req.System, toClaudeMessages(req.Messages), onText)
}

func call(req Request) claudeapi.Call {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = anthropicBest
		if req.Tier == TierFast {
			model = anthropicFast
		}
	}
	return claudeapi.Call{
		Model:      model,
		MaxTokens:  req.MaxTokens,
		Effort:     req.Effort,
		NoThinking: req.NoThinking,
	}
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
