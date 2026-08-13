# 10 — Architecture stack

Technical implementation decisions for Covey's own code (not the adopted services — those are in [`08-market.md`](08-market.md)).

## Frontend

A dashboard-heavy, real-time-driven admin interface (live agent status, recording timeline, backlog kanban, approval queue, cost dashboards). Stack:

- a **TypeScript SPA** (React or Vue),
- **Tailwind + shadcn/ui** (or Radix/Headless UI) for a modern, component-ready UI,
- **TanStack Query** for data handling,
- **WebSocket/SSE** for live updates.

This choice is independent of the backend language and is considered settled.

## Backend: two concerns, one process

Covey is **not a CRUD web app**. The code separates two requirement profiles (see the diagram in the architecture discussion):

- **API / BFF** — the classic web backend job: agents/config/roles/dashboards, REST/GraphQL, RBAC.
- **Orchestration core** — the **always-on concurrency**: many long-lived daemon connections, a dispatch loop per agent, event routing, `blocked` states.

Both start as **one process/binary** but are cleanly separated in the code so that the core can scale independently later.

## Language choice — open, leaning Go

See D10 in [`07-open-decisions.md`](07-open-decisions.md). Both options are viable; the leaning has shifted towards Go with the lean decisions (single binary, DB anchor, built-in services).

| Criterion | Go | Kotlin |
|---|---|---|
| **Deployment** | one static binary, no runtime — ideal for Hetzner/Proxmox | JVM (fat JAR) or GraalVM native (more effort) |
| **Concurrency (core)** | goroutines — simple, sufficient | coroutines/flow — richer (structured concurrency, backpressure) |
| **Ecosystem proximity** | **in the same ecosystem** as what is being adopted (kagent, sandbox SDKs are Go) | integration only over network protocols, no code sharing |
| **Type system / correctness** | simple, verbose, very readable | richer (sealed types, null safety) — an advantage for policy/security logic |
| **AI writes the code** | a huge, uniform cloud-native corpus → very idiomatic results | also well supported |
| **Keycloak/OIDC libs** | solid (`coreos/go-oidc`, `zitadel/oidc`) | historically richer (JVM) — but devalued by the built-in IdM |
| **Onboarding (working students)** | flat learning curve | steeper |

**Leaning:** Go — because of single-binary deployment, ecosystem proximity and because the "I don't know it" objection falls away when the AI writes the code. Kotlin remains the stronger choice if the richer type system for the policy engine is weighted higher than operational simplicity. **The frontend stays TS/Tailwind whichever way this decision goes.**

## Principle: "batteries included, but swappable"

The load-bearing implementation principle. **Every platform capability has a simple, DB-backed built-in default and a narrow interface for an external, heavyweight provider.** Operational load beats lines of code: operating, securing and upgrading a third-party service is often more expensive than a simple built-in implementation.

- The **MVP runs with `builtin` everywhere** — effectively **binary + Postgres + sandbox infra, nothing else**.
- **Enterprise environments** plug existing services (Keycloak, Vault, Redis, Langfuse) in through the same interface without Covey's core changing.

This principle is design principle #10 (see [`README.md`](README.md)).

## Data storage: Postgres as the anchor

As few stateful services as possible — Postgres absorbs as much as it can:

| Job | Postgres mechanic |
|---|---|
| Control-plane state, backlog, RBAC | tables |
| Job queue | `SELECT … FOR UPDATE SKIP LOCKED` |
| Pub/sub (wake events) | `LISTEN` / `NOTIFY` |
| Memory (vector) | `pgvector` |

That makes Postgres the **only new stateful core** in the MVP.

## Database migrations

Since Postgres absorbs nearly everything (state, backlog, RBAC, queue tables, pgvector memory, encrypted secret columns, recording), **one migration set covers the entire schema**.

- **Format:** versioned SQL files under `migrations/` (each with `up`/`down`). Every change is a **new** migration — existing ones are never edited.
- **Tooling:** `goose` or `golang-migrate` (both support `embed.FS`). Leaning `goose` (subcommands, sequential versions, clean embedding).
- **Embedded:** the migrations are **baked into the binary** via `//go:embed migrations/*.sql` — no separate shipping of migration files, which fits the single-binary principle.
- **Execution:** by the subcommand `covey migrate up` **or** automatically at start. Auto-migrate mandates a **`pg_advisory_lock`** so that with several instances (always-on/HA) only one migrates and the others wait instead of colliding.
- **Seeding:** the initial organisation + admin role through an idempotent seed migration or `covey bootstrap`.
- **Rollback:** `down` migrations for emergencies; in normal operation "forward only" applies.

## Pluggable interfaces

Two ports carry the bulk of the "built ourselves vs. external" pattern. They are drawn **now**, even though only `builtin` exists at first — retrofitting an interface under grown direct access is expensive.

### `IdentityProvider`

Issue agent identities, authenticate humans, mint scoped/short-lived tokens.

- **`builtin`** — a DB entry + a signing key; tokens are **signed JWTs** (Ed25519), short TTL, scoped. Human login is simple (Argon2id + sessions).
- **`oidc`** — delegates to Keycloak/Entra/Okta; Covey is the relying party. Humans are federated to the company IdP.
- **Limit:** real **RFC 8693 token exchange against third-party systems** is **not** rebuilt — the `oidc` provider handles that. The built-in variant only mints Covey's own tokens for Covey's own target systems or ones connected simply by API key.

### `SecretStore`

`get` / `put` / `delete`, optionally short-lived leases.

- **`builtin`** — an **AES-GCM-encrypted column in Postgres**, master key from ENV/file/KMS. Fully covers "store a legacy API password and pass it through short-lived".
- **`vault` / `infisical`** — for central secret management or **dynamic credential generation** (the one thing `builtin` deliberately *cannot* do — a scale feature, not an MVP feature).

### The same pattern for two more services

- **Queue/pub-sub** → `builtin` (Postgres) / external (Redis, NATS).
- **Observability** → `builtin` (recording events in a Postgres table) / external (OTEL → Langfuse, OpenObserve).

## Built-in vs. external — overview

| Capability | Built-in (MVP default) | External provider (enterprise/scale) |
|---|---|---|
| Identity / IdM | DB + signed JWTs | Keycloak / Entra / Okta (`oidc`) |
| Secrets | AES-GCM column in Postgres | Vault / Infisical |
| Queue / pub-sub | `SKIP LOCKED` + `LISTEN/NOTIFY` | Redis / NATS |
| Observability | a Postgres table | Langfuse / OpenObserve |
| Memory | wiki (Markdown pages) + pgvector index | Graphiti (temporal, post-wiki) |
| Sandbox / data plane | subprocess (`local`) · container (`docker`) | E2B / Beam (microVMs) |

## Project layout

```
covey/
  cmd/covey/          main.go — wiring, flags, subcommands (serve, migrate, bootstrap)
  internal/
    orchestrator/     dispatcher, state machine, daemon connections
    agents/           registry, config compilation (SOUL.md → prompt)
    backlog/          backlog store, state transitions
    identity/         IdentityProvider — builtin/ + oidc/
    secrets/          SecretStore — builtin/ + vault/
    target/           target-system plugins — registry + manifest engine, zammad/ + …
    guardrails/       policy engine, enforcement points
    observability/    recording, cost, alerts
    http/             API/BFF handlers, RBAC middleware
  web/                React/Vite frontend (dist/ gets embedded)
  migrations/         SQL migrations (embedded)
  go.mod
```

The pluggable interfaces are the core of the "swappable" principle — they are drawn as Go interfaces, with the concrete implementations in subpackages:

```go
// internal/identity
type IdentityProvider interface {
    IssueAgentToken(ctx context.Context, agentID string, scope Scope, ttl time.Duration) (Token, error)
    AuthenticateHuman(ctx context.Context, creds Credentials) (Principal, error)
}
// identity/builtin  → signed JWTs (Ed25519) + Argon2id, state in Postgres
// identity/oidc     → Keycloak / Entra / Okta (relying party, RFC 8693 exchange)

// internal/secrets
type SecretStore interface {
    Get(ctx context.Context, key string) (Secret, error)
    Put(ctx context.Context, key string, value Secret) error
    Delete(ctx context.Context, key string) error
}
// secrets/builtin   → AES-GCM-encrypted column in Postgres
// secrets/vault     → Vault / Infisical (including dynamic credentials)
```

Which implementation is loaded is decided by the config at start (`identity.provider = builtin|oidc`, `secrets.store = builtin|vault`). The rest of the code knows only the interface.

### Target systems as plugins

**Target systems** (Zammad, GitLab, …) follow the same pattern as the runtimes: a self-registering plugin registry instead of hardcoded lists. `internal/target` defines the interface (`System`: webhook verification/parsing, action execution, guard-rail subjects, prompt documentation); every target system is a subpackage that registers itself in `init()`. There are two kinds of plugin:

- **Compiled built-ins** (`internal/target/zammad`, …): pulled in by blank import in `cmd/covey` and `cmd/coveyd`. Whoever wants to ship Covey **lean** leaves the import out — the rest of the system stays unchanged. Necessary for everything that goes beyond simple REST calls (OAuth flows, special protocols).
- **Manifest plugins** (kind=`custom`): an admin uploads a **JSON plugin file** at runtime (UI "Target systems" or `POST /api/v1/targets`) declaring webhook mapping, actions (method + path with `{param}` placeholders) and auth headers. A generic REST engine interprets the manifest — no recompilation, no deploy. The daemon fetches manifests through the daemon protocol (`request_target`/`inject_target`), only for enabled systems.

In the UI both kinds (plus connected MCP servers, kind=`mcp`) appear as a **store**: a catalogue of all registered and uploaded plugins with search, category filter and activation directly on the card, next to a view of the active systems. The category (`ticketing`, `code`, `communication`, `files`, `web`, `dev`, `other`) is declared by the plugin itself — in the `Descriptor` or in the manifest field `category`; the UI derives its filters from the categories that occur and keeps no list of its own. Behaviour never depends on the category, it is pure classification.

Both kinds are **enabled per organisation** (table `target_plugins`); activation is **opt-in and fail-closed** — even compiled built-ins are usable only after explicit activation in the UI (existing organisations were set to their previous state by migration). It takes effect at the central enforcement points: the webhook intake rejects systems that are not enabled, the secrets broker gives them no credentials, and prompt documentation as well as profile-field identifiers appear only for enabled systems.

**Prompt documentation needs the agent's grant on top of that.** A system's action list reaches an agent's system prompt only if the organisation has enabled it *and* the agent has a line for it in `ACCESS.md` — the same table the secrets broker asks. The organisation's activation alone used to decide, so every agent carried the instructions for every enabled system around, including the ones whose credentials the broker refuses it. That is wrong twice over: it invites the agent to attempt something that cannot work, and the docs sit in the context of every turn — the built-in ones come to around 11,000 tokens in total, GitLab and GitHub about 4,000 each. Least privilege and prompt economy point the same way here.

Credentials follow the convention `<system>_token`/`<system>_url` in the SecretStore.

Where the runtime-installable kinds come *from* is a separate question, and it is answered outside the binary: a **catalogue behind a configurable URL**, maintained as an index repository whose entries point at plugins hosted anywhere and pin them by digest. Installing from it writes the same `target_plugins` row the manual upload writes — the marketplace is a new source, not a new runtime path. See [`22-plugin-marketplace.md`](22-plugin-marketplace.md).

## Delivery: one binary (frontend embedded)

The frontend is **embedded into the Go binary** — no separate static hosting, no nginx in front:

```go
//go:embed all:web/dist
var webFS embed.FS

dist, _ := fs.Sub(webFS, "web/dist")
mux.Handle("/", spaHandler(http.FS(dist)))  // SPA fallback: unknown paths → index.html
mux.Handle("/api/", apiHandler)
```

- **SPA fallback:** since React Router routes client-side, a reload on `/agents/42` has to deliver the SPA shell instead of a 404 — the handler recognises the paths of the signed-in interface by their prefix and falls back only there.
- **Dev vs. prod:** in dev the Vite dev server runs (hot reload) and proxies `/api` to Go; in the prod build `web/dist` is embedded. Switched by build tag/ENV.
- **Result:** one process = frontend + API + orchestration core.

### The public website is pre-rendered

The same binary delivers two things that contradict each other in one respect: the **signed-in interface**, which nobody should index, and the **public website** (home, features, integrations, products, docs), which has to be found. A pure SPA satisfies only the first. Google does execute JavaScript, Bing does so unreliably, and the language models' crawlers (GPTBot, ClaudeBot, PerplexityBot) not at all — an empty shell is an empty page to them.

The build therefore pre-renders the public pages (`web/prerender.mjs`, after `vite build`): every page per language as a real HTML file with content and a head of its own. The separation therefore lies in the **delivery**, not in operations — it remains one binary.

- **One source for the routes:** `web/src/public/seo.ts` carries path, title and description per language. The public site's routing, the head (`Head.tsx`), the pre-rendering and `sitemap.xml` are built from it. The docs pages derive from the content — a new page appears in the sitemap without further intervention.
- **Language through the URL:** the English version lives under `/en/…` with translated slugs. A language that only switches in `localStorage` has no address and does not exist for search engines. `hreflang` links the versions to each other.
- **The address only at runtime:** Covey is self-hosted, every installation has its own domain. The pre-rendered HTML therefore contains a placeholder that the server replaces with the actual origin on delivery — normally the request's origin, overridable via `COVEY_SITE_URL`. `robots.txt` and `sitemap.xml` are generated by the binary itself for the same reason. Deliberately **not** `COVEY_PUBLIC_URL`: that is the control plane's address as reachable by the sandboxes and therefore an internal operational address — hanging both off one variable would mean nobody could set the website address without taking the data plane off the network.
- **Honest 404:** unknown paths outside the app prefixes get status 404 instead of the home page with 200 — otherwise every wrong address counts as a valid page ("soft 404").
- **Fonts in the binary:** Inter and Lora ship with it rather than being loaded from Google's CDN — our own CSP allows no foreign hosts, and every visitor's IP would otherwise go to a third party.

## Operations & bootstrapping

- **Config:** through ENV (12-factor) plus flags for the subcommands (`serve`, `migrate`, `bootstrap`).
- **Health/readiness:** `/healthz` (the process is alive) and `/readyz` (checks the DB connection) for orchestration/load balancers.
- **Graceful shutdown:** on `SIGTERM`, finish running tasks cleanly and close daemon connections in a controlled way — essential in an always-on system so that no agent is cut off mid-action.
- **Docker (multi-stage):** Node builds the frontend → Go embeds it and compiles → a distroless final image (just the binary, ~15 MB):

```dockerfile
FROM node:22 AS web
WORKDIR /web
COPY web/ .
RUN npm ci && npm run build          # produces web/dist

FROM golang:1.24 AS build
WORKDIR /src
COPY . .
COPY --from=web /web/dist ./web/dist
RUN go build -o /covey ./cmd/covey    # embed pulls dist + migrations in

FROM gcr.io/distroless/static
COPY --from=build /covey /covey
ENTRYPOINT ["/covey"]
```

## Guard rails for "simple, built ourselves"

So that "built in" does not tip over:

- **Crypto primitives yes, crypto protocols no.** Sign JWTs, encrypt with AES-GCM, hash with Argon2id — with proven libraries, standard and secure. **Do not invent your own crypto, do not rebuild your own OAuth/OIDC server stack.** As soon as real federated SSO or token exchange against third parties is required, the external provider takes over.
- **Interface before implementation.** First the port, then the simplest implementation behind it.
- The **secrets broker** from [`04-identity-secrets.md`](04-identity-secrets.md) *is* this `IdentityProvider`+`SecretStore` interface — the built-in variant is its simplest implementation.

## Deployment

**A single Go binary** (frontend embedded, migrations embedded) **+ Postgres** on your own Hetzner/Proxmox infrastructure. Deploy = copy one file, `covey migrate up`, `covey serve` — no separate frontend hosting, no nginx needed. External services (Keycloak, Vault, Redis, Langfuse) can be switched on optionally, they are not prerequisites. Self-hosting is at the same time the enterprise advantage (data residency, see [`09-enterprise-model.md`](09-enterprise-model.md)) — the data plane's sandbox infrastructure (E2B/Beam) is the only unavoidable addition.
