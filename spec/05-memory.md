# 05 — Memory

So that an agent is not a "goldfish with tool access" but someone who knows the shop, it needs memory across tasks — not just within a session.

## Two layers that must not be confused

| Layer | What | Where |
|---|---|---|
| **File persistence** | Files, artefacts, a task's transient working notes | the sandbox's persistent home (see [`02-agent-model.md`](02-agent-model.md)) |
| **Semantic memory** | "Customer X is handled via Y, looked after by colleague Z." — networked knowledge about tasks, customers, colleagues, systems | **the wiki**, maintained in the control plane |

The persistent home is enough for files but **not** for knowledge that carries beyond the individual task. The wiki exists for that.

## The model: an LLM-maintained wiki

The decisive design point: **it is the *relationships* that make an agent competent**, not textual similarity. A flat similarity search over accumulated free-text snippets finds "similar sentences"; it accumulates duplicates, does not manage contradictions and knows no structure. What the agent needs is the networked reality:

```
   Customer ──has──▶ Ticket ──solved_by──▶ Solution
     │                                        ▲
     └──looked_after_by──▶ Colleague ──knows──┘
```

Instead of extracting this structure into a separate graph store, the agent maintains it as what it is best at anyway: **linked Markdown pages**. One page per entity — customer, project, colleague, system, recurring problem — with explicit cross-references `[[Other-page]]`. The wikilinks *are* the graph: traversable, but without a graph database of its own and without an LLM→graph extraction pipeline.

The wiki has a fixed base structure:

| File | Contents |
|---|---|
| **Entity/topic pages** | one page per linkable entity; frontmatter (`type`, `tags`, `scope`, `source`, `updated_at`) + prose with `[[wikilinks]]` |
| **`index.md`** | a navigable catalogue: one line per page with a one-line summary, grouped by category. Updated on every ingest |
| **`log.md`** | append-only, chronological (`## [2026-07-25] ingest | title`) — a record of all ingests, queries and lint runs |

**Core rule** (also stated in the wiki schema handed to the agent): *a new page when it is a self-contained entity linkable from elsewhere; edit an existing page when it is an attribute or update of something already there.* The agent **invents nothing** — it only compiles from what it has actually learned in tasks.

## Retrieval: wiki + vector index

The pages are the source of truth. For finding things, a **vector index still sits on top of the pages** (`pgvector`, see [`10-architecture-stack.md`](10-architecture-stack.md)) — so the agent no longer finds random snippets but the *relevant pages*. Two routes complement each other:

- **Vector search** across page chunks → the thematically closest pages.
- **Structural navigation** via `index.md` and `[[wikilinks]]` → one hop onward from a hit page to the connected entities (customer → colleague → their open topics).

That preserves good retrieval, and the relationships that motivated the Graphiti plan emerge pragmatically from the linking.

**The embedding carries this retrieval — and is therefore a port, not a detail.** The built-in embedder (`HashEmbedder`) is feature hashing over words and bigrams: offline, deterministic, dependency-free — but a *lexical* measure. "The pipeline is red" and "The CI build is failing" have zero words in common and therefore similarity 0. With that the whole chain fails to engage: the agent does not find its own page again as soon as it phrases things differently, `Ingest` assigns nothing, and the consolidation pass detects no duplicates. For production a real embedding therefore belongs in front of it (`COVEY_EMBEDDING_PROVIDER`, see `internal/memory/embedder_api.go`). Two routes, the same seam: **run it yourself** (`ollama`, a small model on your own host — default EmbeddingGemma, multilingual, CPU-capable, Matryoshka-trained and therefore truncatable to the schema's 256 dimensions) or a **third-party service** (`voyage`/`openai` + `COVEY_EMBEDDING_API_KEY`). The self-hosted route is the more obvious one: wiki pages are customer knowledge, and this way they do not leave the house. The built-in remains the fallback when there is neither.

Vectors from different models are not comparable with one another. Every page therefore carries its model's fingerprint (`wiki_pages.embed_model`): search, ingest assignment and consolidation filter on the active model, and a switch brings the existing corpus along in the background at the next start (`ReembedStale`). A silent fallback to a different embedding deliberately does **not** happen — a mixed index would be worse than a uniformly weak one.

## Storage: hybrid (home working copy ↔ control-plane source)

The wiki lives twice over, with a clear division of roles:

- **Sandbox home — working copy.** The pages sit as real `.md` files in the persistent home. The agent reads and writes them with *normal file tools* — no special API, no friction. That is the natural writing surface.
- **Control plane — source of truth.** On task completion, changed pages are synchronised into the control plane (Postgres); from there the vector index is maintained and the wiki materialised into the home on every sandbox rebuild. That way the knowledge survives the loss of a "dumb and replaceable" sandbox (see [`01-architecture.md`](01-architecture.md)), is brokered, queryable org-wide and accessible to central governance.

This separation resolves the "files vs. control plane" tension: file ergonomics for writing, the control plane for persistence, retrieval and access rules. The sync runs over the daemon protocol, the same stable seam as the rest of the config/home exchange.

## Three operations

**Ingest** — in the **`done`** step (what did I learn / decide?) and proactively mid-run through `covey/remember` at the action proxy (see [`01-architecture.md`](01-architecture.md)), for generally applicable insights that should not wait until completion. The agent assigns the insight to the right page (new vs. edit), sets wikilinks and updates `index.md` + `log.md`. Distinction from task-related interim states (`covey/add_note`, see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)): if it only helps this task → note; if it also helps future ones → wiki.

**Query** — in the **`triage`** step (what do I know about this task / this customer?): vector search delivers the relevant pages, optionally extended by one wikilink hop, as a context block in the prompt. In addition the agent gets the **compact index** of its entire wiki (title + slug) — so it knows its whole body of knowledge, navigates deliberately and creates duplicates less often, instead of only seeing the top hits.

**Lint / consolidation** — **periodically as a scheduled maintenance job** (not in the hot path of the `done` step): merge duplicate pages, resolve contradictions between pages, mark outdated statements, find orphaned pages and missing cross-references. **This is the curation mechanic the flat snippet store lacked** — it keeps the wiki low on contradictions and lets knowledge *condense* with every task instead of merely *growing*. It can also be triggered manually for an agent through the UI.

Consolidation runs on **two levels**: (1) **central & deterministic** — the control plane's scheduled `Consolidate` pass merges embedding-similar duplicate pages, free of charge and without an LLM. (2) **agentic** — an optional, platform-wide configurable **cleanup heartbeat** (`COVEY_WIKI_CLEANUP`, e.g. `täglich 03:00`) periodically creates a backlog task for every agent in which it curates its own wiki with judgement: merge similar pages by content, smooth out outdated/contradictory statements, fix dead `[[references]]`. For that the agent has, alongside `wiki_search|read|write`, the tool **`wiki_delete`** (agent-scoped: only within its own wiki). The heartbeat is a normal system default heartbeat (`source='system'` in `agent_heartbeats`) — overridable per agent by a `HEARTBEAT.md` entry of the same name, visible and manually triggerable in the heartbeat tab.

## Integration into the lifecycle

```
triage:  wiki.query(context)      → relevant pages (+ 1 wikilink hop) into the prompt
working: (work the task; pages directly readable/writable in the home)
done:    wiki.ingest(insight)     → create/edit page, links, index.md, log.md; sync into the control plane
─────────
lint:    wiki.consolidate()       → periodic, independent of tasks
```

This makes memory not a separate feature but a fixed part of every work cycle.

Two quality rules at the ingest point:

- **No noise:** filler without informational value ("no new insights", "n/a") is discarded — the prompt instructs the agent not to touch the wiki in that case; the memory layer additionally filters as a safety net.
- **Manual curation:** humans with a manage role can create, change and delete pages through API and UI (bring onboarding knowledge along, correct outdated or wrong entries) — the induction conversation for the new employee. Manually curated pages carry `source: manual` in the frontmatter and thus stay distinguishable from what the agent learned.

## Scoping the memory

Open design question (see [`07-open-decisions.md`](07-open-decisions.md), D5): is the wiki **per agent**, **per team** or **shared**?

- **Per agent** — the cleanest isolation, but knowledge stays in silos.
- **Shared (team-/org-wide)** — a colleague agent benefits from others' experience, which strengthens the organisation metaphor; but it requires access rules at page level (not every agent may see every page).

Probably sensible: a per-agent core plus a shared org layer. Every page's `scope` frontmatter carries this decision; on query the control plane filters the visible pages. Decision open.

## Implementation status & relation to Graphiti

The MVP (M7) started with a **flat `pgvector` snippet store** (query@triage, ingest@done) — the honest, thin vertical-slice variant. The **wiki model is implemented** (migration `0031_wiki`, `internal/memory`): the unit is the **linked page** (`wiki_pages`) instead of the loose snippet, with a `pgvector` index for retrieval, wikilink extraction from the body, a consolidation pass (`Consolidate`) and the agent tools `covey/wiki_search|read|write|delete` through the action proxy. The old flat store (`memories`) remains as carried-over legacy data. The **hybrid storage is implemented too** (`internal/daemon/wikilocal.go`): the pages are materialised into the home as `~/wiki/*.md` at the start of a task and synced back at the end — the control plane stays the source of truth. That it *stays* so costs two precautions: on materialising, home files without a page in the control plane disappear, and before the sync back everything is reconciled against the current page list. Without both, a file left lying around would write back a page deleted or merged during the run — the agent's cleanup work would be undone at the end of the same run, and it would clean up the same thing again in the next one. Consolidation runs as a **scheduled maintenance job** in the control plane (`Orchestrator.ConsolidateWikis`, default every 10 min), no longer in the `done` hot path. In the **web interface** the wiki is a work surface rather than a list: on the left a **navigation tree** (first level the page type, below it the pages, expandable along their references), in the middle the rendered page, on the right the context — **backlinks including the sentence they sit in**, outgoing references and the neighbourhood as a small graph. Plus a **graph view** across the whole wiki: the linking is invisible as a list, and only in the graph can you see whether a wiki has a connected core or consists of loose sand.

Above that sits the **quality bar** (`GET /agents/{id}/wiki/health`): orphaned pages, dead references, pages without a type, diary titles (titles over 60 characters or containing a date — they name an event instead of a thing), suspected duplicates and stubs. The numbers are filters at the same time; one click shows only the affected pages in tree and graph. The finding is read-only — it does not replace judgement, it makes visible where judgement is needed. Still there: the chronological log (`wiki_log`), the button for manual consolidation, and the manual creation and editing of pages (now with a type).

With that, **the wikilinks replace the previously planned Graphiti knowledge graph**: the relationships live in the linking, not in a separate temporal graph store. Should real temporal reasoning be needed later, Graphiti can be retrofitted behind the wiki through the same interface pattern — the lifecycle contract (query/ingest/consolidate) stays the same.

> **Note:** there is a deliberate closeness to the previously explored "Cruu" concept (email as the first data source, knowledge extraction from live operations). The wiki here can conceptually inherit from it.

## The same memory for humans: the companion

The same wiki model also carries **human** employees' memory: the **companion app** ([`14-companion-memory.md`](14-companion-memory.md)) lets a person offload their entire brain load (audio, mail, screen recording, documents, links); a **memory curator** (itself an agent) condenses it into a wiki — the same infrastructure (linked pages, `pgvector`, consolidation), just with `human_id` instead of `agent_id` as the owner and pages that embed **media**. Private by default — and shareable on request **with one's own supervised agents**, so that a manager's knowledge (centrally enforced, fail-closed) flows into their `triage` as context.
