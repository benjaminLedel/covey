// Package config lädt die Prozess-Konfiguration aus ENV (12-Factor).
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
	// DatabaseURL ist die Postgres-DSN, z. B. postgres://covey:covey@localhost:5433/covey
	DatabaseURL string
	// ListenAddr ist die HTTP-Bind-Adresse des Binaries (API + eingebettetes Frontend).
	ListenAddr string
	// PublicURL ist die von Sandboxen erreichbare Basis-URL der Control Plane.
	PublicURL string
	// CookieSecure setzt das Secure-Flag auf dem Session-Cookie (nur über
	// HTTPS ausgeliefert). Default: automatisch aus dem PublicURL-Schema
	// abgeleitet (https → true), per COVEY_COOKIE_SECURE überschreibbar.
	CookieSecure bool
	// MasterKeyHex ist der 32-Byte-AES-Schlüssel (hex) des Built-in SecretStore.
	MasterKeyHex string
	// IdentityProvider wählt die Implementierung: builtin | oidc (post-MVP).
	IdentityProvider string
	// SecretStore wählt die Implementierung: builtin | vault (post-MVP).
	SecretStore string
	// SandboxProvider wählt die Data-Plane: docker (Container) | e2b (post-MVP).
	SandboxProvider string
	// DataDir hält die persistenten Sandbox-Homes (docker-Provider).
	DataDir string
	// SandboxImage ist das Container-Image des docker-Providers (Dockerfile.sandbox).
	SandboxImage string
	// WebhookSecrets verifizieren Signaturen eingehender Zielsystem-Webhooks:
	// COVEY_<SYSTEM>_WEBHOOK_SECRET → Eintrag unter dem kleingeschriebenen
	// System-Namen (z. B. COVEY_ZAMMAD_WEBHOOK_SECRET → "zammad").
	WebhookSecrets map[string]string
	// TickInterval ist der periodische "was liegt an?"-Impuls des Dispatch-Loops.
	TickInterval time.Duration

	// DreamAt ist die Ortszeit ("03:00"), ab der Agenten nachts ihr Gedächtnis
	// aufräumen. Leer oder "off" schaltet den Nachtlauf ab — jeder Traum kostet
	// einen LLM-Aufruf, und wer das nicht will, soll es abstellen können, ohne
	// die Funktion aus dem Binary zu nehmen.
	DreamAt string
	// SessionTTL für menschliche Logins.
	SessionTTL time.Duration
	// DaemonTokenTTL für die kurzlebigen Sandbox-Daemon-JWTs.
	DaemonTokenTTL time.Duration
	// BoardRetention ist das Alter, ab dem die Control Plane eine terminale
	// Aufgabe selbst archiviert (nicht löscht) und die dadurch leere
	// Agenten-Spalte abräumt — damit sich das Board ohne Zutun aufräumt.
	// COVEY_BOARD_RETENTION (Default 24h); eine negative Dauer schaltet das
	// Aufräumen ab und lässt das Board wachsen.
	BoardRetention time.Duration
	// EgressEnforce schaltet den Egress-Allowlist-Proxy ein (nur docker-Provider):
	// Sandbox-Verkehr geht dann über einen Proxy, der nur Allowlist-Hosts durchlässt.
	EgressEnforce bool
	// EgressAllow sind zusätzliche erlaubte Egress-Hosts (Zielsysteme wie das
	// Zammad-Host), zusätzlich zu den fest erlaubten Anthropic-Hosts.
	// COVEY_EGRESS_ALLOW="helpdesk.example.com,*.internal.example.com".
	EgressAllow []string
	// EgressIsolation wählt die Durchsetzungs-Stärke des Egress (docker-Provider):
	// "proxy" (Default, kooperativ via HTTP_PROXY) | "network" (hart: Sandbox auf
	// internem Netz ohne Internet, Proxy-Container als einziger Ausgang).
	EgressIsolation string
	// EgressProxyAddr ist die Bind-Adresse des standalone Egress-Proxy
	// (Subcommand `covey egress-proxy`, im network-Modus als Container).
	EgressProxyAddr string
	// RequestLog schaltet das Request-Log ein (Default an): die HTTP-Requests
	// an den Rändern der Plattform — eingehende Webhooks und ausgehende
	// Zielsystem-Aufrufe — landen in der Tabelle request_log und sind unter
	// Plattform → Requests einsehbar. COVEY_REQUEST_LOG=false schaltet ab.
	RequestLog bool
	// RequestLogBodies speichert zusätzlich zu den Metadaten die (gekappten,
	// redigierten) Request-/Response-Bodies. Default an — ohne sie ist eine
	// Fehlersuche an einer Zielsystem-API meist wertlos. Wer keine
	// Nutzinhalte (Chat-Nachrichten, Ticket-Texte) in der Diagnose-Tabelle
	// haben will, setzt COVEY_REQUEST_LOG_BODIES=false.
	RequestLogBodies bool
	// RequestLogRetention ist das Alter, ab dem Log-Einträge verschwinden
	// (COVEY_REQUEST_LOG_RETENTION, Default 72h). Zusätzlich greift eine harte
	// Zeilen-Obergrenze im Store.
	RequestLogRetention time.Duration
	// WikiCleanup ist der Zeitplan des plattformweiten Wiki-Aufräum-Heartbeats:
	// leer = aus. Sonst "HH:MM" (täglich, Serverzeit) oder eine Go-Dauer wie "24h"
	// (Intervall). Die Control Plane legt daraus für jeden Agenten einen
	// wiederkehrenden Backlog-Task an, in dem der Agent sein Wiki pflegt
	// (Duplikate mergen, tote Links fixen). Pro Agent per HEARTBEAT.md
	// überschreibbar. COVEY_WIKI_CLEANUP.
	WikiCleanup string
	// EmbeddingProvider wählt das Embedding hinter dem Wiki-Retrieval:
	// "builtin" (Hash, offline, nur lexikalisch — Default), "ollama" (selbst
	// betrieben, ohne Schlüssel) oder die fremden Dienste "voyage"/"openai",
	// die EmbeddingAPIKey brauchen. Ein Wechsel bettet den Bestand im
	// Hintergrund neu ein. COVEY_EMBEDDING_PROVIDER.
	EmbeddingProvider string
	// EmbeddingModel überschreibt das Provider-Default-Modell.
	// COVEY_EMBEDDING_MODEL.
	EmbeddingModel string
	// EmbeddingAPIKey ist der Schlüssel des Providers. COVEY_EMBEDDING_API_KEY.
	EmbeddingAPIKey string
	// EmbeddingURL überschreibt den Endpunkt (Proxy, Azure, kompatible Dienste).
	// COVEY_EMBEDDING_URL.
	EmbeddingURL string
}

func FromEnv() (Config, error) {
	c := Config{
		DatabaseURL:      getenv("COVEY_DATABASE_URL", "postgres://covey:covey@localhost:5433/covey?sslmode=disable"),
		ListenAddr:       getenv("COVEY_LISTEN_ADDR", ":8494"),
		PublicURL:        getenv("COVEY_PUBLIC_URL", "http://localhost:8494"),
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
	// Secure-Cookie standardmäßig an, sobald die öffentliche URL HTTPS ist.
	c.CookieSecure = getenvBool("COVEY_COOKIE_SECURE", strings.HasPrefix(c.PublicURL, "https://"))
	if c.IdentityProvider != "builtin" {
		return c, fmt.Errorf("identity provider %q: nur 'builtin' ist im MVP implementiert", c.IdentityProvider)
	}
	if c.SecretStore != "builtin" {
		return c, fmt.Errorf("secret store %q: nur 'builtin' ist im MVP implementiert", c.SecretStore)
	}
	return c, nil
}

// SecurityWarnings sammelt Härtungs-Hinweise für Deployments, die nicht rein
// lokal sind. Die Meldungen sind bewusst nicht fatal — lokale Entwicklung und
// Demos sollen ohne Zeremonie laufen —, aber sie machen unsichere Defaults beim
// serve-Start sichtbar. Leerer Slice = nichts zu beanstanden.
func (c Config) SecurityWarnings() []string {
	// Auf localhost gebundene Instanzen sind Entwicklung/Demo — dann still.
	if isLoopbackPublic(c.PublicURL) {
		return nil
	}
	var w []string
	if !strings.HasPrefix(c.PublicURL, "https://") {
		w = append(w, "COVEY_PUBLIC_URL ist nicht HTTPS — vor eine TLS-Terminierung stellen (Reverse-Proxy) und die öffentliche URL auf https:// setzen")
	}
	if !c.CookieSecure {
		w = append(w, "Session-Cookie ohne Secure-Flag — nur über HTTPS ausliefern (COVEY_COOKIE_SECURE=true, sobald TLS terminiert)")
	}
	if strings.Contains(c.DatabaseURL, "sslmode=disable") {
		w = append(w, "COVEY_DATABASE_URL nutzt sslmode=disable — im Betrieb TLS zur Datenbank erzwingen (sslmode=require o. höher)")
	}
	if !c.EgressEnforce {
		w = append(w, "Egress-Enforcement aus — für den Betrieb COVEY_EGRESS_ENFORCE=true (mit docker-Provider) setzen, damit Sandboxen nur Allowlist-Hosts erreichen")
	}
	return w
}

func isLoopbackPublic(u string) bool {
	// Auf den HOSTNAMEN prüfen, nicht auf die Zeichenkette: Ein
	// `strings.Contains` hielte auch https://localhost.example.com für
	// Entwicklung — und schwiege damit ausgerechnet bei einer echten, öffentlich
	// erreichbaren Instanz zu jeder Unsicherheit.
	parsed, err := url.Parse(u)
	if err != nil {
		return false // unlesbar = im Zweifel warnen
	}
	host := parsed.Hostname()
	if host == "" {
		// Ohne Schema parst Go den Wert als Pfad; dann steht der Host vorne.
		host, _, _ = strings.Cut(strings.TrimPrefix(u, "//"), "/")
		host, _, _ = strings.Cut(host, ":")
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
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
