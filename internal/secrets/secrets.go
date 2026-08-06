// Package secrets defines the SecretStore port (spec/10): get/put/delete.
// Implementations: builtin (AES-GCM column in Postgres) — vault follows
// post-MVP.
package secrets

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("secret not found")

// KeyPreview describes a secret for the UI. By default a secret is a readable
// variable (server name, URL, config value): Value carries the full plaintext.
// With Sensitive=true the value stays write-only — Prefix then only carries a
// short, deliberately limited prefix as a recognition aid (as with
// GitHub/Stripe); empty when the value is too short to hint at without
// meaningful disclosure.
// AgentIDs are the explicit assignments of an org-wide secret — empty means:
// not assigned to any agent yet, the secret reaches nobody.
type KeyPreview struct {
	Key       string   `json:"key"`
	Prefix    string   `json:"prefix"`
	Sensitive bool     `json:"sensitive"`
	Value     string   `json:"value,omitempty"` // only set when Sensitive=false
	AgentIDs  []string `json:"agent_ids"`
}

// ResolvableKey is a secret name that reaches an agent. Owned=true is the
// agent's own secret — it shadows the org secret of the same name.
type ResolvableKey struct {
	Key   string `json:"key"`
	Owned bool   `json:"owned"`
}

const (
	// previewMinLen: shorter values stay fully masked.
	previewMinLen = 12
	// previewChars: this many leading characters may become visible.
	previewChars = 4
)

// Preview hints at a value for the UI — write-only stays the rule, this is the
// narrowly bounded exception. An empty string means "mask completely".
func Preview(value string) string {
	r := []rune(value)
	if len(r) > previewMinLen {
		return string(r[:previewChars])
	}
	return ""
}

type Store interface {
	// Get/Put/Delete operate on org-wide secrets (without agent context, e.g.
	// bootstrap and webhooks).
	Get(ctx context.Context, orgID uuid.UUID, key string) (string, error)
	Put(ctx context.Context, orgID uuid.UUID, key, value string) error
	Delete(ctx context.Context, orgID uuid.UUID, key string) error
	// Resolve resolves a secret for an agent: the agent's own secret before the
	// org-wide one. Org secrets reach an agent only on explicit assignment —
	// without an assignment an org secret reaches no agent.
	Resolve(ctx context.Context, orgID, agentID uuid.UUID, key string) (string, error)
	// PutAgent/DeleteAgent manage an agent's own secrets.
	PutAgent(ctx context.Context, orgID, agentID uuid.UUID, key, value string) error
	DeleteAgent(ctx context.Context, orgID, agentID uuid.UUID, key string) error
	// Keys lists the names only — values stay write-only for the API.
	Keys(ctx context.Context, orgID uuid.UUID) ([]string, error)
	// ResolvableKeys lists every secret NAME that reaches this agent: its own
	// secrets plus the org secrets explicitly assigned to it — exactly what
	// Resolve would find. Values stay in the store; this is for callers that
	// must CHOOSE among several secrets before resolving one of them (the
	// runtime credential picks this way).
	ResolvableKeys(ctx context.Context, orgID, agentID uuid.UUID) ([]ResolvableKey, error)
	// Previews returns names plus the limited value prefix (see Preview) of the
	// org-wide secrets, including their assignments.
	Previews(ctx context.Context, orgID uuid.UUID) ([]KeyPreview, error)
	// AgentPreviews returns an agent's own secrets.
	AgentPreviews(ctx context.Context, orgID, agentID uuid.UUID) ([]KeyPreview, error)
	// Assign/Unassign maintain the explicit assignment of org-wide secrets.
	Assign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error
	Unassign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error
	// MarkSensitive/MarkAgentSensitive mark a secret as particularly worth
	// protecting (token, password): from then on the value is write-only and the
	// previews only return the limited prefix. Deliberately one-way — lifting
	// the protection again would mean disclosing the value after all. The way
	// back is only through delete and re-create; overwriting keeps the flag.
	MarkSensitive(ctx context.Context, orgID uuid.UUID, key string) error
	MarkAgentSensitive(ctx context.Context, orgID, agentID uuid.UUID, key string) error
}
