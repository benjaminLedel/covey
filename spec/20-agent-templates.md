# Agent templates, setup wizard, functional test

> How a template declares what an agent *needs*, how a wizard collects it, and how the platform
> proves the result actually works before the agent is handed over.

Related: [`02-agent-model.md`](02-agent-model.md) (config as code), [`04-identity-secrets.md`](04-identity-secrets.md)
(secrets broker), [`06-observability-control.md`](06-observability-control.md) (guard rails, enforcement
points), [`13-zammad-integration.md`](13-zammad-integration.md) (a target system's setup surface).

## 1. What exists today

Templates are already first-class, and this document extends them rather than replacing them:

| Piece | Where | What it does |
|---|---|---|
| Template store | `internal/templates/templates.go` | Bundled templates (embedded from `examples/*.bundle.json`, read-only, shared across orgs) plus per-org templates in `agent_templates` |
| Bundle format | `internal/httpapi/export.go` — `agentBundle`, `covey.agent-config` v1 | `agent`, `files`, `stages`, `guardrails`, `egress_templates`, `skills`, `secrets` (names only) |
| Instantiate | `POST /api/v1/templates/{id}/instantiate` | Body is `{slug, display_name}`; overrides those two fields and hands the bundle to the import path |
| Import | `POST /api/v1/agents/import` | Creates the agent, attaches egress templates and skills, re-assigns org secrets *by name*, returns `warnings[]` |
| UI | `web/src/pages/Templates.tsx` | Catalogue, bundle preview, a one-step instantiate dialog, warnings afterwards |

## 2. The problem

Everything a template needs beyond its Markdown files is **prose, or nothing at all**.

A `gitlab` line in `ACCESS.md` implies a `gitlab_token` and a `gitlab_url` secret, an egress allowlist
that reaches `gitlab.com`, and an enabled `gitlab` plugin. None of that is stated in machine-readable
form anywhere. The knowledge lives in `Descriptor.SetupDoc` prose, in the target-system catalogue of
the `covey-agent` skill, and in the operator's head. The consequences are structural, not cosmetic:

- **The gap is only discovered at runtime, by the agent.** The import path reports missing secrets as
  strings *after* the agent already exists (`export.go:709`). The agent's first wake then fails on a
  broker denial, and the failure surfaces as an agent-level error rather than as an incomplete setup.
- **Silent misconfiguration is indistinguishable from a broken agent.** An emptied `ACCESS.md` produces
  `credential for <system> denied: no access per ACCESS.md` on *every* action. From the agent's side
  that reads like a guard-rail decision, so a well-behaved agent escalates it as a policy question —
  correctly, and uselessly, because the cause was a config regression.
- **Requirements that are not credentials are invisible.** An egress template that was never assigned,
  a sandbox image without the toolchain the template's playbooks call, a `dev` scope missing `docker`,
  a host-side component (the `ios` bridge) that is not running — each fails differently and none of
  them is declared.
- **A template cannot be parameterised.** Every bundle is static text. Anything engagement-specific
  (a milestone, a project path, a staging login) is either hardcoded into a copy of the template or
  left to a human to edit afterwards.
- **Nothing is ever proven.** There is no point at which the platform states "this agent's setup is
  complete and functional". `DockerProvider.Check` answers that for the *platform*
  (`sandbox_docker.go:237`), and that is the only check of its kind.

## 3. Scope

**In scope.** A machine-readable requirement model; deriving requirements from what the config already
says; a template-level declaration block for what cannot be derived; typed template inputs with
instantiate-time substitution; a wizard that resolves the requirements; a repeatable functional test
with a stored result.

**Out of scope.** Multi-agent templates (a bundle is one agent; a whole org chart is the natural
successor — see D5). Replacing `SetupDoc` prose (it stays; its *facts* become data). Changing the
secrets broker, the guard-rail engine or the egress enforcement path — this document only reads them.

## 4. The load-bearing decision: derive, then refine

Requirements are **computed** from the config, and a template may only **refine** the result.

This falls out of what is already true: `ACCESS.md` is parsed into `SystemAccess{System, Scopes, Tools}`
(`internal/agents/compile.go:377`), and each target plugin already declares whether it needs credentials
at all. The base requirement set of every existing bundle is therefore derivable without touching a
single one of them.

The alternative — every template author writes a `requires:` block by hand — was rejected for one
reason: a hand-written block is a second source of truth for something the config already says, and it
rots. A template whose `ACCESS.md` gains a `merge` scope but whose `requires:` block still names a
read-only token is worse than no declaration, because it reads as authoritative.

Consequently: **a declaration may add a requirement or narrow one, never remove a derived one** (D1).
What a declaration is *for* is everything that genuinely cannot be derived — custom `{{secret:…}}` keys
used inside action parameters, help text, validation patterns, defaults, and the functional test's
smoke task.

## 5. Data model

### 5.1 Per-plugin requirements (`internal/target`)

`Descriptor` gains one field. This makes plugins self-describing in the same way the registry is
self-registering — no central list to maintain, and the requirement travels with the plugin that
knows it.

```go
// Requires is what an agent needs before this system can be used. Machine-readable
// counterpart to SetupDoc: the prose stays for a human to read, the facts move here so
// the setup plan and the functional test can act on them.
type Requires struct {
    Secrets []SecretReq `json:"secrets,omitempty"`
    // Egress names builtin egress template slugs (internal/egress/builtin.go).
    Egress  []string    `json:"egress,omitempty"`
    // Sandbox names capability keys the sandbox image or its wiring has to provide
    // ("docker", "chromium", "android-sdk", "jdk", "warm").
    Sandbox []string    `json:"sandbox,omitempty"`
    // Host names components that run OUTSIDE any sandbox, on the control-plane host
    // ("ios-bridge"). The only requirement class the platform cannot provision itself.
    Host    []string    `json:"host,omitempty"`
    // Scopes is the catalogue of valid ACCESS.md scopes and what each unlocks. Also the
    // authority for rejecting a typo'd scope at plan time instead of at first action.
    Scopes  []ScopeDoc  `json:"scopes,omitempty"`
}

type SecretReq struct {
    Key       string   `json:"key"`                 // "gitlab_token"
    Label     string   `json:"label"`               // "GitLab Personal Access Token"
    Help      string   `json:"help,omitempty"`      // one sentence: where a human gets it
    Sensitive bool     `json:"sensitive"`           // false → a URL or a username, shown in clear
    Optional  bool     `json:"optional,omitempty"`  // CredentialsOptional systems: raises limits, not required
    Default   string   `json:"default,omitempty"`   // "https://gitlab.com"
    Pattern   string   `json:"pattern,omitempty"`   // client-side sanity check, never the authority
    // OnlyForScopes makes the requirement conditional: a token needs higher privileges
    // only if the agent actually got the scope that uses them (gitlab "merge",
    // ios "simulator").
    OnlyForScopes []string `json:"only_for_scopes,omitempty"`
}
```

The existing flags stay authoritative for the *shape* of a credential: `NoCredentials` means an empty
`Requires.Secrets`, `CredentialsOptional` means every entry is `Optional`, `BaseURLOptional` means the
`<name>_url` entry is `Optional` with a `Default`.

### 5.2 Bundle v2

Three optional blocks. `version: 1` stays valid and is read as v2-with-empty-blocks, so all twelve
bundled templates keep working unchanged; `version: 2` is only required once a v2-only block is present.

```json
{
  "kind": "covey.agent-config",
  "version": 2,
  "inputs": [
    { "key": "milestone", "label": "Milestone", "kind": "text", "required": true,
      "help": "The GitLab milestone this lead drives; appears in HEARTBEAT.md." },
    { "key": "staging_pass", "label": "Staging password", "kind": "secret",
      "secret_key": "staging_pass", "sensitive": true }
  ],
  "requires": {
    "secrets": [ { "key": "staging_user", "label": "Staging login", "sensitive": false } ],
    "sandbox": [ "docker" ],
    "egress":  [ "container" ]
  },
  "verify": {
    "task": "Report which GitLab projects you can see, then finish with done.",
    "expect": ["done"],
    "skip": []
  }
}
```

**`inputs`** are typed instantiate-time variables:

| `kind` | Collected as | Applied by |
|---|---|---|
| `text`, `number`, `bool`, `choice` | A form field (`choices` for `choice`) | Substituting `{{input:<key>}}` in every `files` entry, and in `agent.display_name` |
| `secret` | A password field | Writing the value to the `SecretStore` under `secret_key` (agent-scoped), never into a file |

`{{input:…}}` and `{{secret:…}}` are deliberately different namespaces resolved at different times:
`{{input:…}}` is substituted **once, at instantiate time**, and the resulting file is what a reviewer
reads; `{{secret:…}}` stays in the file verbatim and is resolved **per action, in the daemon**
(`internal/daemon/actionproxy.go:376`) so the value never enters the model's context. An input of kind
`secret` is the bridge between them: the wizard collects the value, the store keeps it, and the file
keeps referring to it as `{{secret:<secret_key>}}`.

Substitution is refused if any `required` input is unresolved, and any `{{input:…}}` left in a file
after substitution is a hard error — a template that ships an unresolved placeholder into an agent's
prompt is worse than one that fails to instantiate.

### 5.3 The setup plan (computed, never stored)

```go
type SetupPlan struct {
    Inputs   []Input          `json:"inputs"`
    Secrets  []PlannedSecret  `json:"secrets"`
    Plugins  []PlannedPlugin  `json:"plugins"`
    Egress   []PlannedEgress  `json:"egress"`
    Sandbox  []PlannedSandbox `json:"sandbox"`
    Host     []PlannedHost    `json:"host"`
    Blockers []string         `json:"blockers"` // instantiation cannot proceed
    Warnings []string         `json:"warnings"` // it can, with a known gap
}
```

Every planned row carries a `status` and, when it is not `satisfied`, a `remedy` — a sentence naming
the concrete next step, in the register `DockerProvider.Check` already uses ("build it once:
`docker build -f Dockerfile.sandbox …`"). A check that cries wolf gets ignored; one that states the fix
gets acted on.

| `status` | Meaning |
|---|---|
| `satisfied` | Already present in this org (secret exists, plugin enabled, capability available) |
| `collect` | The wizard has to ask a human for it |
| `provision` | The platform will do it on apply (import an egress template, enable a plugin) |
| `forbidden` | Required, but this human's role may not resolve it → hand over (§7) |
| `unavailable` | Cannot be satisfied on this instance (no host component, image lacks the toolchain) |

### 5.4 Derivation algorithm

`POST /api/v1/templates/{id}/plan` writes nothing. It:

1. Parses the bundle's `ACCESS.md` with `agents.ParseAccess` → the systems and scopes the template wants.
2. For each system: `target.Describe(name)`. Unknown → blocker. Not enabled for the org → planned
   `provision` (enabling a plugin is org config, not a credential). Unknown scope against
   `Requires.Scopes` → blocker, naming the valid ones.
3. Collects `Requires.Secrets`, dropping entries whose `OnlyForScopes` the template did not grant.
   Cross-checks each key against `Secrets.Keys(orgID)` → `satisfied` or `collect`. Without
   `canReadSecretKeys(role)` the *existence* of a key is not disclosed: the row becomes `forbidden`.
4. Scans all `files` for `{{secret:<key>}}` occurrences → the custom, agent-scoped secrets the
   playbooks actually reference. This is pure derivation and closes today's most common gap: a bundle
   whose playbook interpolates a staging password nobody knew to create.
5. Unions `Requires.Egress` per system with the bundle's own `egress_templates` and `requires.egress`,
   resolves each slug against `egress.Builtins` and against the org's existing templates.
6. Resolves `Requires.Sandbox` against the active sandbox provider's reported capabilities, and
   `Requires.Host` by probing the component's health endpoint.
7. Merges the bundle's `requires` block under the D1 rule (add or narrow only).

Because step 1 reads the *template's own* `ACCESS.md`, the same function serves a second caller:
running it against an **existing agent's current config** yields that agent's requirement state, which
is what the functional test in §8 compares against reality.

### 5.5 Persistence

One migration, `0049_agent_verification`:

```sql
CREATE TABLE agent_verifications (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    UUID        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    passed      BOOLEAN     NOT NULL,
    tiers       JSONB       NOT NULL,        -- per-tier result incl. remedy text
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    started_by  UUID        REFERENCES humans(id)
);
CREATE INDEX agent_verifications_agent_idx ON agent_verifications (agent_id, finished_at DESC);

ALTER TABLE agents ADD COLUMN verified_at TIMESTAMPTZ;
UPDATE agents SET verified_at = now();   -- existing agents are proven by the fact that they run
```

The backfill is the point: introducing a check must not retroactively declare a working fleet broken.

Template `inputs`/`requires`/`verify` need no schema change — they live in the bundle JSON that
`agent_templates.bundle` already stores.

## 6. The wizard

Five steps. Step 3 and 4 are skipped when the plan has nothing for them, which keeps a
no-dependency template (the bootstrap demo agent) at two clicks.

| Step | Shows | Writes |
|---|---|---|
| 1 · Identity | Slug, display name, supervisor, runtime; the bundle's own defaults pre-filled | — |
| 2 · Requirements | The plan as a checklist, grouped by class, each row with status and remedy | — |
| 3 · Inputs | A form built from `inputs`, defaults pre-filled | — |
| 4 · Secrets | One field per `collect` secret, with `label`/`help`/`pattern`; `forbidden` rows render as a hand-over notice instead | — |
| 5 · Review | Everything that will happen, in apply order | On confirm: apply (§7), then verify (§8) |

The wizard is a client of `plan` and `instantiate`; it holds no state the server needs. Collected
secret values are POSTed **directly** to the existing secrets endpoints from step 4, never carried
through the plan or instantiate payloads, never echoed back, never written to the request log
(`internal/reqlog` must treat these paths as body-suppressed), and never part of a verification record.
Bundle export already excludes values and stays that way.

## 7. Apply

Order matters, because agent-scoped writes need an agent ID:

1. Create the agent (`Registry.Create`) — inside a transaction with 2–5.
2. Enable the `provision` plugins for the org; import the `provision` egress templates
   (`egress.CreateFromBuiltin`) and assign them to the agent.
3. Write collected secrets: org-scoped via `Secrets.Put` + `Assign`, agent-scoped via `PutAgent`.
4. Substitute `{{input:…}}` into `files`; save as config version 1 (`Registry.SaveConfig`), which
   materialises `system_accesses` and the tool allowlist.
5. Attach stages, guardrails, skills — the existing import steps, unchanged.
6. **Outside** the transaction: run the functional test, store the result, set `verified_at` on success.

A failure in 1–5 rolls back completely: no half-built agent. A failure in 6 does **not** roll back —
the agent exists, unverified, with a report naming the failed tier. That asymmetry is deliberate: a
wrong token is a five-second fix, and discarding a correct 200-line config to punish it would be
absurd. The wizard's final screen therefore has two exits: *fix and re-verify*, or *keep and finish
later*.

`forbidden` rows never block apply. They produce a **hand-over**: a backlog task on the agent's owner
(or a notification to the security role) naming exactly which secret is missing, so the person who may
resolve it can do so without repeating the wizard. This replaces today's dead-end warning string.

## 8. The functional test

Four tiers, cheapest first, each one only reached when the previous passed. A tier reports
`pass | fail | skip` with a remedy per failure.

**T1 — Config (no I/O, milliseconds).** `ACCESS.md` and `HEARTBEAT.md` parse; every named system is
registered and enabled; every scope exists in that system's `Requires.Scopes`; every `{{secret:…}}`
referenced in a file resolves through the broker path; no `{{input:…}}` survived substitution.

**T2 — Platform.** The sandbox provider's own `Check`. Plus the check that today's incidents keep
asking for: **the sandbox image's build versus the control plane's** — `coveyd version` already answers
which build sits in the image (`cmd/coveyd/main.go:41`), and a mismatch is the difference between "the
fix is deployed" and "the fix is deployed to one of the two images". Plus each `Requires.Host`
component's health endpoint.

**T3 — Credentials and reach.** For every system in `ACCESS.md`, a cheap read-only call through the
**real broker path** — not a side channel. That is the whole value of the tier: it exercises
`ACCESS.md` → `HasAccess` → guard rails → `Secrets.Resolve` → the plugin, so an emptied `ACCESS.md`,
a revoked token and a deny rule are all caught here, by the platform, in seconds. A new optional
plugin interface, in the established style of `Webhooker` and `WorkChecker`:

```go
// Verifier is an optional plugin interface: one cheap, read-only, side-effect-free
// call that proves a brokered credential actually works. A system without it is
// reported as "not verifiable" — never as passed.
type Verifier interface {
    Verify(ctx context.Context, cred Credential) (VerifyResult, error)
}

type VerifyResult struct {
    Identity string `json:"identity"` // who the platform is to that system ("ditscheridou")
    Detail   string `json:"detail"`   // one line ("7 projects visible")
}
```

`Identity` earns its place: an agent acting under an unexpected account is a governance finding, and
this is the only place the platform learns it. The same tier set-compares the agent's
`EffectiveAllowlist` against every host the required egress templates name — a pure comparison, no
traffic (D4).

**T4 — Smoke run (opt-in).** One real agent run with `verify.task`, a hard `max_turns` of 3 and a
capped budget, asserting the run ends `done` and `verify.expect` markers appear. The only tier that
proves the runtime, the credential pool and the prompt compile together — and the only one that costs
money, so it is one explicit click, never automatic.

### Repeatability is the feature

`POST /api/v1/agents/{id}/verify` runs the same tiers against **any** agent at any time, and that
matters more than the wizard step it was built for. Every failure mode this session produced — an
`ACCESS.md` reset to empty, egress templates cleared, a sandbox image lagging the control plane, a
rotated password nobody re-assigned — is a T1–T3 finding that a human currently discovers by reading
an agent's escalation and inferring backwards. Re-verification turns that into a check, and the stored
history turns "when did this break?" into a query. `GET /api/v1/agents/{id}/verifications` serves the
history; a fleet-wide roll-up belongs next to the existing fleet view.

## 9. Gating

`verified_at` gates **scheduled** wakes (heartbeats, ticks) and never manual ones — a human debugging
an agent must always be able to wake it. Default behaviour is to **warn**: an unverified or
last-failed-verification agent still runs, and the dispatcher records an observability event.

That is a deliberate departure from principle 7 (*fail-closed*), and the reasoning is that this is a
setup check, not a security boundary. Guard rails are fail-closed because an unevaluated policy must
never permit an action. A verification result is a statement about the last time someone looked; a
stale statement must not silence a fleet that is doing its job. Orgs that want the stricter reading
get it as one setting, `strict_setup_gate` (D2).

## 10. API surface

| Endpoint | Purpose |
|---|---|
| `POST /api/v1/templates/{id}/plan` | Requirement plan for a template (read-only, no writes) |
| `POST /api/v1/templates/{id}/instantiate` | Extended body: `{slug, display_name, inputs{}, provision{}}`; v1 bodies keep working |
| `GET /api/v1/agents/{id}/plan` | The same plan computed against an existing agent's current config |
| `POST /api/v1/agents/{id}/verify` | Run the functional test; body selects tiers (`{"tiers":["config","platform","credentials"]}`) |
| `GET /api/v1/agents/{id}/verifications` | Verification history |
| `GET /api/v1/targets` | Gains `requires` per descriptor, so the UI can render requirements without a plan |

RBAC follows the existing split: `plan` needs `anyRole` (it names requirements; it discloses secret
*existence* only to `canReadSecretKeys`), `instantiate` needs `manage`, `verify` needs `manage`.

## 11. Build order

Thin vertical slice first, riskiest thing first, most expensive last — each milestone independently
useful on its own.

| M | Content | Why here |
|---|---|---|
| **M1** | `Requires` on `gitlab` + `dev` only; `POST /templates/{id}/plan`; the plan rendered read-only in today's instantiate dialog | Proves the derivation against the two systems every template uses, and already answers "what will I need?" before committing |
| **M2** | Wizard steps 3–5, apply order, `provision` of plugins/egress, hand-over for `forbidden` | Turns the plan into resolution |
| **M3** | T1–T3, `Verifier` for `gitlab`/`email`/`ios`, `POST /agents/{id}/verify`, history, re-verify button | The part that pays off outside the wizard |
| **M4** | Bundle v2 `inputs` + `{{input:…}}` substitution | Needs the wizard to exist to be worth anything |
| **M5** | T4 smoke run; `Requires` for the remaining plugins; template-authoring UI for `requires`/`inputs`/`verify` | Cost and breadth last |

## 12. Open decisions

- **D1 — Declaration vs derivation on conflict.** Recommended: a declaration may add or narrow a
  requirement, never remove a derived one. Open: whether narrowing needs an explicit reason string
  for the audit trail.
- **D2 — Gating strictness.** Recommended: warn by default, `strict_setup_gate` opt-in (§9). Open:
  whether a *failed* verification should block scheduled wakes even in warn mode, given it is a
  positive finding rather than a missing one.
- **D3 — Dynamic input pickers.** `kind: project` fed from the target system (`list_projects`) is
  obviously the better UX, but the plan needs a working credential to query — and the credential is
  what the wizard is collecting. Recommended: defer; revisit once step 4 can validate a secret live.
- **D4 — Egress: compare or probe.** Set-comparing the allowlist is free and catches the common case.
  A real probe from inside a sandbox catches DNS and proxy faults too, and costs a sandbox start.
  Recommended: compare in T3, probe as part of T4 where a sandbox is running anyway.
- **D5 — Multi-agent templates.** A bundle is one agent; a department (manager, developers, QA,
  a shared board and cross-agent hand-offs) is the shape organisations actually want to stamp out.
  It needs slug templating, intra-bundle references and a shared requirement plan. Out of scope here,
  and the natural successor.

## 13. Definition of done

1. A fresh instance, a bundled template with `gitlab` and `dev`, and no secrets: the plan names
   `gitlab_token`, `gitlab_url`, the `gitlab-com` egress template and the disabled `gitlab` plugin,
   each with a remedy.
2. The wizard collects them; a non-privileged role sees the secret rows as `forbidden` and gets a
   hand-over instead of a dead end.
3. Apply either produces a complete agent or no agent at all; a failing functional test leaves the
   agent in place with a stored report.
4. All twelve existing `version: 1` bundles instantiate unchanged and produce a correct plan without
   being edited.
5. `POST /agents/{id}/verify` on an agent whose `ACCESS.md` was emptied fails T1 and names the missing
   system line — the regression is a platform finding, not an agent escalation.
6. A sandbox image older than the control plane fails T2 with the rebuild command.
7. A template with `inputs` refuses to instantiate while a `required` input is unresolved, and no
   `{{input:…}}` ever reaches a saved config.
8. Secret values appear in the `SecretStore` and nowhere else: not in the plan, the verification
   record, the request log, the audit entry or an export.
