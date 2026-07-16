# 10 — Architektur-Stack

Technische Umsetzungsentscheidungen für Coveys eigenen Code (nicht die adoptierten Dienste — die stehen in [`08-marktumfeld.md`](08-marktumfeld.md)).

## Frontend

Eine dashboard-lastige, echtzeit-getriebene Admin-Oberfläche (Live-Agentenstatus, Recording-Timeline, Backlog-Kanban, Approval-Queue, Kosten-Dashboards). Stack:

- **TypeScript-SPA** (React oder Vue),
- **Tailwind + shadcn/ui** (oder Radix/Headless UI) für ein modernes, komponentenfertiges UI,
- **TanStack Query** fürs Daten-Handling,
- **WebSocket/SSE** für Live-Updates.

Diese Wahl ist unabhängig von der Backend-Sprache und gilt als gesetzt.

## Backend: zwei Concerns, ein Prozess

Covey ist **kein CRUD-Webapp**. Der Code trennt zwei Anforderungsprofile (siehe Diagramm in der Architektur-Diskussion):

- **API / BFF** — klassischer Web-Backend-Job: Agenten/Config/Rollen/Dashboards, REST/GraphQL, RBAC.
- **Orchestration-Core** — die **always-on Nebenläufigkeit**: viele langlebige Daemon-Verbindungen, Dispatch-Loop pro Agent, Event-Routing, `blocked`-Zustände.

Beide starten als **ein Prozess/Binary**, sind aber im Code sauber getrennt, damit der Core später eigenständig skalieren kann.

## Sprachwahl — offen, mit Tendenz zu Go

Siehe D10 in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md). Beide Optionen sind tragfähig; die Tendenz hat sich mit den Lean-Entscheidungen (single-binary, DB-Anker, Built-in-Dienste) Richtung Go verschoben.

| Kriterium | Go | Kotlin |
|---|---|---|
| **Deployment** | ein statisches Binary, keine Runtime — ideal für Hetzner/Proxmox | JVM (Fat-JAR) oder GraalVM-native (mehr Aufwand) |
| **Nebenläufigkeit (Core)** | Goroutines — einfach, ausreichend | Coroutines/Flow — reicher (strukturierte Nebenläufigkeit, Backpressure) |
| **Ökosystem-Nähe** | **im selben Ökosystem** wie das Adoptierte (kagent, Sandbox-SDKs sind Go) | Integration nur über Netzwerk-Protokolle, kein Code-Sharing |
| **Typsystem / Korrektheit** | simpel, verbos, sehr lesbar | reicher (sealed types, Null-Safety) — Vorteil bei Policy-/Security-Logik |
| **KI schreibt den Code** | riesiges, uniformes Cloud-native-Korpus → sehr idiomatische Ergebnisse | ebenfalls gut unterstützt |
| **Keycloak/OIDC-Libs** | solide (`coreos/go-oidc`, `zitadel/oidc`) | historisch reicher (JVM) — durch Built-in-IdM aber entwertet |
| **Onboarding (Werkstudenten)** | flache Lernkurve | steiler |

**Tendenz:** Go — wegen single-binary-Deployment, Ökosystem-Nähe und weil der „kenn ich nicht"-Einwand entfällt, wenn die KI den Code schreibt. Kotlin bleibt die stärkere Wahl, falls das reichere Typsystem für die Policy-Engine höher gewichtet wird als Betriebs-Einfachheit. **Frontend bleibt TS/Tailwind, egal wie diese Entscheidung fällt.**

## Prinzip: „Batteries included, but swappable"

Das tragende Umsetzungsprinzip. **Jede Plattform-Fähigkeit hat eine simple, DB-gestützte Built-in-Default und ein schmales Interface für einen externen, schwergewichtigen Provider.** Betriebslast schlägt Code-Zeilen: einen Fremddienst zu betreiben, abzusichern und zu upgraden ist oft teurer als eine simple eingebaute Implementierung.

- Der **MVP läuft mit `builtin` überall** — faktisch **Binary + Postgres + Sandbox-Infra, sonst nichts**.
- **Enterprise-Umgebungen** stöpseln vorhandene Dienste (Keycloak, Vault, Redis, Langfuse) über dasselbe Interface dazu, ohne dass sich Coveys Kern ändert.

Dieses Prinzip ist Designgrundsatz #10 (siehe [`README.md`](README.md)).

## Datenhaltung: Postgres als Anker

So wenige stateful Dienste wie möglich — Postgres absorbiert so viel wie möglich:

| Aufgabe | Postgres-Mechanik |
|---|---|
| Control-Plane-State, Backlog, RBAC | Tabellen |
| Job-Queue | `SELECT … FOR UPDATE SKIP LOCKED` |
| Pub/Sub (Wake-Events) | `LISTEN` / `NOTIFY` |
| Memory (Vektor) | `pgvector` |

Damit ist Postgres im MVP der **einzige neue stateful Kern**.

## Datenbank-Migrationen

Da Postgres nahezu alles absorbiert (State, Backlog, RBAC, Queue-Tabellen, pgvector-Memory, verschlüsselte Secret-Spalten, Recording), deckt **ein Migrations-Set das gesamte Schema** ab.

- **Format:** versionierte SQL-Dateien unter `migrations/` (jeweils `up`/`down`). Jede Änderung ist eine **neue** Migration — bestehende werden nie editiert.
- **Tooling:** `goose` oder `golang-migrate` (beide unterstützen `embed.FS`). Tendenz `goose` (Subcommands, sequentielle Versionen, sauberes Embedding).
- **Eingebettet:** Die Migrationen werden per `//go:embed migrations/*.sql` **ins Binary gebacken** — kein separates Ausliefern von Migrationsdateien, passt zum single-binary-Prinzip.
- **Ausführung:** per Subcommand `covey migrate up` **oder** automatisch beim Start. Beim Auto-Migrate zwingend ein **`pg_advisory_lock`**, damit bei mehreren Instanzen (always-on/HA) nur eine migriert und die anderen warten, statt zu kollidieren.
- **Seeding:** initiale Organisation + Admin-Rolle über eine idempotente Seed-Migration oder `covey bootstrap`.
- **Rollback:** `down`-Migrationen für den Notfall; im Normalbetrieb gilt „forward only".

## Pluggable-Interfaces

Zwei Ports tragen den Großteil des „selbst gebaut vs. extern"-Musters. Sie werden **jetzt** gezogen, auch wenn zunächst nur `builtin` existiert — nachträglich ein Interface unter gewachsene Direktzugriffe zu schieben ist teuer.

### `IdentityProvider`

Agenten-Identität ausstellen, Menschen authentifizieren, scoped/kurzlebige Tokens minten.

- **`builtin`** — DB-Eintrag + Signing-Key; Tokens sind **signierte JWTs** (Ed25519), kurz-TTL, gescopt. Menschen-Login simpel (Argon2id + Sessions).
- **`oidc`** — delegiert an Keycloak/Entra/Okta; Covey ist Relying Party. Menschen werden an den Firmen-IdP föderiert.
- **Grenze:** Echter **RFC-8693-Token-Exchange gegen Drittsysteme** wird **nicht** nachgebaut — den übernimmt der `oidc`-Provider. Die Built-in-Variante mintet nur Covey-eigene Tokens für Covey-eigene bzw. simpel per API-Key angebundene Zielsysteme.

### `SecretStore`

`get` / `put` / `delete`, optional kurzlebige Leases.

- **`builtin`** — **AES-GCM-verschlüsselte Spalte in Postgres**, Master-Key aus ENV/Datei/KMS. Deckt „lagere ein Legacy-API-Passwort und reiche es kurzlebig durch" vollständig ab.
- **`vault` / `infisical`** — für zentrales Secret-Management oder **dynamische Credential-Generierung** (das eine, was `builtin` bewusst *nicht* kann — ein Scale-Feature, kein MVP-Feature).

### Dasselbe Muster für zwei weitere Dienste

- **Queue/PubSub** → `builtin` (Postgres) / extern (Redis, NATS).
- **Observability** → `builtin` (Recording-Events in Postgres-Tabelle) / extern (OTEL → Langfuse, OpenObserve).

## Built-in vs. extern — Übersicht

| Fähigkeit | Built-in (MVP-Default) | Externer Provider (Enterprise/Scale) |
|---|---|---|
| Identität / IdM | DB + signierte JWTs | Keycloak / Entra / Okta (`oidc`) |
| Secrets | AES-GCM-Spalte in Postgres | Vault / Infisical |
| Queue / PubSub | `SKIP LOCKED` + `LISTEN/NOTIFY` | Redis / NATS |
| Observability | Postgres-Tabelle | Langfuse / OpenObserve |
| Memory | pgvector | Graphiti (temporal) |
| Sandbox / Data Plane | Subprozess (`local`) · Container (`docker`) | E2B / Beam (MicroVMs) |

## Projekt-Layout

```
covey/
  cmd/covey/          main.go — Wiring, Flags, Subcommands (serve, migrate, bootstrap)
  internal/
    orchestrator/     Dispatcher, State-Machine, Daemon-Verbindungen
    agents/           Registry, Config-Kompilierung (SOUL.md → Prompt)
    backlog/          Backlog-Store, Zustandsübergänge
    identity/         IdentityProvider — builtin/ + oidc/
    secrets/          SecretStore — builtin/ + vault/
    target/           Zielsystem-Plugins — Registry + Manifest-Engine, zammad/ + …
    guardrails/       Policy-Engine, Enforcement-Punkte
    observability/    Recording, Cost, Alerts
    http/             API/BFF-Handler, RBAC-Middleware
  web/                React/Vite-Frontend (dist/ wird eingebettet)
  migrations/         SQL-Migrationen (eingebettet)
  go.mod
```

Die Pluggable-Interfaces sind der Kern des „swappable"-Prinzips — sie werden als Go-Interfaces gezogen, die konkreten Implementierungen liegen in Unterpackages:

```go
// internal/identity
type IdentityProvider interface {
    IssueAgentToken(ctx context.Context, agentID string, scope Scope, ttl time.Duration) (Token, error)
    AuthenticateHuman(ctx context.Context, creds Credentials) (Principal, error)
}
// identity/builtin  → signierte JWTs (Ed25519) + Argon2id, State in Postgres
// identity/oidc     → Keycloak / Entra / Okta (Relying Party, RFC-8693-Exchange)

// internal/secrets
type SecretStore interface {
    Get(ctx context.Context, key string) (Secret, error)
    Put(ctx context.Context, key string, value Secret) error
    Delete(ctx context.Context, key string) error
}
// secrets/builtin   → AES-GCM-verschlüsselte Spalte in Postgres
// secrets/vault     → Vault / Infisical (inkl. dynamischer Credentials)
```

Welche Implementierung geladen wird, entscheidet die Config beim Start (`identity.provider = builtin|oidc`, `secrets.store = builtin|vault`). Der übrige Code kennt nur das Interface.

### Zielsysteme als Plugins

**Zielsysteme** (Zammad, GitLab, …) folgen demselben Muster wie die Runtimes: eine selbstregistrierende Plugin-Registry statt hartkodierter Listen. `internal/target` definiert das Interface (`System`: Webhook-Verifikation/-Parsing, Aktions-Ausführung, Guard-Rail-Subjekte, Prompt-Doku); jedes Zielsystem ist ein Unterpackage, das sich in `init()` einträgt. Es gibt zwei Plugin-Arten:

- **Kompilierte Built-ins** (`internal/target/zammad`, …): per Blank-Import in `cmd/covey` und `cmd/coveyd` eingebunden. Wer Covey **schlank ausliefern** will, lässt den Import weg — der Rest des Systems bleibt unverändert. Nötig für alles, was über simple REST-Aufrufe hinausgeht (OAuth-Flows, Spezial-Protokolle).
- **Manifest-Plugins** (kind=`custom`): ein Admin lädt zur Laufzeit eine **JSON-Plugin-Datei** hoch (UI „Zielsysteme" bzw. `POST /api/v1/targets`), die Webhook-Mapping, Aktionen (Methode + Pfad mit `{param}`-Platzhaltern) und Auth-Header deklariert. Eine generische REST-Engine interpretiert das Manifest — kein Neukompilieren, kein Deploy. Der Daemon holt sich Manifeste über das Daemon-Protokoll (`request_target`/`inject_target`), nur für aktivierte Systeme.

Beide Arten sind **pro Organisation aktivierbar** (Tabelle `target_plugins`); die Aktivierung greift fail-closed an zwei zentralen Enforcement-Punkten: der Webhook-Eingang lehnt deaktivierte Systeme ab, der Secrets-Broker gibt ihnen keine Credentials. Credentials folgen der Konvention `<system>_token`/`<system>_url` im SecretStore.

## Auslieferung: ein Binary (Frontend eingebettet)

Das Frontend wird **ins Go-Binary eingebettet** — kein separates Static-Hosting, kein nginx davor:

```go
//go:embed all:web/dist
var webFS embed.FS

dist, _ := fs.Sub(webFS, "web/dist")
mux.Handle("/", spaHandler(http.FS(dist)))  // SPA-Fallback: unbekannte Pfade → index.html
mux.Handle("/api/", apiHandler)
```

- **SPA-Fallback:** Da React Router client-seitig routet, muss ein Reload auf `/agents/42` die `index.html` liefern statt 404 — ein schmaler Handler, der bei unbekannten Pfaden zurückfällt.
- **Dev vs. Prod:** Im Dev läuft der Vite-Dev-Server (Hot-Reload) und proxyt `/api` zu Go; im Prod-Build wird `web/dist` eingebettet. Umschaltung per Build-Tag/ENV.
- **Ergebnis:** ein Prozess = Frontend + API + Orchestration-Core.

## Betrieb & Bootstrapping

- **Config:** über ENV (12-Factor) plus Flags für die Subcommands (`serve`, `migrate`, `bootstrap`).
- **Health/Readiness:** `/healthz` (Prozess lebt) und `/readyz` (prüft DB-Verbindung) für Orchestrierung/Loadbalancer.
- **Graceful Shutdown:** auf `SIGTERM` laufende Tasks sauber zu Ende führen und Daemon-Verbindungen kontrolliert schließen — bei einem always-on-System essenziell, damit kein Agent mitten in einer Aktion abgeschnitten wird.
- **Docker (Multi-Stage):** Node baut das Frontend → Go bettet es ein und kompiliert → distroless-Endimage (nur das Binary, ~15 MB):

```dockerfile
FROM node:22 AS web
WORKDIR /web
COPY web/ .
RUN npm ci && npm run build          # erzeugt web/dist

FROM golang:1.24 AS build
WORKDIR /src
COPY . .
COPY --from=web /web/dist ./web/dist
RUN go build -o /covey ./cmd/covey    # embed zieht dist + migrations mit rein

FROM gcr.io/distroless/static
COPY --from=build /covey /covey
ENTRYPOINT ["/covey"]
```

## Leitplanken für „simpel selbst gebaut"

Damit „eingebaut" nicht kippt:

- **Krypto-Primitive ja, Krypto-Protokolle nein.** JWTs signieren, AES-GCM verschlüsseln, Argon2id hashen — mit bewährten Bibliotheken, Standard und sicher. **Kein eigenes Crypto erfinden, keinen eigenen OAuth/OIDC-Server-Stack nachbauen.** Sobald echtes föderiertes SSO oder Token-Exchange gegen Dritte gefragt ist, übernimmt der externe Provider.
- **Interface vor Implementierung.** Erst den Port, dann die simpelste Implementierung dahinter.
- Der **Secrets-Broker** aus [`04-identitaet-secrets.md`](04-identitaet-secrets.md) *ist* dieses `IdentityProvider`+`SecretStore`-Interface — die Built-in-Variante ist seine simpelste Implementierung.

## Deployment

**Ein einziges Go-Binary** (Frontend eingebettet, Migrationen eingebettet) **+ Postgres** auf eigener Hetzner/Proxmox-Infra. Deploy = eine Datei kopieren, `covey migrate up`, `covey serve` — kein separates Frontend-Hosting, kein nginx nötig. Externe Dienste (Keycloak, Vault, Redis, Langfuse) sind optional zuschaltbar, nicht Voraussetzung. Self-Hosting ist zugleich der Enterprise-Vorteil (Datenresidenz, siehe [`09-enterprise-modell.md`](09-enterprise-modell.md)) — die Sandbox-Infra der Data-Plane (E2B/Beam) ist der einzige unvermeidbare Zusatz.
