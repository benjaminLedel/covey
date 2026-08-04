// Package config loads the process configuration from ENV (12-factor).
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// DatabaseURL is the Postgres DSN, e.g. postgres://covey:covey@localhost:5433/covey
	DatabaseURL string
	// ListenAddr is the binary's HTTP bind address (API + embedded frontend).
	ListenAddr string
	// PublicURL is the control plane's base URL as reachable from sandboxes.
	//
	// CAUTION, this is an operational address, not a marketing address: it is
	// what COVEY_WS_URL is built from, the URL every sandbox uses to connect
	// back (cmd/covey/main.go). With the docker provider a loopback host is
	// rewritten to host.docker.internal — a public name is not. Whoever puts
	// the website's domain here sends every sandbox back over the open
	// network, where the egress allowlist stops it; the agents then fail with
	// "daemon did not connect".
	// The address of the public website is SiteURL.
	PublicURL string

	// SiteURL is the address under which the public website is reachable —
	// the basis for canonical, hreflang, sitemap.xml and robots.txt.
	// Empty (the default) means: derive it from the request (Host +
	// X-Forwarded-Proto), which does the right thing behind a properly
	// configured reverse proxy. Set it only when the proxy does not pass the
	// origin through.
	SiteURL string
	// CookieSecure sets the Secure flag on the session cookie (delivered over
	// HTTPS only). Default: derived automatically from the PublicURL scheme
	// (https → true), overridable via COVEY_COOKIE_SECURE.
	CookieSecure bool
	// MasterKeyHex is the 32-byte AES key (hex) of the built-in SecretStore.
	MasterKeyHex string
	// IdentityProvider selects the implementation: builtin | oidc (post-MVP).
	IdentityProvider string
	// SecretStore selects the implementation: builtin | vault (post-MVP).
	SecretStore string
	// SandboxProvider selects the data plane: docker (container) | e2b (post-MVP).
	SandboxProvider string
	// DataDir holds the persistent sandbox homes (docker provider).
	DataDir string
	// SandboxImage is the container image of the docker provider (Dockerfile.sandbox).
	SandboxImage string
	// WebhookSecrets verify signatures of incoming target-system webhooks:
	// COVEY_<SYSTEM>_WEBHOOK_SECRET → entry under the lowercased system name
	// (e.g. COVEY_ZAMMAD_WEBHOOK_SECRET → "zammad").
	WebhookSecrets map[string]string
	// TickInterval is the dispatch loop's periodic "anything to do?" impulse.
	TickInterval time.Duration

	// DreamAt is the local time ("03:00") from which agents tidy up their
	// memory at night. Empty or "off" disables the nightly run — every dream
	// costs an LLM call, and whoever does not want that should be able to turn
	// it off without stripping the feature from the binary.
	DreamAt string
	// SessionTTL for human logins.
	SessionTTL time.Duration
	// DaemonTokenTTL for the short-lived sandbox daemon JWTs.
	DaemonTokenTTL time.Duration
	// BoardRetention is the age at which the control plane archives (does not
	// delete) a terminal task on its own and clears away the agent column left
	// empty by it — so the board tidies itself up without anyone's help.
	// COVEY_BOARD_RETENTION (default 24h); a negative duration disables the
	// cleanup and lets the board grow.
	BoardRetention time.Duration
	// EgressEnforce enables the egress allowlist proxy (docker provider only):
	// sandbox traffic then goes through a proxy that only lets allowlist hosts pass.
	EgressEnforce bool
	// EgressAllow are additional permitted egress hosts (target systems such as
	// the Zammad host), on top of the permanently allowed Anthropic hosts.
	// COVEY_EGRESS_ALLOW="helpdesk.example.com,*.internal.example.com".
	EgressAllow []string
	// EgressIsolation selects how strongly egress is enforced (docker provider):
	// "proxy" (default, cooperative via HTTP_PROXY) | "network" (hard: sandbox on
	// an internal network without internet, the proxy container as the only exit).
	EgressIsolation string
	// EgressProxyAddr is the bind address of the standalone egress proxy
	// (subcommand `covey egress-proxy`, a container in network mode).
	EgressProxyAddr string
	// RequestLog enables the request log (on by default): the HTTP requests at
	// the platform's edges — incoming webhooks and outgoing target-system
	// calls — end up in the request_log table and can be inspected under
	// Platform → Requests. COVEY_REQUEST_LOG=false turns it off.
	RequestLog bool
	// RequestLogBodies stores the (truncated, redacted) request/response
	// bodies in addition to the metadata. On by default — without them,
	// debugging a target-system API is usually worthless. Whoever does not
	// want payloads (chat messages, ticket texts) in the diagnostics table
	// sets COVEY_REQUEST_LOG_BODIES=false.
	RequestLogBodies bool
	// RequestLogRetention is the age at which log entries disappear
	// (COVEY_REQUEST_LOG_RETENTION, default 72h). A hard row cap in the store
	// applies on top of it.
	RequestLogRetention time.Duration
	// WikiCleanup is the schedule of the platform-wide wiki cleanup heartbeat:
	// empty = off. Otherwise "HH:MM" (daily, server time) or a Go duration such
	// as "24h" (interval). From it the control plane creates a recurring
	// backlog task for every agent in which the agent maintains its wiki (merge
	// duplicates, fix dead links). Overridable per agent via HEARTBEAT.md.
	// COVEY_WIKI_CLEANUP.
	WikiCleanup string
	// EmbeddingProvider selects the embedding behind wiki retrieval:
	// "builtin" (hash, offline, lexical only — default), "ollama" (self-hosted,
	// no key) or the third-party services "voyage"/"openai", which need
	// EmbeddingAPIKey. Switching re-embeds the existing corpus in the
	// background. COVEY_EMBEDDING_PROVIDER.
	EmbeddingProvider string
	// EmbeddingModel overrides the provider's default model.
	// COVEY_EMBEDDING_MODEL.
	EmbeddingModel string
	// EmbeddingAPIKey is the provider's key. COVEY_EMBEDDING_API_KEY.
	EmbeddingAPIKey string
	// EmbeddingURL overrides the endpoint (proxy, Azure, compatible services).
	// COVEY_EMBEDDING_URL.
	EmbeddingURL string
}

func FromEnv() (Config, error) {
	c := Config{
		DatabaseURL:      getenv("COVEY_DATABASE_URL", "postgres://covey:covey@localhost:5433/covey?sslmode=disable"),
		ListenAddr:       getenv("COVEY_LISTEN_ADDR", ":8494"),
		PublicURL:        getenv("COVEY_PUBLIC_URL", "http://localhost:8494"),
		SiteURL:          os.Getenv("COVEY_SITE_URL"),
		MasterKeyHex:     os.Getenv("COVEY_MASTER_KEY"),
		IdentityProvider: getenv("COVEY_IDENTITY_PROVIDER", "builtin"),
		SecretStore:      getenv("COVEY_SECRET_STORE", "builtin"),
		SandboxProvider:  getenv("COVEY_SANDBOX_PROVIDER", "docker"),
		DataDir:          getenv("COVEY_DATA_DIR", "./data"),
		SandboxImage:     getenv("COVEY_SANDBOX_IMAGE", "covey-sandbox:latest"),
		WebhookSecrets:   webhookSecretsFromEnv(),
		TickInterval:     getenvDuration("COVEY_TICK_INTERVAL", 30*time.Second),
		DreamAt:          getenv("COVEY_DREAM_AT", "03:00"),
		SessionTTL:       getenvDuration("COVEY_SESSION_TTL", 12*time.Hour),
		DaemonTokenTTL:   getenvDuration("COVEY_DAEMON_TOKEN_TTL", 15*time.Minute),
		BoardRetention:   getenvDuration("COVEY_BOARD_RETENTION", 24*time.Hour),
		EgressEnforce:    getenvBool("COVEY_EGRESS_ENFORCE", false),
		EgressAllow:      splitList(os.Getenv("COVEY_EGRESS_ALLOW")),
		EgressIsolation:  getenv("COVEY_EGRESS_ISOLATION", "proxy"),
		EgressProxyAddr:  getenv("COVEY_EGRESS_PROXY_ADDR", ":8888"),
		WikiCleanup:      strings.TrimSpace(os.Getenv("COVEY_WIKI_CLEANUP")),

		EmbeddingProvider: getenv("COVEY_EMBEDDING_PROVIDER", "builtin"),
		EmbeddingModel:    strings.TrimSpace(os.Getenv("COVEY_EMBEDDING_MODEL")),
		EmbeddingAPIKey:   strings.TrimSpace(os.Getenv("COVEY_EMBEDDING_API_KEY")),
		EmbeddingURL:      strings.TrimSpace(os.Getenv("COVEY_EMBEDDING_URL")),

		RequestLog:          getenvBool("COVEY_REQUEST_LOG", true),
		RequestLogBodies:    getenvBool("COVEY_REQUEST_LOG_BODIES", true),
		RequestLogRetention: getenvDuration("COVEY_REQUEST_LOG_RETENTION", 72*time.Hour),
	}
	// Secure cookie on by default as soon as the public URL is HTTPS.
	c.CookieSecure = getenvBool("COVEY_COOKIE_SECURE", strings.HasPrefix(c.PublicURL, "https://"))
	if c.IdentityProvider != "builtin" {
		return c, fmt.Errorf("identity provider %q: only 'builtin' is implemented in the MVP", c.IdentityProvider)
	}
	if c.SecretStore != "builtin" {
		return c, fmt.Errorf("secret store %q: only 'builtin' is implemented in the MVP", c.SecretStore)
	}
	return c, nil
}

// DataPlaneWarnings checks the address over which sandboxes connect back.
//
// The occasion was an outage: on a public instance COVEY_PUBLIC_URL had been
// set to the website's domain — an obvious move, the name suggests it. Every
// sandbox then dialled back over the open network, where the egress allowlist
// stopped it, and all agents failed with "daemon did not connect". That only
// became visible a minute later in the log of each individual session, not at
// startup.
//
// The check stays a warning and not an abort: there are setups in which the
// sandbox does reach the control plane under its public name. It is meant to
// voice the suspicion at startup, not to forbid an operation it cannot survey.
func (c Config) DataPlaneWarnings() []string {
	// Only the docker provider rewrites loopback to host.docker.internal and
	// therefore has a solid notion of "correct". For other providers any
	// address is conceivable.
	if c.SandboxProvider != "docker" {
		return nil
	}
	if isLoopbackPublic(c.PublicURL) {
		return nil
	}
	parsed, err := url.Parse(c.PublicURL)
	if err == nil && parsed.Hostname() == "host.docker.internal" {
		return nil
	}
	return []string{fmt.Sprintf(
		"COVEY_PUBLIC_URL points at %q — every sandbox has to reach the control plane "+
			"from THERE (COVEY_WS_URL), not the browser. If that address is not reachable "+
			"from inside the sandbox containers or not in the egress allowlist, all agents "+
			"fail with \"daemon did not connect\". "+
			"The website's address belongs in COVEY_SITE_URL.",
		c.PublicURL)}
}

// SecurityWarnings collects hardening hints for deployments that are not purely
// local. The messages are deliberately not fatal — local development and demos
// should run without ceremony — but they make insecure defaults visible at
// serve startup. Empty slice = nothing to complain about.
func (c Config) SecurityWarnings() []string {
	// Instances bound to localhost are development/demo — then stay silent.
	if isLoopbackPublic(c.PublicURL) {
		return nil
	}
	var w []string
	if !strings.HasPrefix(c.PublicURL, "https://") {
		w = append(w, "COVEY_PUBLIC_URL is not HTTPS — put it behind a TLS termination (reverse proxy) and set the public URL to https://")
	}
	if !c.CookieSecure {
		w = append(w, "session cookie without Secure flag — deliver it over HTTPS only (COVEY_COOKIE_SECURE=true as soon as TLS is terminated)")
	}
	if strings.Contains(c.DatabaseURL, "sslmode=disable") {
		w = append(w, "COVEY_DATABASE_URL uses sslmode=disable — in production enforce TLS to the database (sslmode=require or stricter)")
	}
	if !c.EgressEnforce {
		w = append(w, "egress enforcement off — in production set COVEY_EGRESS_ENFORCE=true (with the docker provider) so that sandboxes only reach allowlist hosts")
	}
	return w
}

func isLoopbackPublic(u string) bool {
	// Check the HOSTNAME, not the string: a `strings.Contains` would also take
	// https://localhost.example.com for development — and would then stay
	// silent about every insecurity on precisely a real, publicly reachable
	// instance.
	parsed, err := url.Parse(u)
	if err != nil {
		return false // unparseable = warn when in doubt
	}
	host := parsed.Hostname()
	if host == "" {
		// Without a scheme Go parses the value as a path; the host is then up front.
		host, _, _ = strings.Cut(strings.TrimPrefix(u, "//"), "/")
		host, _, _ = strings.Cut(host, ":")
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

// webhookSecretsFromEnv collects COVEY_<SYSTEM>_WEBHOOK_SECRET variables — a
// new target-system plugin needs no new config code.
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

// splitList breaks a comma-separated ENV list into trimmed, non-empty values.
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
