# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Two primary audiences, on two surfaces.

**The signed-in platform** (this repository) is optimised for **team leads and IT admins**, in that order of design weight:

- **Team lead / agent owner** — accountable for the agents of a department: maintains `SOUL.md` and config, prioritises the backlog, approves their agent's approval gates, reads the recording of a finished run, watches what it cost.
- **Org admin / IT** (`org_admin`) — operates the installation: runtimes and seats, runners, secrets, egress, guard rails, platform health, the fleet-wide kill switch.

Three further seats exist with narrower rights and are served by the same interface, not designed for first (`spec/09-enterprise-model.md`): **security/compliance** (sets org-wide guard rails an agent owner cannot soften), **auditor** (read-only on recording and audit trail, export for inspections), **controlling/finance** (cost per agent, department, cost centre; budgets). Above every org seat sits `accounts.platform_role = system_admin`, which governs the *installation* — tenants, registration, waitlist codes — and belongs to the login, not to a seat.

**The public website** (`covey.work`, separate repository `covey-web`) addresses **anyone interested in AI agents**, not only buyers or evaluators. Its job is to attract those people and get them to install covey.

## Product Purpose

covey is a self-hosted platform for running AI agents inside a company as employees: each agent has an identity, an isolated sandbox with a persistent home, brokered logins, a backlog and a place in the org chart. A person creates an agent, gives it access to the systems it needs, and it works through its backlog on its own. Everything it does is recorded; everything it costs is counted.

Success is that an organisation can hand out work, set the limits, and read back what was done — without trusting the agent's prompt to hold any of those limits.

## Positioning

- **The organisation is the unit, not the user.** Agents are org-owned resources with several human stakeholders (IT, team leads, security, audit, controlling), central governance and a company-wide org chart — not personal assistants in a single-user productivity tool.
- **The control plane is the product.** Sandboxes are commodity and runtimes are swappable; the value sits in scheduling, identity, governance and observability.
- **Guard rails are enforced outside the runtime**, centrally and fail-closed — at the broker, at the egress, in the tool layer. A neighbouring product that leaves limits to the system prompt cannot truthfully claim this.
- **Always reachable, compute only on demand.** An agent sleeps until a webhook, a schedule or a person wakes it, so an idle agent costs nothing.
- **Self-hosted and AGPL-3.0**, with a plugin ecosystem in which third parties have exactly the means the project has — no privileged "compiled in" tier.

## Operating Context

**Two surfaces, two repositories.**

- `covey` (public: GitHub `benjaminLedel/covey` + GitLab `gitlab.lapco.legal`) — one Go binary carrying the control plane, the sandbox daemon, the runner, and the React SPA and migrations embedded via `//go:embed`. Served at `:8494`; documentation under `docs/de` and `docs/en`. Installed by third parties with `docker compose up`.
- `covey-web` (private, GitLab only) — the Astro site at covey.work: home, product pages, integrations, documentation, blog, registration, legal pages. The website left the binary in August 2026 (covey#129/#130) precisely so that a stranger's on-premise instance serves a sign-in under `/`, not somebody else's advertising, and so that publishing a text is not a platform rollout.

**Recurring workflows.** Setup asks three skippable things (engine credential — checked before it is stored, a description of the company as org master data, and whether to hire a People department). Hiring runs *New agent → brief → generated config a human reads and changes → draft → hire*. Then: backlog, wake (webhook, schedule, person), run, recording with screenshots, wiki memory, org chart, cost breakdown per agent and per model, kill switch.

**Vocabulary that must stay stable** (`spec/README.md` glossary): *agent*, *engine* (the framework running the LLM loop), *runtime* (a configured seat: engine plus capacity), *seat* (what an agent occupies on a runtime), *workplace* (the sandbox side — deliberately never called a seat), *guard rail*, *backlog*, *covey* (a small flock that moves together).

## Capabilities and Constraints

- **Stack, signed-in surface:** Go binary + Postgres (pgvector) as the single anchor for state, queue, pub/sub, memory and AES-GCM secret columns; React 19 + Tailwind v4 + TanStack Query + react-i18next + react-router, built by Vite. `web/dist` must exist before the Go build. `spec/10-architecture-stack.md` names shadcn/ui as the intended component layer; the code carries no component-library dependency — components are hand-written under `web/src/components`.
- **Stack, website:** Astro, deployed on its own host.
- **A rebuild is not live.** `go build ./...` is a compile check only; making a change visible means `make build` plus restarting `covey serve`.
- **Strict CSP, no inline script.** The theme is therefore chosen in CSS (`light-dark()` over tokens, `data-theme` overriding), never by a script before first paint.
- **The interface is bilingual and that is binding.** Every UI string exists in both `web/src/locales/de.json` and `en.json`. `README.md`/`README.de.md` are kept in step.
- **What is not translated:** strings the program emits — error messages, log lines — and config syntax a parser reads (the `HEARTBEAT.md` keys `alle:`, `täglich:`, `nur-wenn:`, `titel:`, `aufgabe:`). They stand verbatim.
- **The repository itself is English** — spec, docs, commit messages, comments, branch and file names — because third parties install and extend covey from it.
- **Config as code:** agent behaviour (`SOUL.md`, `PLAYBOOKS`, `ACCESS`, `HEARTBEAT`) is versioned and changed by review, not by deploy.
- **Never put long-lived secrets in a sandbox** — access is brokered at runtime, short-lived and scoped.
- **Migrations are append-only**: never edit an existing one; numbers are consumed in order.
- **Undecided, and not to be invented:** commercial terms beyond the AGPL-3.0 licence — pricing, tiers, hosted offering — are not settled anywhere in the repository.

## Brand Commitments

- **Name and line:** covey — *"The IT and HR department for AI agents."* The codename is explained, not decorated: a covey is a small flock that moves together.
- **Design language:** an exactly neutral ground and its dark counterpart (the same sheet turned from paper to ink), separated by layering and hairlines rather than by hue; **Inter** alone carries the interface. Colour has one job and it is not "state": every state carries a drawn mark whose shape says what it is, and red is the single exception — it means "this ended against its purpose". The clay `#cc7a5b` stays the signet's colour and marks the brand; `--text-accent` is its text-capable cut for action and activity. Tokens live in `web/src/styles.css` (shared, render-blocking) and `web/src/app.css` (signed-in surface only). **Rule: no raw hex values in rule bodies — a colour is a token**, exempting only what is identical in both themes (the signet, white on saturated clay).
- **Voice:** sober, precise, no marketing language. A sentence says what changes and why, not what was touched. This holds for the website too — it persuades by being specific, not by adjectives.
- **Assets:** signet at `web/public/icon-192.png`. Licence AGPL-3.0.

## Evidence on Hand

- **A live instance:** covey.work runs this repository's `main` and is redeployed on every push. It carries a working QA agent and real target systems.
- **Product footage:** `web/public/shots/tour.gif` (seventeen seconds through workforce, backlog, recording, wiki memory, org chart, cost) and the stills beside it in `web/public/shots/`.
- **Written substance:** `spec/` (23 documents, English), `docs/en` and `docs/de`, `README.md`/`README.de.md`, `CONTRIBUTING.md`, `SECURITY.md`.
- **Working demonstrations:** `demo/fakezammad` and the fakemail double; the acceptance checklist as a running integration suite in `internal/integration/`.
- **Pitch material:** `praesentationen/*.pptx` — not to be edited unless asked.
- **Absent, and never to be fabricated:** named customers, testimonials, case studies, adoption or performance benchmarks, prices, and any claim of a hosted or supported commercial offering.

## Product Principles

1. **The organisation is the unit.** Every screen assumes several people with separated rights, not one owner — separation of powers is a feature, not friction to design away.
2. **A limit the platform states, the platform enforces.** Guard rails, budgets and egress hold outside the runtime and fail closed; the interface must never imply that a prompt is what holds them.
3. **Trust is the prerequisite, not the add-on.** Complete traceability, approval gates and a kill switch come before convenience; anything an agent did must be readable afterwards by someone who was not there.
4. **A check that can never be satisfied is furniture.** Whatever the platform reports has to be able to end — fixed, or recorded as accepted by a person. A permanent warning trains people to stop reading warnings.
5. **Say what it is.** Naming, copy and evidence stay concrete and sober; the product's credibility is the product's marketing.

## Accessibility & Inclusion

- **WCAG 2.2 AA is binding** for both surfaces — contrast, focus visibility, keyboard operability, target size, and status messages that reach assistive technology.
- Both light and dark themes ship and both must meet the bar; the theme follows the operating system unless `data-theme` overrides it.
- German and English are equal first-class languages, in the interface and on the website. Layouts must survive German string lengths without truncation.
