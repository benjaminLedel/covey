package teamsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"covey/internal/msauth"
)

// Linking a Microsoft tenant to an organisation.
//
// The tenant ID is what routes every message, so it has to be proven, not
// typed: an organisation that could claim any tenant would receive that
// tenant's messages. The proof is a sign-in with Microsoft through the
// installation's app — the tenant comes out of the ID token the token
// endpoint hands back (never out of a redirect parameter, which the person
// clicking could edit) — by someone holding a Teams or Global Administrator
// role in that tenant. The state and cookie binding (internal/msauth) keeps a
// link somebody else started from handing a tenant to their organisation.

// LinkCookie carries the nonce of a link in progress.
const LinkCookie = "covey_teamsapp_link"

const linkScope = "openid profile"

// Directory role template IDs (Microsoft Entra built-in roles) that may link
// a tenant.
var adminRoles = map[string]string{
	"62e90394-69f5-4237-9190-012177145e10": "Global Administrator",
	"69091246-20e8-4a56-aa4d-066075b2a7a8": "Teams Administrator",
}

var (
	// ErrLinkState: the state is forged, expired, or not from this browser.
	ErrLinkState = msauth.ErrState
	// ErrNotAdmin: the signed-in account holds no admin role in its tenant.
	ErrNotAdmin = errors.New("teamsapp: only a Teams or Global Administrator of the Microsoft tenant can connect it")
)

func (s *Service) auth() msauth.App {
	return msauth.App{ClientID: s.cfg.AppID, Secret: s.cfg.AppPassword, LoginBase: s.cfg.LoginBase, Purpose: "teamsapp-link"}
}

// StartLink returns the Microsoft sign-in URL and the nonce to put into the
// LinkCookie.
func (s *Service) StartLink(orgID uuid.UUID, redirectURI string) (signIn, nonce string, err error) {
	if s == nil {
		return "", "", errNoApp
	}
	return s.auth().Start(orgID.String(), redirectURI, linkScope)
}

// FinishLink completes a link: checks the state and the cookie, exchanges the
// code, reads tenant, account and roles from the ID token, and links the
// tenant to the organisation the state names.
func (s *Service) FinishLink(ctx context.Context, state, cookieNonce, code, redirectURI string) (orgID uuid.UUID, tenantID string, err error) {
	if s == nil {
		return uuid.Nil, "", errNoApp
	}
	subject, _, claims, err := s.auth().Finish(ctx, state, cookieNonce, code, redirectURI, linkScope)
	if errors.Is(err, msauth.ErrState) {
		return uuid.Nil, "", err
	}
	org, perr := uuid.Parse(subject)
	if perr != nil {
		return uuid.Nil, "", ErrLinkState
	}
	if err != nil {
		return org, "", fmt.Errorf("teamsapp: %w", err)
	}
	if !holdsAdminRole(claims.Wids) {
		if len(claims.Wids) == 0 {
			return org, "", fmt.Errorf("%w (the sign-in carried no directory roles: the app registration has to emit them — groupMembershipClaims \"DirectoryRole\")", ErrNotAdmin)
		}
		return org, "", ErrNotAdmin
	}
	if err := s.Store.Link(ctx, org, claims.Tid, claims.Who()); err != nil {
		return org, claims.Tid, err
	}
	return org, claims.Tid, nil
}

func holdsAdminRole(wids []string) bool {
	for _, w := range wids {
		if _, ok := adminRoles[strings.ToLower(w)]; ok {
			return true
		}
	}
	return false
}
