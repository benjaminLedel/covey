// Package teamsapp brings an organisation's agents into Microsoft Teams
// through the organisation's own bot (#710, #715): one Azure registration per
// organisation, configured in the interface, serving every agent of it —
// instead of one bot per agent.
//
// The bot's credential reaches every conversation the bot is part of, so it
// stays in the control plane: deliveries are verified against the bot and
// routed to one of its organisation's agents (route.go), and every action an
// agent takes is resolved against that bot's own records before the
// credential is used (actions.go). Nothing an agent passes names a host.
package teamsapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"sync"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/secrets/sealbox"
)

// Service is every organisation's bot.
type Service struct {
	Store    Store
	Registry *agents.Registry
	Box      *sealbox.Box
	// Enabled answers whether the organisation has the teamsapp system
	// switched on in its store.
	Enabled func(ctx context.Context, orgID uuid.UUID) bool
	// Verify checks a delivery's token for a bot's app ID; the default is
	// the Bot Framework check. A test replaces it.
	Verify func(appID, authHeader, serviceURL string) error
	// Probe checks app ID, secret and tenant at saving; nil: ask Microsoft.
	Probe func(ctx context.Context, tenant, appID, secret string) error
	// LoginBase and TokenURL point at the Microsoft identity platform and
	// the Bot Framework token endpoint; empty: Microsoft's. Tests set them.
	LoginBase string
	TokenURL  string
	Log       *slog.Logger
	// People signs in the person writing (#713); nil: agents never act as
	// the writer.
	People People
	// ActsAsPerson answers whether an agent works with the writer's
	// identity (it holds teamsuser in ACCESS.md).
	ActsAsPerson func(ctx context.Context, agent agents.Agent) bool

	mu         sync.Mutex
	connectors map[uuid.UUID]*teams.Client
}

// New builds the service; nil without a master key to seal secrets with.
func New(pool *pgxpool.Pool, reg *agents.Registry, box *sealbox.Box, log *slog.Logger) *Service {
	if box == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		Store: Store{Pool: pool}, Registry: reg, Box: box, Log: log,
		Verify:     teams.VerifyActivity,
		connectors: map[uuid.UUID]*teams.Client{},
	}
}

// connector returns the bot's Bot Connector client, one per bot so its token
// is cached across calls.
func (s *Service) connector(b Bot) (*teams.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.connectors[b.ID]; ok {
		return c, nil
	}
	c, err := teams.NewClient(target.Credential{Token: b.AppID + ":" + b.secret, BaseURL: s.TokenURL})
	if err != nil {
		return nil, err
	}
	s.connectors[b.ID] = c
	return c, nil
}

func (s *Service) dropConnector(id uuid.UUID) {
	s.mu.Lock()
	delete(s.connectors, id)
	s.mu.Unlock()
}

// Say sends a plain message from the bot itself as a reply to the activity
// that asked.
func (s *Service) Say(ctx context.Context, b Bot, a teams.Activity, text string) error {
	c, err := s.connector(b)
	if err != nil {
		return err
	}
	_, err = c.Reply(ctx, a.ServiceURL, a.Conversation.ID, a.ID, text)
	return err
}

var errNoApp = errors.New("teamsapp: not available on this installation (no master key)")

// People is the half of "the agent works as the person who wrote" that
// holds tokens (internal/teamsuser).
type People interface {
	HasPerson(ctx context.Context, bot uuid.UUID, oid string) bool
	Exchange(ctx context.Context, b DelegationRef, oid, ssoToken string) error
	KeepPending(ctx context.Context, bot uuid.UUID, oid string, activity json.RawMessage) error
	TakePending(ctx context.Context, bot uuid.UUID, oid string) (json.RawMessage, bool)
	SetTaskPerson(ctx context.Context, taskID, bot uuid.UUID, oid string) error
}

// SSOResource is the application ID URI Teams issues the single sign-on
// token for: api://<host>/botid-<bot app id>. The same string goes into the
// app package's webApplicationInfo and the app registration.
func (b Bot) SSOResource(siteURL string) string {
	host := siteURL
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return "api://" + host + "/botid-" + b.AppID
}
