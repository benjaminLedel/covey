package settings

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"
)

func serviceAccount(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "covey-test", "client_email": "push@covey-test.iam.example.org",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	return string(raw)
}

func TestValidatePush(t *testing.T) {
	for key, good := range map[string][]string{
		PushMode:        {"", "direct", "relay", "off"},
		PushRelayURL:    {"", "https://app.covey.work", "https://relay.example.org/covey"},
		PushRelayAccept: {"", On, Off},
	} {
		for _, v := range good {
			if err := validate(key, v); err != nil {
				t.Errorf("%s=%q was refused: %v", key, v, err)
			}
		}
	}
	for key, bad := range map[string][]string{
		PushMode: {"apns", "on"},
		// What travels names the agent and, with the preview, what it said.
		PushRelayURL:    {"http://app.covey.work", "app.covey.work", "https://", "https://relay.example.org?x=1"},
		PushRelayAccept: {"true", "ja"},
	} {
		for _, v := range bad {
			if err := validate(key, v); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s=%q was accepted", key, v)
			}
		}
	}
}

func TestParseServiceAccount(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	sa, err := ParseServiceAccount(serviceAccount(t, rk))
	if err != nil {
		t.Fatal(err)
	}
	if sa.ProjectID != "covey-test" || sa.ClientEmail != "push@covey-test.iam.example.org" ||
		sa.TokenURI != "https://oauth2.googleapis.com/token" || sa.Key == nil {
		t.Fatalf("%+v", sa)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for name, raw := range map[string]string{
		"not JSON":             "service-account",
		"google-services.json": `{"project_info":{"project_id":"covey-test"}}`,
		"no key":               `{"type":"service_account","project_id":"p","client_email":"e@example.org"}`,
		"key not PEM":          `{"type":"service_account","project_id":"p","client_email":"e@example.org","private_key":"abc"}`,
		"an EC key":            serviceAccount(t, ec),
	} {
		if _, err := ParseServiceAccount(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was accepted (err=%v)", name, err)
		}
	}
}

// A store refuses a wrong file before it seals anything — the check runs
// ahead of the database, so a store without one shows it.
func TestSetSecretChecksTheServiceAccount(t *testing.T) {
	s := &Store{}
	if err := s.SetSecret(context.Background(), PushCredentials, `{"project_info":{}}`, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

// What the environment says applies where nothing is set.
func TestPushDefaultsFromTheEnvironment(t *testing.T) {
	ctx := context.Background()
	var s *Store
	for name, c := range map[string]struct {
		env    PushEnv
		mode   string
		relay  string
		accept bool
		from   string
	}{
		"nothing":            {PushEnv{}, PushRelay, DefaultPushRelay, false, ""},
		"relay off":          {PushEnv{RelayURL: "off"}, PushOff, DefaultPushRelay, false, ""},
		"another relay":      {PushEnv{RelayURL: "https://relay.example.org/"}, PushRelay, "https://relay.example.org", false, ""},
		"an account":         {PushEnv{Credentials: "{}", RelayAccept: true}, PushDirect, DefaultPushRelay, true, "environment"},
		"account, relay off": {PushEnv{Credentials: "{}", RelayURL: "off"}, PushDirect, DefaultPushRelay, false, "environment"},
	} {
		p, err := s.Push(ctx, c.env)
		if err != nil {
			t.Fatal(err)
		}
		if p.Mode != c.mode || p.RelayURL != c.relay || p.RelayAccept != c.accept || p.CredentialsFrom != c.from {
			t.Errorf("%s: %+v", name, p)
		}
	}
}
