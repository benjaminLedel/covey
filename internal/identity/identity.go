// Package identity defines the IdentityProvider port (spec/10): issue agent
// identities, authenticate humans, mint short-lived tokens.
// Implementations: builtin (Ed25519 JWT + Argon2id) — oidc follows post-MVP.
package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Scope restricts a token to purpose + system (least privilege).
type Scope struct {
	Audience string // e.g. "daemon" or "zammad"
	System   string // target system, if applicable
	Scopes   []string
}

type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Principal is an authenticated human including their RBAC role (spec/09).
//
// Two levels sit in here since accounts were split off from memberships
// (FR-002): AccountID and PlatformRole belong to the LOGIN — one person, valid
// across organisations. ID, OrgID and Role belong to the ACTIVE MEMBERSHIP,
// the seat this session currently works from.
//
// ID and OrgID are deliberately kept at their old names and meanings: several
// hundred call sites read p.OrgID, and none of them has to care that the
// login now sits one level up. Both are uuid.Nil while an account has no
// membership — the state self-registration produces and the org gate resolves.
type Principal struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Email       string
	DisplayName string
	Role        string

	// AccountID identifies the login itself.
	AccountID uuid.UUID
	// PlatformRole is the instance level: user | system_admin. Deliberately
	// not an org role — no organisation may hand it to itself.
	PlatformRole string
}

// HasOrg reports whether this session works from a seat. False means: signed
// in, but not (yet) in any organisation.
func (p Principal) HasOrg() bool { return p.OrgID != uuid.Nil }

type Credentials struct {
	Email    string
	Password string
}

// Membership is one seat of an account: which organisation, in which role.
type Membership struct {
	HumanID uuid.UUID `json:"human_id"`
	OrgID   uuid.UUID `json:"org_id"`
	OrgName string    `json:"org_name"`
	Role    string    `json:"role"`
}

type Provider interface {
	// IssueAgentToken mints a short-lived, scoped token for an agent identity.
	IssueAgentToken(ctx context.Context, agentID uuid.UUID, scope Scope, ttl time.Duration) (Token, error)
	// VerifyAgentToken checks signature, expiry and audience.
	VerifyAgentToken(ctx context.Context, token, audience string) (agentID uuid.UUID, err error)
	// AuthenticateHuman checks login credentials against the user records.
	AuthenticateHuman(ctx context.Context, creds Credentials) (Principal, error)
}

// Human roles (RBAC, spec/09-enterprise-model.md).
const (
	RolePlatformAdmin = "platform_admin"
	RoleAgentOwner    = "agent_owner"
	RoleSecurity      = "security"
	RoleAuditor       = "auditor"
	RoleControlling   = "controlling"
)
