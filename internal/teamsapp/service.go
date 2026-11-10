// Package teamsapp is the installation's own Microsoft Teams app (#710): one
// bot registration serving every organisation on the installation, instead
// of one Azure bot per agent.
//
// The split that makes it safe: the bot's credential can post into every
// conversation in every tenant that installed the app, so it stays in the
// control plane. An organisation proves which Microsoft tenant is its own
// (link.go), every delivery is routed by that tenant to the organisation and
// then to one of its agents (route.go), and every action an agent takes is
// resolved against the organisation's own records before the credential is
// used (actions.go). Nothing an agent passes names a host.
package teamsapp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
)

// Config is the installation's app registration.
type Config struct {
	AppID       string
	AppPassword string
	// TokenURL overrides the Bot Framework token endpoint (tests, sovereign
	// clouds). Empty: the multi-tenant default.
	TokenURL string
	// LoginBase is the Microsoft identity platform the tenant link signs in
	// against. Empty: https://login.microsoftonline.com.
	LoginBase string
}

// Service is the shared bot.
type Service struct {
	Store    Store
	Registry *agents.Registry
	// Enabled answers whether the organisation has the teamsapp system
	// switched on in its store.
	Enabled func(ctx context.Context, orgID uuid.UUID) bool
	// Verify checks a delivery's token; the default is the Bot Framework
	// check against the app ID. A test replaces it.
	Verify func(authHeader, serviceURL string) error
	Log    *slog.Logger

	cfg    Config
	mu     sync.Mutex
	client *teams.Client
}

// New builds the service, or nil when the installation has no app configured.
func New(pool *pgxpool.Pool, reg *agents.Registry, cfg Config, log *slog.Logger) *Service {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AppPassword) == "" {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{Store: Store{Pool: pool}, Registry: reg, Log: log, cfg: cfg}
	s.Verify = func(auth, serviceURL string) error { return teams.VerifyActivity(cfg.AppID, auth, serviceURL) }
	return s
}

// AppID is the app registration's ID (for the app package and the link).
func (s *Service) AppID() string { return s.cfg.AppID }

// connector returns the one Bot Connector client, so its token is cached
// across calls.
func (s *Service) connector() (*teams.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := teams.NewClient(target.Credential{Token: s.cfg.AppID + ":" + s.cfg.AppPassword, BaseURL: s.cfg.TokenURL})
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

// Say sends a plain message from the bot itself (the chooser, "not linked")
// as a reply to the activity that asked.
func (s *Service) Say(ctx context.Context, a teams.Activity, text string) error {
	c, err := s.connector()
	if err != nil {
		return err
	}
	_, err = c.Reply(ctx, a.ServiceURL, a.Conversation.ID, a.ID, text)
	return err
}

var errNoApp = errors.New("teamsapp: no app configured on this installation")
