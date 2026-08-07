// Package secrets defines the SecretStore port (spec/10): get/put/delete.
// Implementations: builtin (AES-GCM column in Postgres) — vault follows
// post-MVP.
package secrets

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("secret not found")

// ErrLastValue: the last value of a key cannot be removed on its own —
// otherwise a secret would be left standing with no value at all, which is not
// gone but unusable, and a state nothing else in the store expects. Deleting the
// key is the way, and it clears the assignments along with it.
//
// Its own error so the API can answer this as what it is: a request that is
// well-formed and refused by the state, not a server fault.
var ErrLastValue = errors.New("the last value of a secret cannot be removed on its own — delete the secret instead")

// PoolValue describes one value of a key for administration and the UI. Value
// and Prefix follow the same rule as in KeyPreview: readable variables in full,
// sensitive ones only as a prefix.
type PoolValue struct {
	Slot      int       `json:"slot"`
	Prefix    string    `json:"prefix"`
	Value     string    `json:"value,omitempty"`
	Sensitive bool      `json:"sensitive"`
	UpdatedAt time.Time `json:"updated_at"`
}

// KeyPreview describes a secret for the UI. By default a secret is a readable
// variable (server name, URL, config value): Value carries the full plaintext.
// With Sensitive=true the value stays write-only — Prefix then only carries a
// short, deliberately limited prefix as a recognition aid (as with
// GitHub/Stripe); empty when the value is too short to hint at without
// meaningful disclosure.
// AgentIDs are the explicit assignments of an org-wide secret — empty means:
// not assigned to any agent yet, the secret reaches nobody.
// Values is the key's pool. It always holds at least one entry; a key with a
// single value (the normal case) has exactly one, and the fields above then
// describe it. Beyond that the fields describe the lowest slot — enough for a
// list that only wants to recognise the key.
type KeyPreview struct {
	Key       string      `json:"key"`
	Prefix    string      `json:"prefix"`
	Sensitive bool        `json:"sensitive"`
	Value     string      `json:"value,omitempty"` // only set when Sensitive=false
	AgentIDs  []string    `json:"agent_ids"`
	Values    []PoolValue `json:"values"`
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

	// Value reads one exact value of a key. The capacity layer
	// (internal/runtimes) uses it to resolve the credential it picked; it knows
	// which slot it wants and needs nothing else about secrets.
	Value(ctx context.Context, orgID uuid.UUID, key string, slot int) (string, error)
	// PutAgent/DeleteAgent manage an agent's own secrets.
	PutAgent(ctx context.Context, orgID, agentID uuid.UUID, key, value string) error
	DeleteAgent(ctx context.Context, orgID, agentID uuid.UUID, key string) error
	// Keys lists the names only — values stay write-only for the API.
	Keys(ctx context.Context, orgID uuid.UUID) ([]string, error)
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

	// --- Several values under one key ---
	//
	// That a key can carry several values is a STORAGE statement and belongs
	// here, next to the encryption and the AAD. Which of them an agent gets is
	// capacity policy and lives in internal/runtimes (spec/18).

	// AddValue appends another value to an org-wide key and returns its slot.
	// The key has to exist — a second value grows out of a secret, it is not
	// created as one. It inherits the key's sensitive flag: half a pool being
	// readable and the other half write-only would be a trap.
	AddValue(ctx context.Context, orgID uuid.UUID, key, value string) (int, error)
	// DeleteValue removes a single value. The last one cannot go this way —
	// that is Delete's job, which also clears the assignments.
	DeleteValue(ctx context.Context, orgID uuid.UUID, key string, slot int) error
}
