package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/secrets/sealbox"
)

// Source says whose App an organisation is connected through.
const (
	SourceShared = "shared"
	SourceOwn    = "own"
)

// SharedApp is the platform's App, from the environment. Configured only when
// ID, Slug and the key are all present.
type SharedApp struct {
	App
	WebhookSecret string
	// The OAuth client of the App: installing it also authorises the
	// installing user, whose token proves the installation is theirs.
	ClientID     string
	ClientSecret string
	Web          string // empty = github.com
}

// Configured reports whether the platform offers its shared App.
func (s *SharedApp) Configured() bool {
	return s != nil && s.ID != 0 && s.Slug != "" && s.PrivateKey != nil && s.ClientID != "" && s.ClientSecret != ""
}

// Service binds GitHub Apps to organisations and mints their tokens.
type Service struct {
	Pool   *pgxpool.Pool
	Box    *sealbox.Box
	Client *Client
	Shared *SharedApp // nil or unconfigured: only own Apps
	Now    func() time.Time
	// API is GitHub's REST API for own Apps and manifest conversions; empty =
	// github.com. Tests point it at a stand-in.
	API string
}

// BaseURL is the API address the github plugin gets with a minted token:
// empty for github.com (the plugin's default).
func (s *Service) BaseURL() string {
	if s == nil || s.API == "" || strings.TrimRight(s.API, "/") == DefaultAPI {
		return ""
	}
	return s.API
}

// ErrNotConnected: the organisation has no (installed) GitHub App.
var ErrNotConnected = errors.New("no GitHub App connected")

// ErrInstallationTaken: the installation is bound to another organisation.
var ErrInstallationTaken = errors.New("this GitHub App installation is already connected to another organisation")

// Connection is an organisation's GitHub App, as the UI and the broker see it.
type Connection struct {
	OrgID          uuid.UUID `json:"-"`
	Source         string    `json:"source"`
	AppID          int64     `json:"app_id,omitempty"`
	AppSlug        string    `json:"app_slug"`
	AppHTMLURL     string    `json:"app_html_url,omitempty"`
	InstallationID int64     `json:"installation_id,omitempty"`
	AccountLogin   string    `json:"account_login,omitempty"`
	AccountType    string    `json:"account_type,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`

	app           App    // resolved credentials (own: unsealed; shared: env)
	webhookSecret string // own App's secret; shared: from env
}

// Installed reports whether tokens can be minted.
func (c Connection) Installed() bool { return c.InstallationID != 0 }

// BotLogin is the bot's GitHub login, e.g. "covey[bot]".
func (c Connection) BotLogin() string { return c.app.BotLogin() }

func aad(org uuid.UUID, what string) []byte {
	return []byte("github app|" + org.String() + "|" + what)
}

const selectConn = `SELECT org_id, source, app_id, app_slug, app_html_url, key_nonce, key_ct, hook_nonce, hook_ct,
	installation_id, account_login, account_type, updated_at FROM github_app_connections`

func (s *Service) scan(row pgx.Row) (Connection, error) {
	var (
		c              Connection
		appID, instID  *int64
		kn, kc, hn, hc []byte
	)
	if err := row.Scan(&c.OrgID, &c.Source, &appID, &c.AppSlug, &c.AppHTMLURL, &kn, &kc, &hn, &hc,
		&instID, &c.AccountLogin, &c.AccountType, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Connection{}, ErrNotConnected
		}
		return Connection{}, err
	}
	if instID != nil {
		c.InstallationID = *instID
	}
	switch c.Source {
	case SourceShared:
		if !s.Shared.Configured() {
			return Connection{}, fmt.Errorf("connected through the platform's GitHub App, which is not configured on this instance")
		}
		c.app, c.webhookSecret = s.Shared.App, s.Shared.WebhookSecret
		c.AppSlug, c.AppID = s.Shared.Slug, s.Shared.ID
	case SourceOwn:
		if appID != nil {
			c.AppID = *appID
		}
		pem, err := s.Box.Open("GitHub App key", aad(c.OrgID, "key"), kn, kc)
		if err != nil {
			return Connection{}, err
		}
		key, err := ParseKey(pem)
		if err != nil {
			return Connection{}, err
		}
		c.app = App{ID: c.AppID, Slug: c.AppSlug, PrivateKey: key, API: s.API}
		if hc != nil {
			if c.webhookSecret, err = s.Box.Open("GitHub App webhook secret", aad(c.OrgID, "hook"), hn, hc); err != nil {
				return Connection{}, err
			}
		}
	}
	return c, nil
}

// Get returns the organisation's connection (installed or not yet).
func (s *Service) Get(ctx context.Context, org uuid.UUID) (Connection, error) {
	if s == nil || s.Pool == nil {
		return Connection{}, ErrNotConnected
	}
	return s.scan(s.Pool.QueryRow(ctx, selectConn+` WHERE org_id = $1`, org))
}

// ByInstallation finds the organisation an installation belongs to — the
// routing key of the central webhook.
func (s *Service) ByInstallation(ctx context.Context, installationID int64) (Connection, error) {
	if s == nil || s.Pool == nil || installationID == 0 {
		return Connection{}, ErrNotConnected
	}
	return s.scan(s.Pool.QueryRow(ctx, selectConn+` WHERE installation_id = $1`, installationID))
}

// WebhookSecret is the secret the connection's webhooks are signed with.
func (c Connection) WebhookSecret() string { return c.webhookSecret }

// Token mints (or reuses) the installation token of the organisation's App.
// ErrNotConnected when there is no installed App — the caller then falls back
// to a stored github_token.
func (s *Service) Token(ctx context.Context, org uuid.UUID) (Token, Connection, error) {
	c, err := s.Get(ctx, org)
	if err != nil {
		return Token{}, Connection{}, err
	}
	if !c.Installed() {
		return Token{}, c, ErrNotConnected
	}
	t, err := s.Client.InstallationToken(ctx, c.app, c.InstallationID)
	var ae *APIError
	if errors.As(err, &ae) && (ae.Status == 404 || ae.Status == 401 || ae.Status == 403) {
		// Uninstalled or suspended on GitHub's side: say so plainly.
		return Token{}, c, fmt.Errorf("GitHub App installation %d is no longer usable (%s) — connect GitHub again", c.InstallationID, ae.Message)
	}
	return t, c, err
}

// --- the browser flows ------------------------------------------------------

// Flow is what a signed state carries through GitHub and back.
type Flow struct {
	Org    uuid.UUID `json:"o"`
	Kind   string    `json:"k"` // "install" | "manifest"
	Source string    `json:"s"` // shared | own
	Nonce  string    `json:"n"` // bound to a flow cookie in the starting browser
	Exp    int64     `json:"e"`
}

// FlowTTL is how long a started flow stays valid.
const FlowTTL = 30 * time.Minute

var stateAAD = []byte("github app flow state")

// NewState seals a flow into an opaque state and returns the nonce the caller
// puts into a flow cookie.
func (s *Service) NewState(org uuid.UUID, kind, source string) (state, nonce string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	f := Flow{Org: org, Kind: kind, Source: source, Nonce: base64.RawURLEncoding.EncodeToString(b), Exp: s.now().Add(FlowTTL).Unix()}
	raw, _ := json.Marshal(f)
	n, ct, err := s.Box.Seal(stateAAD, string(raw))
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(n) + "." + base64.RawURLEncoding.EncodeToString(ct), f.Nonce, nil
}

// OpenState checks a state handed back by GitHub: sealed by us, not expired,
// of the expected kind and started in this browser (cookieNonce).
func (s *Service) OpenState(state, kind, cookieNonce string) (Flow, error) {
	bad := errors.New("the GitHub flow is invalid or expired — start it again in covey")
	a, b, ok := strings.Cut(state, ".")
	if !ok {
		return Flow{}, bad
	}
	n, err1 := base64.RawURLEncoding.DecodeString(a)
	ct, err2 := base64.RawURLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil {
		return Flow{}, bad
	}
	raw, err := s.Box.Open("GitHub flow state", stateAAD, n, ct)
	if err != nil {
		return Flow{}, bad
	}
	var f Flow
	if json.Unmarshal([]byte(raw), &f) != nil || f.Kind != kind || s.now().Unix() > f.Exp {
		return Flow{}, bad
	}
	if cookieNonce == "" || cookieNonce != f.Nonce {
		return Flow{}, errors.New("the GitHub flow was started in another browser — start it again here")
	}
	return f, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// StoreOwnApp keeps an App created through the manifest flow. An existing
// connection of the organisation is replaced; its installation goes with it.
func (s *Service) StoreOwnApp(ctx context.Context, org uuid.UUID, app Created) error {
	if _, err := ParseKey(app.PEM); err != nil {
		return err
	}
	kn, kc, err := s.Box.Seal(aad(org, "key"), app.PEM)
	if err != nil {
		return err
	}
	var hn, hc []byte
	if app.WebhookSecret != "" {
		if hn, hc, err = s.Box.Seal(aad(org, "hook"), app.WebhookSecret); err != nil {
			return err
		}
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO github_app_connections
		  (org_id, source, app_id, app_slug, app_html_url, key_nonce, key_ct, hook_nonce, hook_ct)
		VALUES ($1,'own',$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (org_id) DO UPDATE SET source='own', app_id=EXCLUDED.app_id, app_slug=EXCLUDED.app_slug,
		  app_html_url=EXCLUDED.app_html_url, key_nonce=EXCLUDED.key_nonce, key_ct=EXCLUDED.key_ct,
		  hook_nonce=EXCLUDED.hook_nonce, hook_ct=EXCLUDED.hook_ct,
		  installation_id=NULL, account_login='', account_type='', updated_at=now()`,
		org, app.ID, app.Slug, app.HTMLURL, kn, kc, hn, hc)
	return err
}

// AppFor returns the App an install flow of the given source completes
// against: the shared one, or the organisation's own (which must exist).
func (s *Service) AppFor(ctx context.Context, org uuid.UUID, source string) (App, error) {
	if source == SourceShared {
		if !s.Shared.Configured() {
			return App{}, errors.New("this instance offers no shared GitHub App")
		}
		return s.Shared.App, nil
	}
	c, err := s.Get(ctx, org)
	if err != nil || c.Source != SourceOwn {
		return App{}, errors.New("create the organisation's own GitHub App first")
	}
	return c.app, nil
}

// CompleteInstall binds an installation to the organisation — after checking
// with GitHub, under the App's own JWT, that the installation exists and
// belongs to that App. The id from the browser alone proves nothing.
//
// For the shared App the installing user must also prove the installation is
// theirs: oauthCode is the code GitHub appended to the redirect.
func (s *Service) CompleteInstall(ctx context.Context, org uuid.UUID, source string, installationID int64, oauthCode string) (Connection, error) {
	app, err := s.AppFor(ctx, org, source)
	if err != nil {
		return Connection{}, err
	}
	if source == SourceShared {
		if oauthCode == "" {
			return Connection{}, errors.New("GitHub sent no authorisation code — the shared App must request user authorisation during installation")
		}
		userToken, err := s.Client.ExchangeCode(ctx, s.Shared.Web, s.Shared.ClientID, s.Shared.ClientSecret, oauthCode)
		if err != nil {
			return Connection{}, err
		}
		ok, err := s.Client.UserCanSee(ctx, app.API, userToken, app.ID, installationID)
		if err != nil {
			return Connection{}, err
		}
		if !ok {
			return Connection{}, fmt.Errorf("installation %d is not one of yours on GitHub", installationID)
		}
	}
	in, err := s.Client.GetInstallation(ctx, app, installationID)
	if err != nil {
		return Connection{}, fmt.Errorf("GitHub does not confirm installation %d for this App: %w", installationID, err)
	}
	if source == SourceShared {
		_, err = s.Pool.Exec(ctx, `INSERT INTO github_app_connections (org_id, source, installation_id, account_login, account_type)
			VALUES ($1,'shared',$2,$3,$4)
			ON CONFLICT (org_id) DO UPDATE SET source='shared', app_id=NULL, app_slug='', app_html_url='',
			  key_nonce=NULL, key_ct=NULL, hook_nonce=NULL, hook_ct=NULL,
			  installation_id=EXCLUDED.installation_id, account_login=EXCLUDED.account_login,
			  account_type=EXCLUDED.account_type, updated_at=now()`,
			org, in.ID, in.Account.Login, in.Account.Type)
	} else {
		_, err = s.Pool.Exec(ctx, `UPDATE github_app_connections SET installation_id=$2, account_login=$3, account_type=$4, updated_at=now()
			WHERE org_id=$1 AND source='own'`, org, in.ID, in.Account.Login, in.Account.Type)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return Connection{}, ErrInstallationTaken
	}
	if err != nil {
		return Connection{}, err
	}
	return s.Get(ctx, org)
}

// Disconnect removes the organisation's connection. The App and its
// installation on GitHub stay; GitHub is where they are removed.
func (s *Service) Disconnect(ctx context.Context, org uuid.UUID) error {
	c, err := s.Get(ctx, org)
	if err == nil && c.Installed() {
		s.Client.Forget(c.app, c.InstallationID)
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM github_app_connections WHERE org_id=$1`, org)
	return err
}

// Uninstalled clears an installation GitHub reported as deleted.
func (s *Service) Uninstalled(ctx context.Context, installationID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE github_app_connections SET installation_id=NULL, account_login='', account_type='', updated_at=now()
		WHERE installation_id=$1`, installationID)
	return err
}
