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

	// ViaAPIKey says how this principal proved who it is: false for a browser
	// session, true for an API key. The rights are the same — a key carries the
	// role of its seat — but two moves are reserved for the session, and
	// therefore for the password: minting another key and changing the
	// password itself. A leaked credential must not be able to entrench
	// itself.
	ViaAPIKey bool
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

// Human roles (RBAC, spec/09-enterprise-model.md). They belong to the SEAT —
// the row in humans that ties an account to an organisation — and every
// organisation hands them out itself. The instance level is PlatformRole and
// lives on the account; the two must not be confused, which is why the top org
// role is called org_admin and not, as until migration 0061, platform_admin.
const (
	RoleOrgAdmin    = "org_admin"
	RoleAgentOwner  = "agent_owner"
	RoleSecurity    = "security"
	RoleAuditor     = "auditor"
	RoleControlling = "controlling"
)

// LegacyRoleOrgAdmin is what RoleOrgAdmin was called before migration 0061.
// Rows carrying it are rewritten by that migration; the name stays here for
// the two edges the migration does not reach — an API client that still sends
// the old value, and a database somebody upgraded halfway.
const LegacyRoleOrgAdmin = "platform_admin"

// NormalizeRole maps the legacy name onto the current one. Call it at every
// edge where a role arrives from outside — the database, a request body, the
// CLI — so that no comparison further in has to know both names.
func NormalizeRole(role string) string {
	if role == LegacyRoleOrgAdmin {
		return RoleOrgAdmin
	}
	return role
}
