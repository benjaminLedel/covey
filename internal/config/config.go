// Package config lädt die Prozess-Konfiguration aus ENV (12-Factor).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// DatabaseURL ist die Postgres-DSN, z. B. postgres://covey:covey@localhost:5433/covey
	DatabaseURL string
	// ListenAddr ist die HTTP-Bind-Adresse des Binaries (API + eingebettetes Frontend).
	ListenAddr string
	// PublicURL ist die von Sandboxen erreichbare Basis-URL der Control Plane.
	PublicURL string
	// MasterKeyHex ist der 32-Byte-AES-Schlüssel (hex) des Built-in SecretStore.
	MasterKeyHex string
	// IdentityProvider wählt die Implementierung: builtin | oidc (post-MVP).
	IdentityProvider string
	// SecretStore wählt die Implementierung: builtin | vault (post-MVP).
	SecretStore string
	// SandboxProvider wählt die Data-Plane: local (Subprozess) | docker (Container) | e2b (post-MVP).
	SandboxProvider string
	// DataDir hält persistente Sandbox-Homes (local- und docker-Provider).
	DataDir string
	// CoveydPath ist der Pfad zum Daemon-Binary für den local-Provider.
	CoveydPath string
	// SandboxImage ist das Container-Image des docker-Providers (Dockerfile.sandbox).
	SandboxImage string
	// WebhookSecrets verifizieren Signaturen eingehender Zielsystem-Webhooks:
	// COVEY_<SYSTEM>_WEBHOOK_SECRET → Eintrag unter dem kleingeschriebenen
	// System-Namen (z. B. COVEY_ZAMMAD_WEBHOOK_SECRET → "zammad").
	WebhookSecrets map[string]string
	// TickInterval ist der periodische "was liegt an?"-Impuls des Dispatch-Loops.
	TickInterval time.Duration
	// SessionTTL für menschliche Logins.
	SessionTTL time.Duration
	// DaemonTokenTTL für die kurzlebigen Sandbox-Daemon-JWTs.
	DaemonTokenTTL time.Duration
	// EgressEnforce schaltet den Egress-Allowlist-Proxy ein (nur docker-Provider):
	// Sandbox-Verkehr geht dann über einen Proxy, der nur Allowlist-Hosts durchlässt.
	EgressEnforce bool
	// EgressAllow sind zusätzliche erlaubte Egress-Hosts (Zielsysteme wie das
	// Zammad-Host), zusätzlich zu den fest erlaubten Anthropic-Hosts.
	// COVEY_EGRESS_ALLOW="helpdesk.example.com,*.internal.example.com".
	EgressAllow []string
}

func FromEnv() (Config, error) {
	c := Config{
		DatabaseURL:         getenv("COVEY_DATABASE_URL", "postgres://covey:covey@localhost:5433/covey?sslmode=disable"),
		ListenAddr:          getenv("COVEY_LISTEN_ADDR", ":8494"),
		PublicURL:           getenv("COVEY_PUBLIC_URL", "http://localhost:8494"),
		MasterKeyHex:        os.Getenv("COVEY_MASTER_KEY"),
		IdentityProvider:    getenv("COVEY_IDENTITY_PROVIDER", "builtin"),
		SecretStore:         getenv("COVEY_SECRET_STORE", "builtin"),
		SandboxProvider:     getenv("COVEY_SANDBOX_PROVIDER", "local"),
		DataDir:             getenv("COVEY_DATA_DIR", "./data"),
		CoveydPath:          getenv("COVEY_COVEYD_PATH", "./coveyd"),
		SandboxImage:        getenv("COVEY_SANDBOX_IMAGE", "covey-sandbox:latest"),
		WebhookSecrets:      webhookSecretsFromEnv(),
		TickInterval:        getenvDuration("COVEY_TICK_INTERVAL", 30*time.Second),
		SessionTTL:          getenvDuration("COVEY_SESSION_TTL", 12*time.Hour),
		DaemonTokenTTL:      getenvDuration("COVEY_DAEMON_TOKEN_TTL", 15*time.Minute),
		EgressEnforce:       getenvBool("COVEY_EGRESS_ENFORCE", false),
		EgressAllow:         splitList(os.Getenv("COVEY_EGRESS_ALLOW")),
	}
	if c.IdentityProvider != "builtin" {
		return c, fmt.Errorf("identity provider %q: nur 'builtin' ist im MVP implementiert", c.IdentityProvider)
	}
	if c.SecretStore != "builtin" {
		return c, fmt.Errorf("secret store %q: nur 'builtin' ist im MVP implementiert", c.SecretStore)
	}
	return c, nil
}

// webhookSecretsFromEnv sammelt COVEY_<SYSTEM>_WEBHOOK_SECRET-Variablen ein —
// ein neues Zielsystem-Plugin braucht keinen neuen Config-Code.
func webhookSecretsFromEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		name, ok := strings.CutPrefix(k, "COVEY_")
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "_WEBHOOK_SECRET")
		if !ok || name == "" || v == "" {
			continue
		}
		out[strings.ToLower(name)] = v
	}
	return out
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// splitList zerlegt eine kommaseparierte ENV-Liste in getrimmte, nicht-leere Werte.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	return fallback
}
