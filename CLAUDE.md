# CLAUDE.md

Orientation for Claude Code in this repository.

## What covey is

An enterprise platform that treats **AI agents like employees**: an identity, an isolated sandbox, brokered access, a backlog, a place in the org chart, central governance. The unit is the **organisation**, not the individual user. The guiding metaphor: the **IT and HR department for AI agents**.

## Where it stands

**Well past the MVP.** The vertical slice (M0–M7 from `spec/11-mvp-plan.md`) has long been in place; on top of it sit the org chart and departments, employee profiles, further target-system plugins (GitHub, GitLab, Jira, Confluence, email/IMAP, MCP), docker sandboxes, egress control, a QA agent bundle, the **wiki memory** (linked markdown pages plus a pgvector index instead of flat snippets, `spec/05`), the **runner** (the data plane leaves the control plane's machine, `spec/16`) and the **plugin marketplace** (`spec/22`). "M0–M7" marks the baseline, not the current scope. The repository holds:

- `spec/` — the full specification (23 documents, **English**). Start at `spec/README.md`.
- The code, laid out as in `spec/10-architecture-stack.md`: `cmd/covey` (control plane), `cmd/coveyd` (sandbox daemon), `cmd/covey-runner` (the remote runner), `internal/…`, `web/` (React SPA, embedded), `migrations/` (embedded).
- `internal/integration/` — the acceptance checklist from `spec/11-mvp-plan.md` as an integration test suite (a real Postgres on port 5433, an in-process daemon over a real WebSocket, a mock runtime, a fake Zammad).
- `demo/fakezammad/` — a Zammad double for local demos.
- `mockup/covey-ui-mockup.html` — a static HTML mockup; the React UI takes its design language from it (CSS variables, Inter/Lora).
- `.claude/skills/covey-agent/` — a Claude Code skill for **building, designing and updating covey agents**: it produces a `covey.agent-config` bundle following the repo's conventions (SOUL/PLAYBOOKS/ACCESS/HEARTBEAT, loop protection, `warm_sandbox`) and optionally creates the agent through the API. Start at its `SKILL.md`.

Development workflow: `make dev-db && make bootstrap && make run` (see the README). Tests: `make test`; integration tests: `make test-integration` (they need the dev database and skip when port 5433 is unreachable). `web/dist` has to exist before the Go build (`cd web && npm run build`) — `//go:embed` pulls it into the binary.

**Rebuild and restart the server after a change.** `go build ./...` is a compile check only — it does *not* rewrite the `./covey` binary. To make a change live (backend or web UI): `make build` (builds `web/dist` + `covey` + `coveyd`), then stop the running `covey serve` process (`pgrep -fl "covey serve"`) and start it again via `make run` or `COVEY_MASTER_KEY=$(cat .covey.key) ./covey serve`. The data plane runs through the **docker provider** (the default); after a change that touches coveyd, the sandbox image needs rebuilding (`make sandbox-image`) or fetching (`make sandbox-images-pull`, which pulls the published images) so that the sandbox carries the new binary. Migrations run automatically at `serve` (auto-migrate behind an advisory lock).

## Language and conventions

- **Everything in this repository is English.** `spec/`, `docs/`, `README.md`, commit messages, code comments, branch names, file names. The repo is public on GitHub, third parties install covey from it, write plugins against it and read the code — a German history and German comments shut them out. The *tone* stays as it was: sober, precise, no marketing language; a sentence says what changes and why, not what was touched.
- **Strings that come out of the program are not translated:** error messages, log lines, UI labels and config syntax a parser reads (the `HEARTBEAT.md` keys `alle:`/`täglich:`/`nur-wenn:`/`titel:`/`aufgabe:`). They stand verbatim, with an explanation beside them — a translated error message documents something that never appears.
- **Existing German comments are not translated in one sweep**; whoever touches a place brings it along.
- **README:** `README.md` is the English calling card. The German version sits beside it in `README.de.md`. Keep both in step when changing one — they are translations of each other, not separate documents.
- **The interface has ten language catalogues** (`web/src/locales/*.json`: de, en, es, fr, it, nl, pl, pt, ja, zh). `web/src/locales/parity.test.ts` fails the `build-web` job as soon as a key is missing in any of them — new UI text goes into all ten, and `cd web && npm test` runs before a commit that touches the UI.
- Spec documents link each other relatively (``[`04-…`](04-identity-secrets.md)``). Keep that linking consistent when changing things.
- Every file in `spec/` has one clear area of responsibility (see the table in `spec/README.md`) — write content into the right file rather than duplicating it.

## Core architecture (for quick reference)

- **Control plane** (stateful, always on): scheduler/dispatcher, agent registry and org chart, backlog store, identity and secrets broker, guard-rail/policy engine, observability, config sync.
- **Data plane**: isolated, ephemeral sandboxes with a persistent `/home`. "Dumb and replaceable" — if a sandbox is lost it is rebuilt from config plus home.
- **Runner protocol**: every sandbox starts through it, including on a single machine, where the control plane runs a built-in runner itself (`spec/16`). That is what keeps the path to a remote host from being a second implementation.
- **Daemon protocol**: bidirectional (WebSocket/gRPC) between control plane and sandbox daemon. A stable seam — runtimes change, the protocol stays. Messages in `spec/01-architecture.md`.
- **Runtime adapters**: thin, translating between the daemon protocol and a runtime's specifics. The first one: Claude Code headless via `claude -p` (`spec/12-claude-code-adapter.md`).

## The stack

- **Backend:** one Go binary. API/BFF plus orchestration core, one process, cleanly separated inside.
- **Frontend:** React SPA + Tailwind + shadcn/ui + TanStack Query, WebSocket/SSE, **embedded into the binary** (`//go:embed`).
- **Postgres as the anchor:** state, backlog, RBAC, queue (`SELECT … FOR UPDATE SKIP LOCKED`), pub/sub (`LISTEN/NOTIFY`), memory (`pgvector`), AES-GCM secret columns.
- **Migrations:** versioned SQL under `migrations/` (up/down), embedded via `//go:embed`, run by `covey migrate up` behind a `pg_advisory_lock`. Never edit an existing migration — always add a new one. Numbers are consumed in order; when two branches take the same number, the test in `internal/db` says so and the newer branch renumbers.
- **"Batteries included, but swappable":** two ports carry the pattern — `IdentityProvider` (builtin JWT/Argon2id ↔ `oidc`) and `SecretStore` (builtin AES-GCM ↔ `vault`). Draw the interface before the implementation, even while only `builtin` exists.
- **A catalogue behind a URL** is the shape two things share: the plugin marketplace (`spec/22`) and the workplaces an agent's sandbox starts from (`spec/16`). One JSON file, fetched with a cache, pinned by digest, deciding nothing on its own — `marketplace.Feed` holds the mechanism, and a third catalogue should use it rather than bring its own.

## The three repositories beside covey

Plugin code does **not** live in this repository. Three modules of their own carry it, and the separation is deliberate: a third party should have exactly the means we have, with no privileged "compiled in" tier.

| Repo | Module | Contents |
|---|---|---|
| [covey-plugin-sdk](https://github.com/benjaminLedel/covey-plugin-sdk) | `github.com/benjaminLedel/covey-plugin-sdk` | the contract: `target.System`, registry, `Descriptor`, credentials, sandbox helpers, HTTP client. **No dependencies** — a plugin author does not drag covey along. |
| [covey-plugin-pack](https://github.com/benjaminLedel/covey-plugin-pack) | `github.com/benjaminLedel/covey-plugin-pack` | the plugins covey ships with as ordinary Go code, the three that moved to the catalogue as wasm modules under wasm/, plus the manifest plugins |
| [covey-plugins](https://github.com/benjaminLedel/covey-plugins) | — | the catalogue: entries pointing at artefacts hosted anywhere, pinned by digest |

The dependency graph is acyclic: covey → SDK, pack → SDK, covey → pack (for the default build). **Nothing depends on covey.**

So whoever changes a plugin works in the pack, not here. Whoever changes the contract (a new interface, a new helper) changes the SDK — and has to remember that foreign plugins build against it: additions yes, renames only with a new major version.

## Where the code goes

A single-binary Go project with **`go.mod` at the repository root** — the code sits **beside `spec/`**, not in a subfolder. Frontend and migrations are compiled in via `//go:embed` and therefore have to live in the same module tree. Layout from `spec/10-architecture-stack.md`:

```
covey/                    ← repo root = Go module root (go.mod here)
  cmd/covey/              main.go — wiring, flags, subcommands (serve, migrate, bootstrap, doctor)
  cmd/coveyd/             the sandbox daemon: protocol client + runtime adapters
  cmd/covey-runner/       the remote runner: speaks the runner protocol, no database access
  internal/
    orchestrator/         dispatcher, state machine, daemon connections (control-plane side)
    agents/               registry, config compilation (SOUL.md → prompt)
    backlog/              backlog store, state transitions
    identity/             IdentityProvider — builtin/ (JWT/Argon2id) + oidc/
    secrets/              SecretStore — builtin/ (AES-GCM) + vault/
    runner/               the pool (control-plane side), the node (runner side), the protocol
    sandbox/              the workplace catalogue: profiles, published images, resolution
    marketplace/          catalogue fetching with cache (Feed) — shared with sandbox/
    target/               ONLY the plugin machinery: manifestplug/ (JSON engine), wasmplug/
                          (WebAssembly runtime), mcp/, store/ (activation per org).
                          NO plugin code — that lives in the pack, see above.
    guardrails/           policy engine, enforcement points
    observability/        recording, cost, alerts
    httpapi/              API/BFF handlers, RBAC middleware
  web/                    React/Vite frontend (dist/ embedded via //go:embed)
  migrations/             SQL migrations (up/down, embedded via //go:embed)
  go.mod
  ─────────────────────── (existing, stays beside it)
  spec/  mockup/
```

The reasoning: one binary → `go.mod` at the root, `web/` and `migrations/` as siblings of `cmd/` (otherwise `//go:embed` does not reach them). `internal/` keeps the packages private. For the pluggable ports (`identity/`, `secrets/`) the interface sits in the package root and the implementations in subpackages — "interface before implementation".

## When something new is built

- **The thinnest vertical slice first** (end to end rather than layer by layer), `builtin` everywhere, exactly one of everything.
- **Take the risky part early.** That was M1 (sandbox/daemon/runtime) and M4 (the `blocked` loop plus event correlation, with a design spike before the build); the rule generalises.
- The MVP's definition of done is the acceptance checklist at the end of `spec/11-mvp-plan.md`.

## Guard rails (from the design principles)

- **Never put long-lived secrets into the sandbox** — broker access at runtime, short-lived and scoped.
- **Enforce guard rails centrally**, outside the runtime, fail-closed — never leave them to the agent's prompt.
- **Crypto primitives yes, crypto protocols no:** sign JWTs / AES-GCM / Argon2id with proven libraries; do not rebuild an OAuth/OIDC server — that is the external provider's job.
- **Config as code:** agent behaviour (`SOUL.md`) is versioned, changed by PR and review, not by deploy.
- **A check that can never be satisfied is furniture.** Whatever the platform reports has to be able to end — by being fixed, or by somebody recording that they have taken the obligation. Two weeks of a permanent "!" and the finding beside it goes unread as well.

## Git

- Branch `main`. **Two remotes, and both get every push:** `origin` (GitLab, `gitlab.lapco.legal` — pipeline and deploy run here) and `github` (`benjaminLedel/covey` — the public version third parties install covey from). A branch on only one of them lets the two histories drift apart; "push" without further qualification means `git push origin <branch> && git push github <branch>`.
- **Commit regularly:** hold a finished, coherent step (a feature done, tests green) as its own commit — do not collect everything into one huge one.
- Do not commit to `main` without asking — create a branch first. Push only when explicitly asked.
- **Every change is represented by an issue**, and the issue comes first: what was observed, how it was reproduced, and what the code says about it — written so that somebody who was not there can follow it. Then the branch, then the commit that names the issue (`closes #<n>`), so the merge closes it. A bug found while fixing another one gets its own issue rather than being carried along quietly in a commit message. Issues belong in the repository that owns the code: plugin behaviour in the pack, the contract in the SDK, everything else here.
  The reason is not bookkeeping. A commit answers "what changed"; the issue answers "what was wrong, and how do you know" — and that second half is what a maintainer needs six months later, what a third party reads before installing covey, and what keeps the same fault from being diagnosed twice. Where a finding must not be public (credentials, a way in), it is written down where it belongs and the public issue stays with the harmless half.
