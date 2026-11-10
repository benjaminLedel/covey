package teamsapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"covey/internal/msauth"
)

// An organisation's bot (#715): its own Azure app registration, entered in
// the interface by an org admin. The secrets are sealed with the master key;
// nothing about them is ever shown again.

var (
	// ErrNoBot: the organisation has not configured a Teams bot.
	ErrNoBot = errors.New("teamsapp: this organisation has no Teams bot configured")
	// ErrAppTaken: another organisation configured this app ID.
	ErrAppTaken = errors.New("teamsapp: this app ID is configured by another organisation")
)

// Bot is an organisation's bot as the interface shows it — never a secret.
type Bot struct {
	ID            uuid.UUID `json:"id"`
	OrgID         uuid.UUID `json:"org_id"`
	Name          string    `json:"name"`
	AppID         string    `json:"app_id"`
	TenantID      string    `json:"tenant_id"`
	SSOConnection string    `json:"sso_connection"`
	// DelegAppID is the app registration delegated access uses; empty: the
	// bot's own.
	DelegAppID string    `json:"deleg_app_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`

	secret      string
	delegSecret string
}

// BotInput is what an org admin enters. Empty secrets on an update keep the
// stored ones.
type BotInput struct {
	Name          string `json:"name"`
	AppID         string `json:"app_id"`
	TenantID      string `json:"tenant_id"`
	Secret        string `json:"secret"`
	SSOConnection string `json:"sso_connection"`
	DelegAppID    string `json:"deleg_app_id"`
	DelegSecret   string `json:"deleg_secret"`
}

func botAAD(id uuid.UUID, what string) []byte {
	return []byte("teamsapp-bot:" + what + ":" + id.String())
}

const botCols = `id, org_id, name, app_id, tenant_id, sso_connection, deleg_app_id, updated_at,
	secret_nonce, secret_ct, deleg_nonce, deleg_ct`

func (s *Service) scanBot(row pgx.Row) (Bot, error) {
	var b Bot
	var sn, sc, dn, dc []byte
	err := row.Scan(&b.ID, &b.OrgID, &b.Name, &b.AppID, &b.TenantID, &b.SSOConnection, &b.DelegAppID, &b.UpdatedAt,
		&sn, &sc, &dn, &dc)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNoBot
	}
	if err != nil {
		return b, err
	}
	if b.secret, err = s.Box.Open("teams bot secret", botAAD(b.ID, "secret"), sn, sc); err != nil {
		return b, err
	}
	if b.DelegAppID != "" && len(dc) > 0 {
		if b.delegSecret, err = s.Box.Open("teams delegation secret", botAAD(b.ID, "deleg"), dn, dc); err != nil {
			return b, err
		}
	}
	return b, nil
}

// BotOfOrg is the organisation's bot.
func (s *Service) BotOfOrg(ctx context.Context, org uuid.UUID) (Bot, error) {
	if s == nil {
		return Bot{}, ErrNoBot
	}
	return s.scanBot(s.Store.Pool.QueryRow(ctx, `SELECT `+botCols+` FROM teamsapp_bots WHERE org_id = $1`, org))
}

// BotByID is a bot by the id in its messaging endpoint.
func (s *Service) BotByID(ctx context.Context, id uuid.UUID) (Bot, error) {
	if s == nil {
		return Bot{}, ErrNoBot
	}
	return s.scanBot(s.Store.Pool.QueryRow(ctx, `SELECT `+botCols+` FROM teamsapp_bots WHERE id = $1`, id))
}

// SaveBot creates or updates the organisation's bot. Before anything is
// stored, Microsoft is asked whether app ID, secret and tenant belong
// together: a client-credentials token from that tenant. A typo then fails
// here, with Microsoft's reason, and not at the first message.
func (s *Service) SaveBot(ctx context.Context, org uuid.UUID, in BotInput) (Bot, error) {
	if s == nil {
		return Bot{}, errNoApp
	}
	in.Name, in.AppID, in.TenantID = strings.TrimSpace(in.Name), strings.TrimSpace(in.AppID), strings.TrimSpace(in.TenantID)
	in.SSOConnection, in.DelegAppID = strings.TrimSpace(in.SSOConnection), strings.TrimSpace(in.DelegAppID)
	if in.Name == "" {
		in.Name = "covey"
	}
	if in.AppID == "" || in.TenantID == "" {
		return Bot{}, fmt.Errorf("teamsapp: app ID and tenant ID are required")
	}
	if _, err := uuid.Parse(in.AppID); err != nil {
		return Bot{}, fmt.Errorf("teamsapp: the app ID is the registration's application (client) ID, a GUID")
	}
	existing, err := s.BotOfOrg(ctx, org)
	if err != nil && !errors.Is(err, ErrNoBot) {
		return Bot{}, err
	}
	id := existing.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	secret := in.Secret
	if secret == "" {
		if existing.AppID != in.AppID {
			return Bot{}, fmt.Errorf("teamsapp: the client secret is required")
		}
		secret = existing.secret
	}
	delegSecret := in.DelegSecret
	if in.DelegAppID != "" && delegSecret == "" {
		if existing.DelegAppID != in.DelegAppID {
			return Bot{}, fmt.Errorf("teamsapp: the delegation app's client secret is required")
		}
		delegSecret = existing.delegSecret
	}
	if err := s.probe(ctx, in.TenantID, in.AppID, secret); err != nil {
		return Bot{}, err
	}
	if in.DelegAppID != "" {
		if err := s.probe(ctx, in.TenantID, in.DelegAppID, delegSecret); err != nil {
			return Bot{}, fmt.Errorf("delegation app: %w", err)
		}
	}
	sn, sc, err := s.Box.Seal(botAAD(id, "secret"), secret)
	if err != nil {
		return Bot{}, err
	}
	var dn, dc []byte
	if in.DelegAppID != "" {
		if dn, dc, err = s.Box.Seal(botAAD(id, "deleg"), delegSecret); err != nil {
			return Bot{}, err
		}
	}
	_, err = s.Store.Pool.Exec(ctx, `INSERT INTO teamsapp_bots
		  (id, org_id, name, app_id, tenant_id, secret_nonce, secret_ct, sso_connection, deleg_app_id, deleg_nonce, deleg_ct)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (org_id) DO UPDATE SET name = EXCLUDED.name, app_id = EXCLUDED.app_id, tenant_id = EXCLUDED.tenant_id,
		  secret_nonce = EXCLUDED.secret_nonce, secret_ct = EXCLUDED.secret_ct, sso_connection = EXCLUDED.sso_connection,
		  deleg_app_id = EXCLUDED.deleg_app_id, deleg_nonce = EXCLUDED.deleg_nonce, deleg_ct = EXCLUDED.deleg_ct,
		  updated_at = now()`,
		id, org, in.Name, in.AppID, in.TenantID, sn, sc, in.SSOConnection, in.DelegAppID, dn, dc)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return Bot{}, ErrAppTaken
	}
	if err != nil {
		return Bot{}, err
	}
	s.dropConnector(id)
	return s.BotOfOrg(ctx, org)
}

// DeleteBot removes the organisation's bot and everything it knew.
func (s *Service) DeleteBot(ctx context.Context, org uuid.UUID) error {
	if s == nil {
		return ErrNoBot
	}
	var id uuid.UUID
	err := s.Store.Pool.QueryRow(ctx, `DELETE FROM teamsapp_bots WHERE org_id = $1 RETURNING id`, org).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoBot
	}
	if err == nil {
		s.dropConnector(id)
	}
	return err
}

// probe asks Microsoft for a client-credentials token of the app in the
// tenant: it exists there and the secret is right.
func (s *Service) probe(ctx context.Context, tenant, appID, secret string) error {
	if s.Probe != nil {
		return s.Probe(ctx, tenant, appID, secret)
	}
	app := msauth.App{ClientID: appID, Secret: secret, LoginBase: s.LoginBase}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := app.ClientCredentials(ctx, tenant, "https://graph.microsoft.com/.default"); err != nil {
		return fmt.Errorf("teamsapp: Microsoft did not accept app ID, secret and tenant together — %w", err)
	}
	return nil
}

// botApp is the bot's own registration.
func (b Bot) botApp(loginBase string) msauth.App {
	return msauth.App{ClientID: b.AppID, Secret: b.secret, LoginBase: loginBase, Purpose: "teamsapp-person:" + b.ID.String()}
}

// DelegationApp is the registration delegated access runs through: the one
// configured for it, or the bot's own.
func (b Bot) DelegationApp(loginBase string) msauth.App {
	if b.DelegAppID != "" {
		return msauth.App{ClientID: b.DelegAppID, Secret: b.delegSecret, LoginBase: loginBase, Purpose: "teamsapp-person:" + b.ID.String()}
	}
	return b.botApp(loginBase)
}

// SSOConnectionName is the bot's OAuth connection for single sign-on.
func (b Bot) SSOConnectionName() string {
	if b.SSOConnection != "" {
		return b.SSOConnection
	}
	return "covey"
}

// SSOAppID is the app Teams issues the single sign-on token for — the
// delegation app if there is one.
func (b Bot) SSOAppID() string {
	if b.DelegAppID != "" {
		return b.DelegAppID
	}
	return b.AppID
}
