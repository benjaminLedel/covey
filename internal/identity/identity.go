// Package identity definiert den IdentityProvider-Port (spec/10):
// Agenten-Identität ausstellen, Menschen authentifizieren, kurzlebige Tokens minten.
// Implementierungen: builtin (Ed25519-JWT + Argon2id) — oidc folgt post-MVP.
package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Scope beschränkt ein Token auf Zweck + System (Least Privilege).
type Scope struct {
	Audience string // z. B. "daemon" oder "zammad"
	System   string // Zielsystem, falls zutreffend
	Scopes   []string
}

type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Principal ist ein authentifizierter Mensch inkl. RBAC-Rolle (spec/09).
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
	// IssueAgentToken mintet ein kurzlebiges, gescoptes Token für eine Agent-Identität.
	IssueAgentToken(ctx context.Context, agentID uuid.UUID, scope Scope, ttl time.Duration) (Token, error)
	// VerifyAgentToken prüft Signatur, Ablauf und Audience.
	VerifyAgentToken(ctx context.Context, token, audience string) (agentID uuid.UUID, err error)
	// AuthenticateHuman prüft Login-Credentials gegen den Nutzerbestand.
	AuthenticateHuman(ctx context.Context, creds Credentials) (Principal, error)
}

// Menschliche Rollen (RBAC, spec/09-enterprise-modell.md).
const (
	RolePlatformAdmin = "platform_admin"
	RoleAgentOwner    = "agent_owner"
	RoleSecurity      = "security"
	RoleAuditor       = "auditor"
	RoleControlling   = "controlling"
)
