// Package runtimes is the capacity layer: a runtime is an engine plus the
// credentials to run it, and an agent is assigned to one the way an employee is
// assigned to a cost centre (spec/18-runtimes-capacity.md).
//
// The split against internal/secrets is deliberate and load-bearing. The secret
// store is STORAGE — an encrypted value under a name, org-bound, sensitive or
// not. Which of several values an agent gets right now is POLICY, and policy
// needs things a storage layer has no business knowing: what a run consumed,
// that an LLM API rejected a token, which agent sits where. That is here.
package runtimes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("runtime not found")

// ErrExhausted: the runtime has credentials, but at this moment none is usable
// — every one is either parked or has used up its limit in the current window.
// Deliberately its own error: nothing is missing, it is only too early. The
// caller postpones the work rather than failing it.
var ErrExhausted = errors.New("no credential of this runtime is free")

// Exhausted carries the moment the runtime frees up again — the earliest
// cooldown across its credentials. Zero means unknown.
type Exhausted struct {
	Runtime string
	Until   time.Time
}

func (e *Exhausted) Error() string {
	if e.Until.IsZero() {
		return fmt.Sprintf("runtime %q: %v", e.Runtime, ErrExhausted)
	}
	return fmt.Sprintf("runtime %q: %v (free again at %s)",
		e.Runtime, ErrExhausted, e.Until.Format(time.RFC3339))
}

func (e *Exhausted) Is(target error) bool { return target == ErrExhausted }

// Limit caps what one credential may consume in a rolling window.
//
// Two units, because "consumption" means different things: on a metered
// credential it is money, and the figure is what was billed. On a subscription
// seat money is a list-price equivalent nobody pays, and the token count is the
// closer stand-in for the provider's own window. WindowSecs=0 means no limit.
type Limit struct {
	Amount     float64 `json:"amount"`
	Unit       string  `json:"unit"` // "usd" | "tokens"
	WindowSecs int     `json:"window_secs"`
}

func (l Limit) Active() bool { return l.WindowSecs > 0 && l.Amount > 0 }

// Runtime is a configured workplace.
type Runtime struct {
	ID          uuid.UUID    `json:"id"`
	OrgID       uuid.UUID    `json:"org_id"`
	Engine      string       `json:"engine"`
	DisplayName string       `json:"display_name"`
	Model       string       `json:"model"`
	// FallbackRuntimeID: the contract to try when every credential of THIS
	// runtime is exhausted (session limit, cooldown) — e.g. an OpenAI-backed
	// runtime standing in while the Claude Code subscription seat is in its
	// limit window. NULL is the default: exhaustion means waiting, as before.
	FallbackRuntimeID *uuid.UUID   `json:"fallback_runtime_id,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	Credentials       []Credential `json:"credentials"`
}

// Credential is one unit of capacity. Ord is its place in the merit order.
type Credential struct {
	Ord            int        `json:"ord"`
	Kind           string     `json:"kind"` // daemon.CredAPIKey | daemon.CredSubscription
	SecretKey      string     `json:"secret_key"`
	SecretSlot     int        `json:"secret_slot"`
	Label          string     `json:"label"`
	CooldownUntil  *time.Time `json:"cooldown_until,omitempty"`
	CooldownReason string     `json:"cooldown_reason,omitempty"`
	Limit          Limit      `json:"limit"`
}

func (c Credential) parked(now time.Time) bool {
	return c.CooldownUntil != nil && c.CooldownUntil.After(now)
}

// Binding records which agent sits on which credential.
type Binding struct {
	AgentID uuid.UUID `json:"agent_id"`
	Ord     int       `json:"ord"`
	HomeOrd *int      `json:"home_ord,omitempty"`
	Reason  string    `json:"reason"`
	BoundAt time.Time `json:"bound_at"`
}

// Why an agent came to sit where it sits.
const (
	ReasonInitial = "initial"
	ReasonLimit   = "limit"  // dodged: the previous credential was at its limit
	ReasonError   = "error"  // dodged: the target system rejected it
	ReasonReturn  = "return" // returned to its home seat
)

// Picked is the credential chosen for one waking phase, ready to hand to the
// daemon: the value plus how the engine wants it delivered.
type Picked struct {
	RuntimeID uuid.UUID
	Ord       int
	Kind      string
	Label     string
	Value     string
	// Exactly one of these is set, from the engine's declaration: an
	// environment variable, or a file at this path in the agent home.
	EnvVar string
	Path   string
}

// UsageFunc reports what one credential consumed in a window. Injected rather
// than called, because consumption is booked in observability and this package
// must not depend on it — the dependency would run the wrong way round.
type UsageFunc func(ctx context.Context, runtimeID uuid.UUID, ord int, window time.Duration) (usd float64, tokens int64, err error)

// ReportedFunc gives the PROVIDER's own utilisation of a credential, where the
// engine can ask for it and the answer is recent. ok=false means: nothing
// known, fall back on what we booked ourselves.
type ReportedFunc func(runtimeID uuid.UUID, ord int) (percent float64, ok bool)

// Signals is what the capacity layer can learn about a credential from outside:
// what we booked against it, and what the provider says about it. Both are
// optional — with neither, only cooldowns apply.
type Signals struct {
	Usage    UsageFunc
	Reported ReportedFunc
}

// reportedFull: from this share of its window a credential counts as used up,
// regardless of any configured limit.
//
// This is not a policy cap but a FACT about capacity: a seat the provider
// reports at 98% will not carry a run of any length, and starting one costs a
// wake and a sandbox to arrive at the error we already knew about. The
// remaining two per cent are left as the margin in which a short run can still
// succeed — and if it does not, the hard signal parks the credential anyway.
const reportedFull = 98.0

// SecretReader is the storage half: read one exact value. Deliberately narrow —
// the capacity layer needs to read values and nothing else about secrets.
type SecretReader interface {
	Value(ctx context.Context, orgID uuid.UUID, key string, slot int) (string, error)
}
