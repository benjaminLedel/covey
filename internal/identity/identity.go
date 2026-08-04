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
type Principal struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Email       string
	DisplayName string
	Role        string
}

type Credentials struct {
	Email    string
	Password string
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
