package settings

// How this installation sends push notifications (#431).
//
// One sender for the iPhone and Android alike: Firebase Cloud Messaging with
// a service account of the app's Firebase project, which passes an iPhone's
// notification on to Apple with the APNs key uploaded there. Whoever does not
// hold such an account — every installation running the app from the stores
// — hands its notifications to a relay that does.
//
// The keys sit here, beside the mail, for the reason the mail's do: an
// administrator switches push on where they see whether it is on, without a
// shell on the host and without a restart. The environment variables
// (COVEY_FCM_CREDENTIALS_FILE, COVEY_PUSH_RELAY, COVEY_PUSH_RELAY_ACCEPT)
// stay as defaults for an installation configured as code: an empty setting
// means "what the environment says", a stored one wins.

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// PushMode: direct | relay | off. Direct sends with this installation's
	// own service account, relay hands every notification to PushRelayURL.
	PushMode = "push.mode"
	// PushRelayURL is the instance notifications are handed to in relay
	// mode. Empty = the environment's, and failing that DefaultPushRelay.
	PushRelayURL = "push.relay_url"
	// PushRelayAccept: on | off — whether this installation delivers for
	// others. Meaningful only in direct mode: a relay without an account of
	// its own would only pass the notification on once more.
	PushRelayAccept = "push.relay_accept"
	// PushCredentials is the service account JSON. Sealed like the SMTP
	// password and never handed back; the API names only its project and
	// account, which are not secret and say which project is configured.
	// #nosec G101 — the key the account is stored under, not the account.
	PushCredentials = "push.fcm_credentials"
	// PushLastTestAt, PushLastTestError: what the last test did — written by
	// the installation, as the mail test's are.
	PushLastTestAt    = "push.last_test_at"
	PushLastTestError = "push.last_test_error"
)

// The modes of PushMode.
const (
	PushDirect = "direct"
	PushRelay  = "relay"
	PushOff    = "off"
)

// DefaultPushRelay is the instance that holds the service account of the
// app in the stores and relays for installations without one (#379).
const DefaultPushRelay = "https://app.covey.work"

func init() {
	for k, v := range map[string]string{
		PushMode: "", PushRelayURL: "", PushRelayAccept: "", PushCredentials: "",
		PushLastTestAt: "", PushLastTestError: "",
	} {
		Defaults[k] = v
	}
	Secrets[PushCredentials] = true
	ReadOnly[PushLastTestAt] = true
	ReadOnly[PushLastTestError] = true
}

func validatePush(key, value string) error {
	// Empty is allowed everywhere and hands the key back to the environment.
	if value == "" {
		return nil
	}
	switch key {
	case PushMode:
		switch value {
		case PushDirect, PushRelay, PushOff:
			return nil
		}
		return fmt.Errorf("%w: %s must be one of direct|relay|off", ErrInvalid, key)
	case PushRelayURL:
		// https only: what travels is who wrote and, with the preview on,
		// the first line of it.
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%w: %s must be an https address such as %s", ErrInvalid, key, DefaultPushRelay)
		}
	case PushRelayAccept:
		if value != On && value != Off {
			return fmt.Errorf("%w: %s must be on or off", ErrInvalid, key)
		}
	}
	return nil
}

// ServiceAccount is what a sender needs of a Google service account key
// file: whom it signs as, for which project, with which key.
type ServiceAccount struct {
	ProjectID   string
	ClientEmail string
	TokenURI    string
	Key         *rsa.PrivateKey
}

// ParseServiceAccount reads the JSON file the Firebase console hands out
// under Project settings → Service accounts. The app's own
// google-services.json or GoogleService-Info.plist is the most likely
// mistake, and it is refused here, when it is uploaded, rather than on the
// first notification.
func ParseServiceAccount(raw string) (ServiceAccount, error) {
	var sa struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		PrivateKey  string `json:"private_key"`
		ClientEmail string `json:"client_email"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return ServiceAccount{}, fmt.Errorf("%w: the service account is not JSON: %v", ErrInvalid, err)
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return ServiceAccount{}, fmt.Errorf("%w: expected a service account key file (type service_account, project_id, client_email, private_key)", ErrInvalid)
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return ServiceAccount{}, fmt.Errorf("%w: the service account's private_key is not PEM", ErrInvalid)
	}
	var rk *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if rk, ok = k.(*rsa.PrivateKey); !ok {
			return ServiceAccount{}, fmt.Errorf("%w: the service account's private_key is not an RSA key", ErrInvalid)
		}
	} else if rk, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return ServiceAccount{}, fmt.Errorf("%w: the service account's private_key: %v", ErrInvalid, err)
	}
	return ServiceAccount{ProjectID: sa.ProjectID, ClientEmail: sa.ClientEmail, TokenURI: sa.TokenURI, Key: rk}, nil
}

// PushEnv is what the environment says, handed in by the caller: the
// defaults for every push key that is not set here.
type PushEnv struct {
	// RelayURL is COVEY_PUSH_RELAY: empty for the default, "off" for no push
	// unless there is an account.
	RelayURL    string
	RelayAccept bool
	// Credentials is the content of COVEY_FCM_CREDENTIALS_FILE, read once at
	// startup.
	Credentials string
}

// Push is the effective configuration.
type Push struct {
	Mode        string
	RelayURL    string
	RelayAccept bool
	// Credentials is the service account JSON, empty for none;
	// CredentialsFrom says where it came from: "settings" or "environment".
	Credentials     string
	CredentialsFrom string
	LastTestAt      string
	LastTestError   string
}

// Push reads the configuration, the sealed account included. Called per
// round of the notifier: a change applies without a restart.
func (s *Store) Push(ctx context.Context, env PushEnv) (Push, error) {
	all, err := s.All(ctx)
	if err != nil {
		return Push{}, err
	}
	p := Push{LastTestAt: all[PushLastTestAt], LastTestError: all[PushLastTestError]}
	creds, err := s.GetSecret(ctx, PushCredentials)
	if err != nil {
		return p, err
	}
	switch {
	case creds != "":
		p.Credentials, p.CredentialsFrom = creds, "settings"
	case strings.TrimSpace(env.Credentials) != "":
		p.Credentials, p.CredentialsFrom = env.Credentials, "environment"
	}
	envRelay := strings.TrimSpace(env.RelayURL)
	p.Mode = all[PushMode]
	if p.Mode == "" {
		switch {
		case strings.TrimSpace(env.Credentials) != "":
			p.Mode = PushDirect
		case strings.EqualFold(envRelay, Off):
			p.Mode = PushOff
		default:
			p.Mode = PushRelay
		}
	}
	p.RelayURL = all[PushRelayURL]
	if p.RelayURL == "" && envRelay != "" && !strings.EqualFold(envRelay, Off) {
		p.RelayURL = envRelay
	}
	if p.RelayURL == "" {
		p.RelayURL = DefaultPushRelay
	}
	p.RelayURL = strings.TrimRight(p.RelayURL, "/")
	switch all[PushRelayAccept] {
	case On:
		p.RelayAccept = true
	case Off:
		p.RelayAccept = false
	default:
		p.RelayAccept = env.RelayAccept
	}
	return p, nil
}

// RecordPushTest writes down what the last test did; `testErr` empty means
// it worked.
func (s *Store) RecordPushTest(ctx context.Context, at time.Time, testErr string) error {
	for key, value := range map[string]string{
		PushLastTestAt:    at.UTC().Format(time.RFC3339),
		PushLastTestError: testErr,
	} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO system_settings (key, value, updated_at) VALUES ($1,$2,now())
			 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`,
			key, value); err != nil {
			return err
		}
	}
	return nil
}
