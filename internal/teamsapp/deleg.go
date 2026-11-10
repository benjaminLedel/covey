package teamsapp

import (
	"context"

	"github.com/google/uuid"

	"covey/internal/msauth"
)

// DelegationRef is what delegated access needs of an organisation's bot:
// its id (the key tokens are kept under), the tenant, and the app
// registration to sign in and refresh through.
type DelegationRef struct {
	BotID  uuid.UUID
	Tenant string
	App    msauth.App
}

// DelegationFor answers it for an organisation (internal/teamsuser asks).
func (s *Service) DelegationFor(ctx context.Context, org uuid.UUID) (DelegationRef, error) {
	b, err := s.BotOfOrg(ctx, org)
	if err != nil {
		return DelegationRef{}, err
	}
	return s.delegation(b), nil
}

// DelegationByBot answers it for a bot id.
func (s *Service) DelegationByBot(ctx context.Context, bot uuid.UUID) (DelegationRef, error) {
	b, err := s.BotByID(ctx, bot)
	if err != nil {
		return DelegationRef{}, err
	}
	return s.delegation(b), nil
}

func (s *Service) delegation(b Bot) DelegationRef {
	return DelegationRef{BotID: b.ID, Tenant: b.TenantID, App: b.DelegationApp(s.LoginBase)}
}
