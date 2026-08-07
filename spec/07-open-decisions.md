# 07 — Open decisions & MVP scope

Status: the outcome of the brainstorm. These points are deliberately open and should be nailed down early, because they cascade heavily. Decided points remain in place marked `✅ *decided*` — the reasoning belongs to the decision and would otherwise be lost.

## Open decisions

### D1 — Event correlation *(highest priority, next step)*

How is an incoming event (mail reply, ticket update) reliably mapped onto a parked (`blocked`) task?

Options: reply-to with a task ID · a central event router · hybrid (details in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).

This decision determines how "real" the email-as-a-bus idea turns out to be, and it influences the scheduler, the wake logic and the whole `blocked` mechanism. **Settle it first.**

### D2 — Sandbox: build vs. buy

Operating Firecracker/gVisor ourselves vs. sandbox-as-a-service (E2B, Beam, Northflank). Persistent volume + ephemeral compute is settled; the question is the operating form. Recommendation from the brainstorm: **do not build the sandbox infra from scratch** — the differentiated part is the control plane (details in [`01-architecture.md`](01-architecture.md)). Market findings and concrete building blocks in [`08-market.md`](08-market.md) (among them Daytona's switch to closed source in June 2026). How the project-specific toolchain gets into the sandbox is decided independently of this — see D11.

### D3 — Agent identity: real user vs. service account

Decide per system (real identity + native audit vs. lean/licence-free). Influences cost (licences) and how real the org chart becomes (details in [`02-agent-model.md`](02-agent-model.md)).

### D4 — Backlog store: existing ticket system vs. our own store

Repurpose an existing ticket system (a shared task reality with humans, a strong org-chart feeling) vs. a leaner store of our own (less coupling). Details in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).

### D5 — Memory scoping

Per agent, per team or shared (with access rules at wiki page level, `scope` frontmatter)? Probably a per-agent core + a shared org layer. Details in [`05-memory.md`](05-memory.md).

### D6 — Which engines

Which engine does the adapter set start with, and which follows? Claude Code first (familiar, CLI-based, easy to bootstrap) — that is built ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)).

**Second is Codex, not OpenHands.** The reason is coverage rather than preference: an organisation wants Claude *and* ChatGPT, each through an API key or a subscription, and those four cells are the realistic demand. Codex fills the two that are missing, and being from another provider it is also the harder test of whether the engine seams hold — two of them did not, and that is written up in [`19-codex-adapter.md`](19-codex-adapter.md).

Three of the four cells are straightforward. The fourth, Codex on a ChatGPT plan, is the awkward one: its credential is a **file** rather than an environment variable, and the documentation treats non-interactive use of it as a special case.

Whether the cells are equally *capable* is open and hangs on one question — whether `codex exec` can resume a session. If it cannot, ChatGPT is not "also supported" but "supported for the agents that never block", which excludes the MVP's core case. That is why resume is a declared engine capability, checked at assignment ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)).

Beyond that: OpenHands/Harness stay possible but are not the next step, and D14 asks whether a harness-less direct engine belongs alongside them.

### D7 — Codename ✅ *decided*

**Covey.** A *covey* is a small, coordinated flock — abstract on the surface, with a quiet meaning underneath. "A covey of agents" as the guiding image.

### D8 — Email identity per agent

Email is **optional** (see [`02-agent-model.md`](02-agent-model.md)): which agents really need one? Rule of thumb: only when the role requires human↔agent communication or email-based wake triggers. Purely event/ticket-driven automation agents manage without. To be decided concretely per agent type.

### D9 — Tenant model

Single-org self-hosted (one company, one instance) vs. multi-tenant capable (several isolated organisations on one instance). Single-org suffices initially; multi-tenancy is a later expansion stage but — if aimed at at all — has to be considered in the data model from the start for data and policy isolation. Details in [`09-enterprise-model.md`](09-enterprise-model.md).

### D10 — Backend language: Go vs. Kotlin

Both are viable for the concurrency-heavy orchestration core. **Leaning Go** (single-binary deployment, ecosystem proximity to kagent/sandbox SDKs, AI writes idiomatic Go) vs. **Kotlin** (a richer type system for the policy engine, structured concurrency). The frontend stays TS/Tailwind in either case. Trade-off table in [`10-architecture-stack.md`](10-architecture-stack.md).

### D11 — A project-appropriate sandbox environment ✅ *decided*

How does the project-specific toolchain get into a sandbox that hangs off the **agent**? A developer agent works on several projects with different technologies and versions; its container, however, starts on wake, before it is settled which ticket from which project comes up. "One image per project" therefore presupposes one agent per project.

Decided: **version → home, toolchain → image.** The image carries system packages (PHP, JDK) and the version managers (`fvm`, `uv`); the SDK **versions** are fetched by the agent itself into the persistent home, following the pin in the project repo.

The image hangs off the **agent**, not off the language: a profile is a *union* of toolchains (`base` for support, mail and QA agents, `dev` for developers), so that the same agent can work on a PHP **and** a Flutter project. One image per *language* would bring back exactly the question rejected above — which one do I start on wake? That the selection currently runs instance-wide via `COVEY_SANDBOX_IMAGE` and is not yet a field on the agent is an open build task, not a property of the decision: see [`16-runner.md`](16-runner.md), where the image also becomes a runner capability.

Rejected: **installing packages at runtime through the UI.** Three reasons, all structural — the agent runs as non-root (Claude Code refuses `--dangerously-skip-permissions` as root); a package manager on the egress allowlist is a generic code-execution channel and not a target-system host; and a sandbox whose tools arise from a click list plus the state of a mirror is no longer reconstructible from config + home — the core of the "dumb and replaceable" promise from [`01-architecture.md`](01-architecture.md).

The operations side including egress templates is in [`../docs/ops-deployment.md`](../docs/ops-deployment.md).

### D12 — Distributed data plane: runners ✅ *decided*

How does the data plane get from **one** host onto many? Today the control plane starts sandboxes through the local Docker CLI: the size of the workforce is therefore the size of one machine, and data residency per department or hardware proximity (ARM, GPU, a host inside the target system's network) cannot be represented.

Decided: **registered runners modelled on GitLab runners** — a standalone process on an arbitrary host registers with a registration token, holds an outbound connection and gets sandboxes assigned to it from there. The `SandboxProvider` port remains the seam; the orchestrator notices nothing of it. In full in [`16-runner.md`](16-runner.md).

The runner is a **release artefact of its own** (a binary per architecture, a Docker image, a systemd unit, its own version number, a configuration file with the server address and token) — it is installed on machines where nothing else of Covey lives. Its code stays in the **same repository**, though, modelled on `coveyd`: that too is a separate binary on a foreign machine, and `internal/daemon` defines its protocol for both sides. That way a protocol change stays a single commit instead of a compatibility dance across two repositories. A separate repository stays possible once the protocol has settled; for the operator it changes nothing, because they see the artefact, not the repository. One runner process serves **exactly one** Covey instance — block storage shared across two organisations would be a channel between them.

The real question behind this is not registration but the **persistent home** — and there the first impression deceives. A measured developer home of 7.1 GB consists to over 99 % of things that already have a source elsewhere: 3.0 GB of checkouts (the Git remote), 4.0 GB of caches and SDKs (derivable from the pin in the project repo), plus the wiki and skills that the control plane maintains anyway. Only 48 MB are unrecoverable: the Claude Code session transcripts and the agent's self-written tools, analyses and interim results.

What is decided is therefore **not** binding but a **central home store**: after every job the home is synced there as a whole and materialised from there on wake — content-addressed and deduplicated block-wise, so that only changed blocks travel. That makes an agent's home on a runner a **disposable working copy**, and it follows the pattern already built for the wiki ([`05-memory.md`](05-memory.md), "hybrid storage"; `internal/daemon/wikilocal.go`) — just without its restriction to curated content.

**Deliberately no list, neither positive nor negative.** An agent puts interim results and self-written tools all over the home, not into a folder provided for the purpose: in the measured home, alongside the transcripts, sit 95 KB of extracted code, several analysis JSONs and two task directories directly in the root. Every list would be a rule that can be wrong, and its error would already cost work that has been paid for. A complete sync never asks the question "what is valuable?" in the first place — deduplication makes it affordable, because the 4 GB of toolchain caches sit org-wide once instead of once per agent. Exclusions stay possible but are **optimisation rather than politics**: empty by default, and when in doubt it is synced.

Two consequences that must not be skimmed over. First: for 99 % of its content the store is a cache, for the 48 MB of unique work it is the **only copy** — it therefore requires backup like the database and cannot be deleted at will. Second: GB-sized blocks do not belong in Postgres. Another port arises following the pattern of `IdentityProvider`/`SecretStore` — **`BlobStore`** with `builtin` (a directory next to `data/homes`) as the default and `s3` as a switchable implementation. Deliberately **S3-compatible rather than "AWS S3"**: Covey does not prescribe a server but speaks the protocol that Hetzner Object Storage, Garage, MinIO and Ceph have in common. Which client library will be used for that is **left open** — `minio-go` costs a measured 18 indirect modules and 41 compiled foreign packages, while Covey today makes do with 22 modules in total and needs only five operations from an S3 client (multipart is not needed, because blocks are small and immutable). That will be decided when the backend is built. The runner never gets the store's credentials but short-lived, scoped URLs — the same rule as at the secrets broker, and it also keeps the payload out of the control channel.

Warmup falls out of this rather than being built specially: if the runner keeps the fetched blocks locally, its block cache is the shared holding. Two developer agents on one host then hold the same 4 GB once instead of twice, and a new agent starts almost warm on a run-in runner. The isolation question that would otherwise arise — a shared cache is a channel between agents — is eliminated by construction: a block is requested by the hash of its content, and which blocks make up a home is written in the respective agent's snapshot. Nothing can be slipped in under a foreign hash.

From this follows the runner's role: **affinity is a scheduling preference, not a binding.** The scheduler prefers the runner the agent last ran on, because checkouts and caches lie warm there; if it is missing, the agent wakes up elsewhere and *stays there* (no migration back, otherwise a failure wave is followed by a second one). A cold start costs minutes — but time, not work. A runner failure makes an agent slow, not unable to work, and failover falls out without a mechanism of its own.

A cold start no longer means "everything anew from the internet": the runner fetches only the blocks it lacks. The only expensive case that remains is the **fresh host** on which not a single block sits yet — after that, nobody.

Rejected: **binding with an explicit move** (the agent is pinned to a runner, a change is manual work) — it turns a host failure into an employee standing still and would only be necessary if the home really were irreplaceable. Likewise rejected: **shared storage** (NFS/CSI) — it relocates `node_modules`, `vendor` and Gradle caches onto a network file system and makes an operational decision about the host a prerequisite of the platform. And **the whole home as a Git repo**: `repos/` are checkouts themselves (nested repos, duplication of an existing remote), caches belong in no history, and transcripts are append-only JSONL, on which Git bloats. What makes Git attractive — dedup and history — the content-addressed store delivers anyway.

**The prerequisite that carries this decision in the first place:** the home store has to be in place before the first agent runs on a second host. It is at the same time the only stage that pays off even **without** runners: today a deleted home is unrecoverable, and two developer agents on the same machine hold the same 4 GB twice.

A runner is **trusted infrastructure of the organisation** — it sees the daemon and egress tokens of the agents it hosts and can therefore impersonate them. It is not a way of bringing in foreign compute capacity. From this follows in particular: no database access from the runner, mandatory TLS, revocation and audit per runner.

### D13 — Runtimes as contracts: scope, utilisation, fixed cost ✅ *decided*

Once an organisation holds several LLM credentials, "which runtime" stops being a technical property and becomes a commercial one — a runtime is then an **engine plus the capacity to run it**, and an agent is assigned to it the way an employee is assigned to a cost centre. The model is described in [`18-runtimes-capacity.md`](18-runtimes-capacity.md). All three questions it raised are settled; they are kept here with their reasoning because two of them constrain the implementation.

**Is a runtime bound to one organisation? ✅ Yes.** A runtime carries credentials, and a credential reaching across tenants would be a channel between them — the same reason a runner serves exactly one Covey instance ([`16-runner.md`](16-runner.md)). The storage layer had in fact already decided it: the built-in secret store binds every ciphertext to its organisation through the AES-GCM AAD, so a shared credential would not decrypt for the second tenant at all. The schema follows that instead of fighting it.

The cost is real and accepted: a group with one provider contract and several subsidiary tenants deposits the credential per tenant, and its seats cannot be pooled across them. That is a **billing** problem rather than a credential one — costs can be rolled up across organisations later without any tenant holding another's secret, which is the cheaper direction to solve it from. This was the one question in D13 that is a data question rather than a policy one; the two below can be retrofitted.

**What is the scope of an engine's utilisation figure? ✅ Account-wide, but rate limited.** Settled from the Claude Code documentation. The plan usage bars come from a **server-side usage endpoint**, not from local history — the documentation gives it away in the failure case ("when the request for your plan limits fails, most often because the usage endpoint is rate limited, `/usage` shows the last usage bars it loaded on this machine within the past 60 minutes"). The caveat about "local session history on this machine" applies to the *attribution* breakdown underneath (which skill, which MCP server contributed), which Covey does not need.

Three consequences for the implementation:

- **Query centrally per credential, cached — never per run.** The endpoint has a rate limit of its own, and a fleet asking on every wake will hit it. Three agents on one seat get the same answer anyway.
- **Read the staleness note.** A rate-limited request yields bars up to 60 minutes old with a `Showing last-known usage` marker; treating that as fresh would be worse than having no figure.
- **Prefer a real API where the plan offers one.** On Team/Enterprise plans an Enterprise Analytics API (`read:analytics`) returns usage and cost per user as structured data, and Teams plans export a spend report; `/usage` scraping is the fallback for individual Pro/Max subscriptions, and the platform's own estimate the last resort.

One property of seats is worth carrying into capacity planning: the allowance is **shared with Claude chat and Cowork**, so a seat a human also works on has less left for agents than the contract suggests.

**How is a fixed cost entered and spread? ✅ It is not.** Covey keeps **one** cost figure — the one the engine reports, computed from token counts at standard list prices — and books it unchanged for every credential. No fixed seat amount entered by hand, no apportionment at period close.

The exactness that would buy is not worth its apparatus: a second cost model has to be maintained, can be wrong in its own ways, and still would not be the invoice. What is required instead is a **label**, because the same number means two things: billed spend on a metered credential, and a list-price equivalent on a subscription seat. As a measure of consumption it is comparable across both; as spend it is exact on one and an upper bound on the other — and it errs by making agents look more expensive than they are, which is the safe direction for the decision the figure is used for. Spelled out in [`18-runtimes-capacity.md`](18-runtimes-capacity.md) and [`17-kpis.md`](17-kpis.md).

**One addendum, found at the second engine:** "take what the engine reports" presumes an engine that reports something. Codex returns token counts and no money ([`19-codex-adapter.md`](19-codex-adapter.md)), so the platform needs a **price list** per model and token kind to price those itself. That is a lookup table producing the same list-price equivalent one level further in — not the fixed-cost apparatus rejected above. A model the list does not know yields *no* figure rather than a guessed one.

### D14 — A direct engine for work that needs no harness

Should Covey ship an engine that talks to a provider API directly — single turn, no tools, no sandbox — alongside the harness engines (Claude Code, Codex)?

The case for it: not every task is agentic. Classifying a ticket, summarising a thread, extracting fields — that is one model call, and driving it through a coding harness pays for a tuned system prompt, tool definitions and a multi-turn loop in order to answer one question. Such an engine would need no sandbox (so it could run in the control plane and be cheap enough for high-frequency work), would be provider-agnostic by construction, and its cost would be exact rather than notional.

The embryo exists: `internal/claudeapi` is exactly this path — tool-less, single-shot — used today by the config copilot and the dream. It is simply not modelled as an engine, and it is wired to one provider.

The case against, and the line not to cross: this must not become **Covey implementing an agent loop**. Tool use, context compaction and above all session resume — on which the whole `blocked` mechanism rests ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) — are what the harness engines provide, and rebuilding them makes the platform a harness vendor, against design principles 2 and 3. The distinction to hold is **agentic versus not**, not CLI versus API. Discussion in [`18-runtimes-capacity.md`](18-runtimes-capacity.md).

## Proposed MVP scope

The goal: the shortest path to **one real agent that behaves like an employee**, not the full fleet.

**In scope (MVP):**

1. A control plane with scheduler + dispatch loop for **one** agent type (e.g. a support agent).
2. **One** runtime adapter (proposal: Claude Code).
3. A persistent sandbox with a persistent home (via a buy solution, D2).
4. Config as code: `SOUL.md` + a minimal set of MD files, compiled into prompt/config.
5. A secrets broker with Keycloak + RFC 8693 for **one** target system (e.g. the ticket system).
6. The backlog as a first-class object with the full state machine including `blocked`.
7. Event correlation for the one `blocked` case (decision D1 implemented).
8. **A minimal central guard-rail set** — platform-enforced: egress deny to the outside without approval, deny for non-approved systems/tools, mandatory approval for destructive actions. Fail-closed.
9. Session recording + kill switch + simple cost tracking.

**Explicitly later (not MVP):**

- Further runtimes/adapters.
- The supervisor agent (AI-assisted anomaly detection).
- A shared org-wide memory.
- Inter-agent communication over A2A/MCP (the email bus suffices for now).
- A fully built-out admin dashboard (a minimal view suffices).

**Guiding question for the MVP:** can a support agent triage a ticket, answer it itself or escalate it, go cleanly `blocked` on a follow-up question, wake up correctly again on the incoming answer and write the solution into memory — fully recorded, fenced in by central guard rails and with a kill switch? If yes, the core stands.
