# 14 — Companion: brain dump & context for agents

covey treats agents like employees — with an identity, a workplace and **memory** ([`05-memory.md`](05-memory.md)). What the agents lack is the context from their humans' knowledge: mails, ideas spoken on the move, whiteboard photos, screen recordings, PDFs skimmed once. That knowledge sits scattered and gets lost.

The **companion** is a dedicated app alongside the covey web UI, a capture product of its own. Its purpose: to collect a person's brain dump in one place and make it available as context for their agents. The human offloads raw material, the platform condenses it into structured knowledge, the agents consume it. Unlike a private notes tool, sharing with agents is the actual purpose here, not an add-on.

Technically the companion is a separate client on covey's backend: the data lives in the control plane (Postgres, memory — [`10-architecture-stack.md`](10-architecture-stack.md)), there is one source of truth, and the agents read directly. No second store.

## Guiding principles

1. **One universal funnel.** Every source, every format may come in — audio, text, email, screen recording, photo, link, Word, PDF, any file at all. The human does not decide about structure while capturing; they offload.
2. **A text representation as the common denominator.** Every capture is reduced to a **text** (transcript / OCR / vision caption / extracted document text) that carries retrieval and embedding. The original is preserved and linked as a **medium**. The text serves finding, the medium serves fidelity. That way arbitrary input stays immediately usable for agents.
3. **The same infrastructure, a different owner.** The structured result is the wiki from [`05-memory.md`](05-memory.md) — the same page as the unit, the same `[[wikilinks]]`, the same retrieval and consolidation pass. Owner column `human_id` instead of `agent_id`. No second memory model.
4. **Private by default, sharing centrally enforced** (principle 7). The brain dump belongs to the human. What goes to the agents as context is decided by them — and the control plane enforces it, fail-closed. See "Data protection & governance".
5. **Frictionless capture.** Capturing has to be fast and possible on the move, otherwise the material never lands in the app.

## Capture — the universal funnel

The human produces raw material; the companion takes it in **any** format and puts it into an inbox as a **capture**. Every capture gets its text representation (for retrieval) and keeps its original (as an attachment):

| Source | Processing → text representation | Original |
|---|---|---|
| **Audio idea** | speech-to-text (on-device, see data protection) | audio *(optional, otherwise discarded)* |
| **Text / quick capture** | directly | — |
| **Email** | connect/forward a mailbox → subject + body (uses the existing IMAP plugin where applicable) | the original mail |
| **Screen recording** | transcript + keyframe OCR / vision | video |
| **Photo / screenshot** | OCR + vision caption | image |
| **Link / URL** | title and text extraction | URL + snapshot |
| **Document (Word, PDF, …)** | text extraction | file |

The **MVP** carries **audio (with transcription) and text**; all further sources follow the same pattern (raw input → text representation + attachment → ingest) and are additive. Screen recording and email import pull **desktop** in as a surface (see "Surfaces").

## Processing — the memory curator

The pure cosine assignment of the ingest ([`05`](05-memory.md)) carries terse entries but not a raw stream of mails, memos and PDFs in which people, projects and decisions run together. This **cut** is made by an LLM step — and in covey that is consistently **an agent** itself: the **memory curator**.

Instead of wiring an LLM call into the control plane, the triage is an org-owned agent with its own `SOUL.md`. It thereby inherits everything agents have anyway: **config as code** (the curation rules are versioned and changeable by PR; the human defines how their brain dump is cut), the runtime abstraction, cost accounting, guard rails and the shared LLM subscription (the "global token") as a credential. The global token only determines how the curator reaches the model, not what it does.

Sequence: new captures wake the curator (wake source "open captures for human H"). It reads the inbox (including the media's text representations) + the human's wiki index and decides as in the agent `done` step: new page vs. extend an existing one, set wikilinks, extract the core, **embed media into the appropriate page**, discard filler. Writing happens through a tool **scoped to exactly this human** into the `human_wiki` — the only case in which an agent writes into foreign (human) memory: on the human's behalf and in their ownership, fully audited. The purely mechanical cosine ingest remains as an LLM-free fallback.

Scope open (see "Open decisions"): one curator **per human** (personal, but many agents) vs. one **per org with human scoping** (frugal). Implemented as a template/role so that both work.

## The wiki with media

The structured result is the wiki from [`05`](05-memory.md) — but the pages are **Markdown with media**, not pure text:

- A page embeds media through standard Markdown (`![Whiteboard](covey-media://<id>)`) or lists attachments in the frontmatter. The wikilinks still carry the graph.
- **Blob storage as a port.** Media live in a `BlobStore` — "batteries included, but swappable" ([`10-architecture-stack.md`](10-architecture-stack.md)): builtin (file system/Postgres), swappable (S3-compatible). The page references only the blob ID; the vector index embeds the **text representation**, not the binary medium.
- **Retrieval stays text-carried.** Pages are found through the embedded text representation; the medium hangs off the found page. That way semantic search works across a corpus of images/audio/PDFs without presupposing multimodal embeddings.

## Context for agents

The agents consume the **curated wiki** (not the raw inbox) — the condensed layer. The route there is **sharing**:

- **Private by default; sharing explicit.** Every page is `private` or `shared with my agents`. Only shared pages are eligible.
- **Bound to supervision.** The recipients are only the agents supervised by the human (`supervisor_id`, [`02-agent-model.md`](02-agent-model.md)) — no org-wide reach (that would be the org scope, D5 in [`07-open-decisions.md`](07-open-decisions.md)).
- **One embedding space.** Human and agent pages share the vector space and the embedder; shared pages can therefore be mixed straight into the agent search. At `triage` the control plane extends the wiki query with the manager's shared pages, clearly marked out separately in the prompt ("## From your manager's memory").
- **Central, fail-closed, read-only, audited** (principle 7). The agent never reads by itself — the control plane selects and injects. No token onto human memory. Every inclusion is logged ([`06-observability-control.md`](06-observability-control.md)); the human sees which agent saw what and when. Revocation withdraws immediately (a reference at runtime, no copying).

**Media for agents — the expansion path.** Today the agent triages via a page's **text representation**; that makes it immediately usable. For an agent to see or hear media directly, the **hybrid home storage** from [`05`](05-memory.md) is extended: it materialises the shared pages into the sandbox as `~/wiki/*.md` anyway — additionally the linked media are materialised into `~/wiki/media/`, and the agent opens them with **normal file tools** (Claude Code reads local images multimodally, [`12-claude-code-adapter.md`](12-claude-code-adapter.md)). Multimodal depth then depends on the runtime, not on a special API. Extent open (see below).

## Tasks for agents

Not every captured thought is memory; some are an assignment. The same capture surface therefore also pushes **tasks into an agent's backlog** ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)): "turn this memo into a task for agent X". The human speaks/types, picks one of their supervised agents, the app creates a backlog item (title/body from the text representation) — against the existing control-plane API, by bearer auth, RBAC-scoped to the agents the human is accountable for (`supervisor_id` / agent owner, [`09-enterprise-model.md`](09-enterprise-model.md)).

The companion thereby also serves as mobile control of one's own agents: offload while on the move, either into one's own memory or as an assignment to an agent. The human makes that call — or the memory curator suggests it ("sounds like a task for agent X"). MVP: the call is manual; automatic suggestions are additive.

## Data protection & governance

An employee's entire brain load on company infrastructure is delicate — under employment law (co-determination/works council), under data protection law (GDPR: purpose limitation, data minimisation, data subject rights) and for acceptance. The basis is a clear promise: **the brain dump belongs to the person, not to the employer.**

- **Not a monitoring tool.** The personal offloading place serves the employee's productivity, not their surveillance. The platform that monitors *agents* ([`06-observability-control.md`](06-observability-control.md)) therefore does **not monitor the humans** — a hard promise, not a setting.
- **No employer reach-through by default.** No admin, no manager reads the personal dump. An org admin sees existence/metadata (offboarding), never content. Access to content only through a defined, logged compliance process (legal hold), ideally under four-eyes control — never casually.
- **Encryption at rest.** Text representations *and* media blobs are encrypted (AES-GCM, the same pattern as the secret columns, [`04-identity-secrets.md`](04-identity-secrets.md)), with keys scoped such that casual reading along is excluded technically — not just by policy.
- **Data-minimising capture.** On-device STT: the raw audio never leaves the device, only the transcript is stored (the audio optionally discarded afterwards). Analogously, the human chooses whether an original medium (video, mail) is kept at all or only its text representation.
- **Transparency for the human.** Every access — the curator agent, every inclusion of a shared page in an agent — is visible to the human. No silent access.
- **Sharing crosses a data-protection boundary — made visible.** As soon as the human shares a page with their agents, that content can appear in the **monitored** agent activity (session recording, visible to security/audit). Sharing is therefore the deliberate step out of the private and into the governed space — the UI has to name that consequence clearly, not hide it.
- **Data subject rights.** Deletion at any time (capture/page/medium); offboarding cascades (`ON DELETE CASCADE` on `humans`) and thereby also withdraws previously shared context; export for access requests/portability.

## Surfaces

Two interfaces on the same control-plane API:

- **The companion app (mobile, Flutter).** The primary capture surface on the move: offload audio/text/photo/link, search, read pages, share, push tasks. Authenticated by bearer token (see "Technical implementation").
- **Desktop.** **Screen recording** and **email/file import** need a computer surface — open whether as a desktop build of the same Flutter app or as a slim companion. *An expansion stage.*
- **Web (person page).** The existing React SPA shows one's own brain dump on the person page: pages (with source, media, wikilinks), the log, manual curation, share management and the audit view "who saw what".

## Technical implementation (reference)

Deliberately thin — the mechanics are in [`05`](05-memory.md), here only the deltas:

- **Schema.** `human_wiki_pages` / `human_wiki_log` mirroring `wiki_pages` / `wiki_log`, owner `human_id UUID REFERENCES humans(id) ON DELETE CASCADE`, visibility (`visibility: private | shared`). Plus `captures` (the inbox: source, status, text representation, blob ref) and an attachment/blob table. A new migration; never edit existing ones.
- **An owner-agnostic store.** `internal/memory.Store` is parameterised with the table and owner column names (`NewStore` = agent behaviour unchanged, `NewOwnerStore` for humans). Ingest, query, consolidate, log — identical code, a different owner. No duplication.
- **BlobStore port.** Media storage behind a narrow interface (builtin file system/Postgres, swappable S3), analogous to `SecretStore`. Pages reference blob IDs.
- **Extraction pipeline.** One extractor per source (STT, OCR/vision, document text, HTML→text) → text representation. As a registry/plugin pattern like the target systems ([`13-zammad-integration.md`](13-zammad-integration.md)).
- **Endpoints on the signed-in human.** `/api/v1/me/captures` (POST upload/capture), `/me/memories` (GET/POST), `/me/wiki/log`, `/me/wiki/consolidate`, `/me/memories/{id}` (PATCH/DELETE) — scoped through the authenticated principal, no detour through an agent.
- **Mobile auth.** The existing session infrastructure (`http_sessions`, [`04-identity-secrets.md`](04-identity-secrets.md)) is reused: the login additionally returns the session token in the body, and the `auth` middleware accepts it via `Authorization: Bearer <token>`. No new JWT path for humans.
- **Memory curator.** An agent template with a `SOUL.md`; a write tool scoped to the respective human (`covey/human_wiki_write`) at the action proxy, woken through the wake source "open captures". Uses the global LLM token like any agent.
- **Media into the sandbox.** The hybrid home materialisation ([`05`](05-memory.md)) is extended by `~/wiki/media/` so that agents read media with normal file tools.
- **Task push.** Reuse of the existing backlog API ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)), RBAC-scoped to `supervisor_id`/agent owner.
- **Consolidation.** The control plane's scheduled maintenance job runs across the human wikis too.

## Open decisions

In addition to [`07-open-decisions.md`](07-open-decisions.md):

- **Multimodal agent use.** How deeply agents really exploit media (only the text representation vs. images/audio via materialised files vs. multimodal embeddings) — extent and order open; depends on the runtime ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
- **Media storage backend.** Builtin (file system/Postgres) suffices for the vertical slice; from what volume onwards S3-compatible? Dedup of identical media, size/retention limits.
- **Retention of the raw medium.** What is kept after extraction (only text, text + original, time-limited)? Data minimisation vs. traceability.
- **Extraction quality.** STT, OCR and document extraction determine how well the curator cuts. On-device vs. server, choice of model.
- **Embedder quality.** The `HashEmbedder` carries the vertical slice but delivers no real semantic proximity — particularly relevant for the freer texts of a brain dump. A priority for the swap to a real API embedding.
- **Isolation of shared context.** Preventing an agent from taking shared manager context over into its *own* wiki page (and keeping it after revocation) is currently only addressable prompt-side. A clean enforcement point is open.
- **Curator scope.** One curator per human vs. per org with human scoping vs. configurable per human. MVP leaning: an org default, overridable per human.
- **Sharing granularity.** Per page (simple) vs. per area/project/"space" (more ergonomic) vs. per tag. MVP: per page.
- **The task call automatically.** Whether the curator only suggests "memory vs. task for agent X" or (with confirmation) triggers it itself.
- **The employer access model.** The legal-hold process (who may see content in a compliance case, under what four-eyes procedure) has to be formally balanced with [`09-enterprise-model.md`](09-enterprise-model.md) and [`06-observability-control.md`](06-observability-control.md).
- **The desktop surface.** A desktop build of the Flutter app vs. a slim companion for screen recording and mail/file import.
- **The return channel agent → human.** An agent highlights something important → it lands in its supervisor's inbox. The mirror image of sharing, left out here for now.
- **The product name.** The companion as a product of its own needs a name (working title "Companion").
- **Org scope (D5).** A shared org memory that humans *and* agents feed into is the next expansion stage beyond personal sharing.
