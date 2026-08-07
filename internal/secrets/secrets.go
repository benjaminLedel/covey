// Package secrets defines the SecretStore port (spec/10): get/put/delete.
// Implementations: builtin (AES-GCM column in Postgres) — vault follows
// post-MVP.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("secret not found")

// ErrPoolExhausted: the key has values, but at this moment none is usable —
// every one is either in cooldown or has used up its limit in the current
// window. Deliberately its own error and not ErrNotFound: nothing is missing
// here, it is just too early. The caller should postpone the work rather than
// fail it (see PoolExhausted.Until).
var ErrPoolExhausted = errors.New("all values of this secret are exhausted or in cooldown")

// PoolExhausted carries the moment at which the pool frees up again — the
// earliest cooldown across all its values. Zero means unknown; then the caller
// has to fall back on its own interval.
type PoolExhausted struct {
	Key   string
	Until time.Time
}

func (e *PoolExhausted) Error() string {
	if e.Until.IsZero() {
		return fmt.Sprintf("secret %q: %v", e.Key, ErrPoolExhausted)
	}
	return fmt.Sprintf("secret %q: %v (free again at %s)",
		e.Key, ErrPoolExhausted, e.Until.Format(time.RFC3339))
}

func (e *PoolExhausted) Is(target error) bool { return target == ErrPoolExhausted }

// Value is one picked value out of a key's pool. Slot identifies which one it
// was — the caller needs it in order to book consumption against it and to put
// exactly this value into cooldown when the target system rejects it.
type Value struct {
	Value string
	Slot  int
	Label string
}

// Limit caps what a single value may consume in a rolling window.
//
// Two units, because "consumption" means different things depending on the
// credential: for an API key it is money (Unit=usd, cost_entries is real
// billing there), for a subscription token money is notional — the real limit
// is the provider's rolling window, and tokens are the closest proxy
// (Unit=tokens). WindowSecs=0 means: no limit.
type Limit struct {
	Amount     float64 `json:"amount"`
	Unit       string  `json:"unit"` // "usd" | "tokens"
	WindowSecs int     `json:"window_secs"`
}

func (l Limit) Active() bool { return l.WindowSecs > 0 && l.Amount > 0 }

// PoolValue describes one value of a key for administration and the UI. Value
// and Prefix follow the same rule as in KeyPreview: readable variables in full,
// sensitive ones only as a prefix.
type PoolValue struct {
	Slot           int        `json:"slot"`
	Label          string     `json:"label"`
	Prefix         string     `json:"prefix"`
	Value          string     `json:"value,omitempty"`
	Sensitive      bool       `json:"sensitive"`
	CooldownUntil  *time.Time `json:"cooldown_until,omitempty"`
	CooldownReason string     `json:"cooldown_reason,omitempty"`
	Limit          Limit      `json:"limit"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Binding records which agent sits on which value. Reason documents how it got
// there; HomeSlot is its home seat as long as it is standing in for another
// (nil = it is sitting on its own).
type Binding struct {
	AgentID  uuid.UUID `json:"agent_id"`
	Slot     int       `json:"slot"`
	HomeSlot *int      `json:"home_slot,omitempty"`
	Reason   string    `json:"reason"`
	BoundAt  time.Time `json:"bound_at"`
}

// Reasons for a binding — how an agent came to sit on this value.
const (
	ReasonInitial = "initial" // first assignment
	ReasonLimit   = "limit"   // dodged: the previous value had used up its limit
	ReasonError   = "error"   // dodged: the target system rejected the previous value
	ReasonReturn  = "return"  // returned to the home seat
)

// UsageFunc reports what a value has consumed in the given window.
//
// Injected rather than called directly, because consumption is booked in
// observability and the secret store must not depend on it — the dependency
// would run the wrong way round. Whoever passes nil gets no limit check;
// cooldowns still apply, because they need no measurement.
type UsageFunc func(ctx context.Context, orgID uuid.UUID, key string, slot int, window time.Duration) (usd float64, tokens int64, err error)

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
	//
	// Convenience wrapper around Pick without a limit check, for every caller
	// that only wants the value and does not book any consumption against it.
	Resolve(ctx context.Context, orgID, agentID uuid.UUID, key string) (string, error)

	// Pick chooses one value out of the key's pool for this agent and reports
	// which one it was.
	//
	// The choice is sticky: an agent keeps its value as long as that value is
	// healthy — that keeps the prompt cache warm and its identity in the target
	// system stable. It moves only when its value is in cooldown or has used up
	// its limit, and then to the least loaded healthy one. An agent-owned secret
	// takes precedence over the pool as before; such an agent does not take part
	// in the distribution at all.
	//
	// usage may be nil — then limits are not checked (cooldowns still are).
	// If no value is usable, the error wraps ErrPoolExhausted.
	Pick(ctx context.Context, orgID, agentID uuid.UUID, key string, usage UsageFunc) (Value, error)
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

	// --- Pool: several values under one key ---

	// AddValue appends another value to an org-wide key and returns its slot.
	// The key has to exist — a pool grows out of a secret, it is not created as
	// one. The new value inherits the key's sensitive flag: half a pool being
	// readable and the other half write-only would be a trap.
	AddValue(ctx context.Context, orgID uuid.UUID, key, value, label string) (int, error)
	// DeleteValue removes a single value. The last one cannot go this way —
	// that is Delete's job, which also clears the assignments.
	DeleteValue(ctx context.Context, orgID uuid.UUID, key string, slot int) error
	// SetLimit sets (or with Limit{} clears) the cap of a single value.
	SetLimit(ctx context.Context, orgID uuid.UUID, key string, slot int, l Limit) error
	// SetLabel renames a value — the recognition aid for whoever administers the
	// pool ("subscription Ben", "team account").
	SetLabel(ctx context.Context, orgID uuid.UUID, key string, slot int, label string) error
	// Cooldown parks a value until the given moment. Called for the hard signal
	// (the target system rejected the credential) and by the limit check.
	// A zero time frees it again.
	Cooldown(ctx context.Context, orgID uuid.UUID, key string, slot int, until time.Time, reason string) error
	// Bindings reports who sits on which value of this key.
	Bindings(ctx context.Context, orgID uuid.UUID, key string) ([]Binding, error)
}
