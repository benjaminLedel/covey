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

// Lifetime is what the platform knows about a stored value beyond the value
// itself: when the target system will stop accepting it, whether it already
// has, and what the last connection test saw. It is storage ABOUT the value,
// kept beside it — not a policy about choosing between values, which is why it
// lives here and the cooldowns of spec/18 do not.
//
// Every field is cleared when the value is overwritten: a new value is a new
// credential, and the state of the old one would be a lie beside it.
type Lifetime struct {
	// ExpiresAt is when the target system will stop accepting the value —
	// entered by hand, or reported by the plugin where the system states it.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RejectedAt is when the target system refused the credential itself (a
	// 401, not a 403). Set by the first rejection, cleared by a successful
	// probe or a new value.
	RejectedAt     *time.Time `json:"rejected_at,omitempty"`
	RejectedReason string     `json:"rejected_reason,omitempty"`
	// ProbedAt is the last connection test; ProbeError what went wrong, empty
	// when it worked; ProbeIdentity whom the system saw.
	ProbedAt      *time.Time `json:"probed_at,omitempty"`
	ProbeError    string     `json:"probe_error,omitempty"`
	ProbeIdentity string     `json:"probe_identity,omitempty"`
	// CredentialID is the target system's own id for the value, where it has
	// one — what a rotation revokes afterwards.
	CredentialID string `json:"credential_id,omitempty"`
	// Rotatable says the plugin can mint this value's successor.
	Rotatable bool `json:"rotatable,omitempty"`
	// WarnedAt records that a person has been told about the current state
	// (rejected, or about to expire); it goes when the state does.
	WarnedAt *time.Time `json:"warned_at,omitempty"`
}

// Expired says whether the value is past its stated expiry at t.
func (l Lifetime) Expired(t time.Time) bool {
	return l.ExpiresAt != nil && !t.Before(*l.ExpiresAt)
}

// Ref addresses one stored value: an org-wide one (AgentID nil) or an agent's
// own, in a slot of its key.
type Ref struct {
	OrgID   uuid.UUID
	AgentID *uuid.UUID
	Key     string
	Slot    int
}

// Stored is a value as the lifetime work sees it: where it is and what is
// known about it — never the plaintext, which Open hands out on demand.
type Stored struct {
	Ref
	Sensitive bool
	Lifetime
}

// Probe is the outcome of a connection test to record beside the value.
type Probe struct {
	At       time.Time
	Identity string
	Err      string
	// Rejected: the error was the credential itself, not the connection.
	Rejected bool
	// What the plugin learned about the lifetime, if anything. A nil
	// ExpiresAt and an empty CredentialID leave the stored ones standing —
	// the plugin not knowing today does not unsay what it knew yesterday.
	ExpiresAt    *time.Time
	CredentialID string
	Rotatable    bool
}

// PoolValue describes one value of a key for administration and the UI. Value
// and Prefix follow the same rule as in KeyPreview: readable variables in full,
// sensitive ones only as a prefix.
type PoolValue struct {
	Slot      int       `json:"slot"`
	Prefix    string    `json:"prefix"`
	Value     string    `json:"value,omitempty"`
	Sensitive bool      `json:"sensitive"`
	UpdatedAt time.Time `json:"updated_at"`
	Lifetime
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
	Lifetime
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

	// --- The life of a value ---
	//
	// A credential has a lifetime the store did not know about until it
	// learned it the hard way, from a 401 in a recording. What follows keeps
	// what the platform learns about a value — from a probe, from a rejection,
	// from a person — beside the value, so that the lint and the UI can say
	// it before a run does.

	// Lookup names the value Resolve would hand the agent for key — its
	// address and what is known about it, no plaintext.
	Lookup(ctx context.Context, orgID, agentID uuid.UUID, key string) (Stored, error)
	// List returns every stored value whose key ends in suffix: one
	// organisation's, or all of them for uuid.Nil (the probe loop's view).
	List(ctx context.Context, orgID uuid.UUID, suffix string) ([]Stored, error)
	// Open decrypts one exact value — org-wide or an agent's own.
	Open(ctx context.Context, ref Ref) (string, error)
	// Replace writes a successor into an existing slot and the lifetime that
	// came with it. The row's protection stays; everything else about the old
	// value goes. ErrNotFound when the slot does not exist — a rotation does
	// not create secrets.
	Replace(ctx context.Context, ref Ref, value string, lt Lifetime) error
	// RecordProbe keeps the outcome of a connection test. A working credential
	// is not a rejected one: success clears RejectedAt and WarnedAt.
	RecordProbe(ctx context.Context, ref Ref, p Probe) error
	// MarkRejected marks the value Resolve hands the agent for key as refused
	// by the target system. It reports the value and whether this is news —
	// the first rejection is worth telling somebody, the fiftieth is not.
	MarkRejected(ctx context.Context, orgID, agentID uuid.UUID, key, reason string) (Stored, bool, error)
	// SetExpiry records when a value runs out, as a person knows it; nil
	// clears it. The plugin's report wins over it where the system states
	// the date, because the system is the one that will act on it.
	SetExpiry(ctx context.Context, ref Ref, at *time.Time) error
	// MarkWarned notes that a person has been told about the current state.
	MarkWarned(ctx context.Context, ref Ref) error
}
