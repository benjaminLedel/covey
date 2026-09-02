# FR-003 — Strict separation of organisations

Status: **Proposed** · As of: 2026-08-11

> Feature requests are proposals, not yet settled spec. If a request is accepted,
> its content moves into the responsible `spec/` document and this document is
> set to *accepted* / *rejected*.

## In short

Everything in covey is org-scoped **by intent**. This document records where it
is not so **in fact**: seven places where data or a write crosses the boundary
between two organisations. Five of them are a few lines each, one is a routing
decision, one is the data plane.

None of this matters on a single-tenant installation, which is what covey has
been until now. All of it matters the moment one instance carries strangers —
which is exactly what [`002-plattform-registrierung.md`](002-plattform-registrierung.md)
proposes. **This request is a prerequisite for that one**, not a follow-up: an
instance that accepts self-registration while the findings below stand hands
every new tenant a view into all the others.

## Motivation

The boundary has failed once before, and the fix is documented in the code
itself (`internal/httpapi/server.go:552-560`): the check "does this object belong
to the caller's organisation?" used to be every handler's own business, and *in
25 out of 67 it was simply forgotten*. The answer then was the right one — turn
it into a middleware you cannot get past (`agentScoped`, and later `taskScoped`,
`stageScoped`, `pageScoped`).

That family covers the objects it knows. It does not cover the ones that came
afterwards, and it cannot cover a store method that quietly accepts an id
without an organisation. So the same class of gap grew back in the places the
middleware does not reach — 52 routes with a path parameter still run through
bare `s.rbac`, where the handler is on its own again.

The point of this request is therefore not only to close seven holes. It is to
make the eighth one hard to open.

## What already holds

Worth stating plainly, because it is the pattern the fixes should copy rather
than invent:

- The scoped middleware family (`agentScoped` / `taskScoped` / `stageScoped` /
  `pageScoped`) resolves the object, compares the organisation, and answers
  *not found* rather than *forbidden* — the existence of a foreign object is
  itself a disclosure.
- `secrets.Assign` checks **both** sides in one query — the secret *and* the
  agent against `org_id`. This is the model for every "attach X to Y" action.
- `AddDepartmentLead` inserts through `SELECT … WHERE id=$2 AND org_id=$3`, so a
  foreign member cannot be linked even in principle.
- `ensureNoManagerCycle`, `SetHumanDepartment`, `runtimes.Assign`,
  `DecideApproval`, `reqlog.Get/List/Clear` and `Obs.GetBlob` are all correctly
  scoped.

The gaps below are the exceptions, not the rule.

## Stand

**A–F sind behoben** (Commits `e3e5339`, und der hier beschriebene Durchgang);
die Tests dazu stehen in `internal/integration/isolation_test.go` und
`platform_test.go` — jeder versucht genau das, was vorher ging. Offen bleibt
**G**, das gemeinsame Docker-Netz, und die drei strukturellen Punkte weiter
unten.

Nachgezogen wurde inzwischen auch das **Wort**: die oberste Organisations-Rolle
heißt seit Migration 0061 `org_admin`. Befund F hatte die Rechte getrennt, aber
beide Ebenen weiter „Plattform" genannt — eine Oberfläche, die „Plattform-Admin"
zeigt für eine Rolle, die jede Organisation an sich selbst vergibt, lädt genau
die Verwechslung wieder ein, die der Befund ausgeräumt hat. „Plattform" gehört
jetzt der Instanz, „org" der Organisation, und die zwei Verwaltungsbereiche
(*Administration* je Organisation, *Plattform* je Installation) machen die
Trennung sichtbar statt nur wirksam (`spec/09-enterprise-model.md`).

## Findings

| # | Finding | Class | Effort |
|---|---|---|---|
| A | The SSE event bus broadcasts every event to every tenant | cross-tenant read, continuous | **behoben** |
| B | Webhooks resolve the agent by slug **across organisations** | misrouting of real work | **behoben** |
| C | `dream-actions/{id}/undo` has no organisation check | cross-tenant write | **behoben** |
| D | `skills.Assign` does not check the agent's organisation | cross-tenant write | **behoben** |
| E | Egress template assignment does not check the template's organisation | cross-tenant read + guard-rail widening | **behoben** |
| F | Every `platform_admin` administers every tenant | privilege boundary | **behoben** |
| G | All sandboxes share one Docker network | data-plane isolation | large |

### A — the live event bus knows no organisation

`Broadcaster` (`internal/orchestrator/broadcast.go`) carries no organisation, and
`handleSSE` (`internal/httpapi/sse.go`) subscribes without one:

```go
ch, cancel := s.Orch.Events().Subscribe()   // no org, no filter
```

All seven publish sites (`orchestrator.go:708, 1502, 1736, 2436, 2454, 2478,
2528`) push `agent_status`, `task`, `recording`, `guardrail` and `approval` to
**every** open connection. The payloads are metadata rather than content — agent
and task UUIDs, statuses, action names such as `gitlab/create_merge_request`,
guard-rail decisions, approval ids — but any signed-in human of any organisation
watches another tenant's fleet work in real time, with no id to guess and no
role to hold: `/api/v1/events` hangs on bare `s.auth`.

**Fix.** `Event` gains `OrgID`; `Subscribe(orgID uuid.UUID)` keeps it on the
subscription and `Publish` compares before the non-blocking send. Every publish
site already has the agent in hand, which carries `OrgID`.

### B — a webhook finds its agent across organisations

`findWebhookAgent` (`internal/httpapi/spa.go:177`) resolves the address part of a
webhook URL through `Registry.FindBySlug` (`internal/agents/agents.go:209`):

```sql
SELECT … FROM agents WHERE slug=$1 ORDER BY created_at LIMIT 1
```

No organisation, and **the oldest agent wins**. Agent slugs are unique only
*per organisation* (`UNIQUE (org_id, slug)`, `migrations/0001_init.up.sql`), and
the slugs that a second tenant picks are the same ones the first picked:
`support`, `qa`, `dev`. The consequence is not a disclosure but a misdelivery:
org B's ticket payload is handed to org A's agent, processed in org A's sandbox,
and answered through org A's credentials — in the target system of org B.

The code names the assumption itself: *"MVP: one organisation — the agent is
resolved across orgs."* It was true when it was written.

**Fix.** Address a webhook by something that is unique instance-wide: the agent
id (already accepted as a fallback in the same function) or, better, the
webhook token — `/api/trigger/{token}` demonstrates the pattern and is
unaffected by this finding. Slug resolution can stay only as a compatibility
path while the slug is unambiguous instance-wide; otherwise it must refuse
rather than pick. What the UI hands out (`handleGetAgentWebhook`) and the
runbooks under `docs/ops-*.md` change with it.

### C — undoing a dream in a foreign organisation

`POST /api/v1/dream-actions/{id}/undo` runs on `s.rbac(manage, …)`, and the store
never sees an organisation (`internal/dream/dream.go:212`):

```go
func (s *Store) Undo(ctx context.Context, actionID uuid.UUID) error {
    // dream_actions → dreams → agent_id, then mem.UpdatePage(...)
```

Any `platform_admin` or `agent_owner` of any organisation can undo a retitle in
another tenant's wiki. The blast radius is small — `retitle` is the only
undoable kind — but it is a write across the boundary.

**Fix.** `Undo(ctx, orgID, actionID)` with the organisation in the join, or a
`dreamActionScoped` middleware in the family of the existing ones.

### D — assigning a skill to a foreign agent

`skills.Assign` (`internal/skills/skills.go:418`) checks that the *skill* belongs
to the caller's organisation and then links it without checking the *agent*:

```go
`SELECT agent_id IS NULL FROM skills WHERE org_id=$1 AND id=$2`   // skill: checked
`INSERT INTO skill_assignments (skill_id, agent_id) VALUES ($1,$2)` // agent: not
```

A skill is a procedure that reaches the agent's instructions. Linking one to a
foreign agent injects text into another tenant's agent.

**Fix.** The two-sided `EXISTS` that `secrets.Assign` already uses. `Unassign`
is unaffected — it joins through `skills` and is scoped.

### E — attaching a foreign egress template

`SetAgentTemplate` (`internal/egress/store.go:292`) takes the template id
straight from the path and inserts it:

```go
`INSERT INTO agent_egress_templates (agent_id, template_id) VALUES ($1,$2)`
```

The agent is settled by `agentScoped`, the template is not — and the allowlist
compile (`store.go:376`) joins `agent_egress_templates → egress_template_hosts`
without an organisation either. Attaching a foreign template therefore discloses
which hosts another tenant permits and widens one's own agent's egress from a
foreign list. The sibling function one screen up (`store.go:234`) already carries
the correct guard: `WHERE EXISTS (SELECT 1 FROM egress_templates WHERE id=$1 AND
org_id=$4)`.

**Fix.** The same `EXISTS` here, and the organisation in the compile join.

### F — every platform_admin administers every tenant

> **Behoben** (P2): die Mandanten-Routen liegen unter `/api/v1/platform/orgs`
> und hängen an `accounts.platform_role = system_admin`
> (`internal/httpapi/server.go`, `platformAdmin`). Vergeben wird die Ebene nur
> über `covey system-admin add <mail>` — ein Endpunkt dafür wäre aus einer
> Organisation heraus erreichbar und damit genau die Grenze, die die Rolle
> ziehen soll.

`GET/POST/PATCH/DELETE /api/v1/orgs` are guarded by `adminOnly`, i.e. by an
**org** role that every organisation hands out itself (`server.go:403-406`).
Correct while an instance belongs to one company, untenable the moment it does
not. The change is the `system_admin` level described in
[`002-plattform-registrierung.md`](002-plattform-registrierung.md) and is
recorded here only for completeness.

### G — all sandboxes share one network

Both isolation modes put every sandbox of every organisation on one shared
network (`internal/orchestrator/sandbox_docker.go:129-158`):

- `EgressIsolation: "network"` — every sandbox joins the single flat
  `covey-egress-internal`,
- default — every sandbox joins the Docker bridge, plus
  `--add-host host.docker.internal:host-gateway`.

Sandboxes reach each other directly on that network, which is lateral traffic
the egress proxy never sees: the allowlist governs the way *out*, not the way
*sideways*. For one company that is a non-issue — the agents are colleagues. For
two tenants it is the boundary that matters most, because behind it sits the
other tenant's daemon and home.

**Fix.** A network per organisation (`covey-egress-<org>`), one proxy container
per network or a proxy that is reachable from each of them, and no shared bridge
in the default mode. This is the expensive one, and it is the same decision as
the capacity question in FR-002 — runners, a hardened runtime, or no sandboxes
for unapproved tenants.

## The structural change

Fixing seven places is a day's work. Keeping the eighth from growing back is the
actual request:

1. **Extend the scoped middleware family** to the remaining id-addressed objects
   — dream actions, skills, templates, guardrails, egress templates. Same shape
   as `idScoped` (`server.go:613`), same answer (*not found*, never *forbidden*).
2. **Make the org id non-optional in the store layer.** Every store method that
   takes an object id takes an `orgID` next to it, even where the caller has
   already checked. `Dreams.Undo` was reachable precisely because its signature
   allowed it to be. A small test that walks the store packages by reflection
   and fails on an exported method with a `uuid.UUID` id but no `orgID` would
   make the convention enforceable rather than aspirational.
3. **One integration test with two organisations.** Create org A and org B, then
   walk the entire route table with B's session against A's object ids and
   assert 404 on every one. The existing suite (`internal/integration/`) has the
   fixtures for it. This is what turns the cleanup into a boundary — every new
   route is tested against it the day it is added, without anybody remembering
   to.

Point 3 is the one worth doing first: it *finds* the next A–E rather than
waiting for a reader to.

## Build order

- **H1 — the cheap five.** A, C, D, E in one pass, each with a test that fails
  before and passes after. Acceptance: org B can neither read A's events nor
  write A's dream actions, skills or egress assignments.
- **H2 — the two-org test.** The route-table walk from point 3, red where H1 has
  not reached yet. Acceptance: it runs in `make test-integration` and covers
  every route with a path parameter.
- **H3 — webhook addressing.** B: token/id addressing, the UI hands out the new
  form, the runbooks follow, slug resolution refuses when ambiguous.
  Acceptance: two organisations with an agent named `support` each receive their
  own webhooks.
- **H4 — the store convention.** Point 2, including the reflection test.
- **H5 — network per organisation.** G, together with the capacity decision from
  FR-002. Acceptance: a sandbox of org A cannot reach a sandbox of org B, proven
  by a test that tries.

H1–H4 are independent of FR-002 and worth doing whether or not
self-registration is built. H5 is the one that gates opening the instance to
strangers.

## Non-goals

- **A rewrite of the RBAC model.** The five org roles stay as they are; this is
  about the organisation boundary, not about who may do what inside it.
- **Row-level security in Postgres.** It would be the thorough answer and it
  would touch every query in the code base; the middleware plus the store
  convention gets the same guarantee at a fraction of the disruption. Worth
  revisiting if the tenant count ever justifies it.
- **Encrypting one tenant's data against another.** Separation here means
  access, not cryptographic isolation — the secrets are already sealed per
  organisation (`secrets`, AES-GCM with `org_id` in the AAD).

## Open decisions

- **D1 — what happens to existing webhook URLs?** Every target system out there
  is configured with the slug form today. Either the slug keeps working while it
  is unambiguous (silent, and correct until a second tenant picks the same
  name), or it is switched off with a deprecation window. The proposal prefers
  the former plus a warning in the UI, because the alternative breaks working
  installations on upgrade.
- **D2 — does the event bus filter, or does the handler?** Filtering in
  `Publish` keeps foreign events out of the process memory of a subscription;
  filtering in `handleSSE` is a one-line change. The proposal filters at
  subscription, because a fan-out that has already copied the event has already
  lost the argument.
- **D3 — is `agents.slug` made unique instance-wide instead?** It would fix B
  with one migration, and it would be wrong: a slug is a name inside an
  organisation, and two companies may both employ a `support`. Recorded because
  it is the obvious shortcut and should be refused deliberately.

## Effect on the specification

- [`09-enterprise-model.md`](../spec/09-enterprise-model.md) — the tenant model
  gains what isolation actually means and where it is enforced.
- [`01-architecture.md`](../spec/01-architecture.md) — the event bus is part of
  the control plane's contract; that it is org-scoped belongs there.
- [`06-observability-control.md`](../spec/06-observability-control.md) — the
  egress allowlist governs the way out, not the way sideways; the network
  boundary belongs next to it.
- [`13-zammad-integration.md`](../spec/13-zammad-integration.md) and the other
  target-system documents — the webhook address form changes.
