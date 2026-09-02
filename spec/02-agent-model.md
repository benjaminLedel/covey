# 02 — Agent model

An agent is the platform's central entity: a configured, persistent "person" with four building blocks — identity, workplace, credentials and personality. All four are created and configured in one place.

An agent is an **organisation-owned resource**, not a user's personal assistant: it belongs to the company, is assigned to a department and is accounted for by an **agent owner** (usually a team lead), while remaining subject to central governance. How the human roles interact is described in [`09-enterprise-model.md`](09-enterprise-model.md).

## Identity

Every agent has:

- a **unique ID** in the registry,
- a **place in the org chart** with reporting and, where applicable, peer relationships,
- a **machine identity** at the identity provider (Keycloak), through which all access runs,
- **optionally an email address of its own** (e.g. `support-agent@…`).

**The email is optional, not mandatory.** An agent's binding identity is its machine identity in Keycloak — access and attribution run through that. An email address is an *additional channel* you give an agent when its role needs one. Where it exists it is doubly useful: as a bus for inter-agent communication and human↔agent escalation (see below) and as a natural wake trigger (new mail → agent wakes up). A pure automation agent that only reacts to ticket events does not need one.

### Identity model: real user vs. service account

Two options, with consequences for the whole system:

| | (a) Real user per agent | (b) Service account with delegation |
|---|---|---|
| **Permissions/audit** | the existing systems (Confluence, Teams) handle it directly | have to be rebuilt platform-side |
| **Org chart** | becomes real (the agent is a real user) | stays platform-internal |
| **Cost** | one licence per agent (M365 etc.) | no additional licences |
| **Provisioning** | overhead per agent | leaner |

Recommended default: **(a) for systems where real identity + native audit matter** (mail, Teams, Confluence), **(b) for purely technical access**. Whichever model — access never runs through long-lived secrets baked into the sandbox, but through the broker (see [`04-identity-secrets.md`](04-identity-secrets.md)).

### Before the first day: the draft

An agent carries the moment it was **hired**. One that has none yet is a **draft**: it has a name, a config and a place in the org chart, but it is not dispatched, its heartbeat does not fire, its webhook is not live, it gets no sandbox and it costs nothing. Tasks may be queued against it and wait for the first day.

The kill switch would have been technically sufficient for this — a killed agent does not run either — but it would confuse two different facts in one field: *this one was stopped* and *this one has not started yet*. And the hiring date is not a flag but a fact with a time, which is why it belongs in the employee profile next to a human's ([`09-enterprise-model.md`](09-enterprise-model.md)).

The state pays for itself wherever an agent comes into existence without a person having finished thinking about it: instantiating from a template, importing a bundle (see below), leaving the creation form half-filled — and above all where an *agent* drafts the agent ([`20-hiring-and-setup.md`](20-hiring-and-setup.md)). Hiring stays a human act in every one of those cases, and so does the other way out: a draft that does not fit is **discarded**, and it leaves nothing behind, because nothing ran.

## Workplace (sandbox)

The "employee's PC": an isolated sandbox with a **persistent home directory** that survives across sessions. Files, accumulated artefacts and local notes are preserved. Compute is ephemeral, the volume persistent (see [`01-architecture.md`](01-architecture.md)).

The persistent home covers files — **not** episodic memory across tasks. There is a separate memory layer for that (see [`05-memory.md`](05-memory.md)).

### The home in the web interface

The home is open in the web interface as a **file browser** (the *Files* tab on the agent): look through it, open things, edit text files, upload and download, create folders, rename, delete. That makes "what is actually lying around at this agent?" a question for the UI rather than for a shell on the host — and the way to hand an agent material (a template, a price list, a data set) no longer leads through a target system as a detour.

**Whole folders, in both directions.** In: several files at once, via dialog or drag & drop — including a complete folder whose structure is preserved (the browser does not deliver a file tree here but entries the UI walks itself). Out: selected entries and whole folders as a **ZIP**, streamed rather than staged. The size is measured beforehand — "too large" has to be an error, not an archive that breaks off mid-download without showing it.

**Look instead of download.** The browser displays the usual files in place: Markdown rendered, images (including SVG) as images, PDF embedded, CSV/TSV as a table, everything else in the editor. Where there is a source text, it is one click away and stays editable — the preview comes *before* the editor, it does not replace it. The kind of a file is determined by the control plane in one place (`sandboxfs.PreviewKind`) so that display and delivery cannot drift apart.

**How full it is, and what is filling it.** The tab shows the file system's occupancy and, next to it, the project checkouts under `repos/` — the one directory that grows without bound, because the home is persistent and every checkout stays. That measurement was missing, and the consequence was only visible to the agent itself: one wrote into its own wiki that its 40 GB overlay was running full "through old checkouts", and shortly after a run died of it. Since then the checkouts are also **bounded**: after every checkout the least recently used working copies fall away (measured by last use, not by age; the count is configurable). Which ones went is part of the checkout's answer — the agent may be holding a path from an earlier run, and "no such file or directory" is a poor way to learn about it.

Four decisions carry this:

- **Past the daemon, straight onto the home.** Access runs through the sandbox provider's `FileAccess` port onto the home directory, not through the daemon protocol. Otherwise the workplace would only exist while the sandbox is running — and normally it is not (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)). A provider without a reachable home has no feature, rather than a guessed one.
- **No way out of the home.** Every path is normalised and checked against the deepest existing ancestor; a symlink pointing outward is displayed but not opened. A control plane's file browser that can be widened into the host's file browser would be the platform's most expensive convenience.
- **Inline only by allowlist.** Displaying a file from an agent home in the browser means rendering foreign bytes on the covey origin. Only a short list of types (images, PDF) therefore goes out *inline* — with `nosniff` and a CSP without any privilege; everything else is delivered exclusively as an attachment. Uploaded HTML is thereby a file you download, not a page that runs on the platform.
- **Every change into the recording.** Writing access lands as an event (`kind: file`) in the same trace as the agent's actions, together with the human who acted. Whoever swaps out a file in the home overnight changes the agent's behaviour; for someone reading the run later, that is the same kind of event as a tool call.

**Roles:** the agent's administrators and security may read — whoever investigates an agent has to see what sits with it. Writing stays with the administrators: a file in the home is agent configuration, not an audit operation.

The wiki working copy under `~/wiki/` is visible but is not a place to edit: the source of truth is the control plane, and the next run materialises it anew (see [`05-memory.md`](05-memory.md)). The UI says so in place.

> **Soft vs. hard limits.** The `## Limits` in `SOUL.md` are **self-binding** — they steer the agent's behaviour via the prompt. They are valuable, but they are **not** the security boundary: a prompt can be worked around or defeated by injection. The *hard* limits come from the central, platform-enforced **guard rails** (see [`06-observability-control.md`](06-observability-control.md)), which take effect outside the runtime. Both layers together = defence in depth.

## Credentials

An agent is given targeted access to the systems its role needs. Support agent, for example: ticket system, Confluence, Teams. This access is **not** deposited as credentials in the sandbox but configured as **permissions** in the platform. At runtime the agent requests a token through the daemon, the broker checks the permission and issues a short-lived, scoped token.

> **Security note:** a support agent with access to tickets + Confluence + Teams that gets prompt-injected through a prepared ticket is a genuine security incident (data exfiltration through *legitimate* access). The threat model for this is in [`04-identity-secrets.md`](04-identity-secrets.md) and has to be thought through from the start.

Separate from these are the credentials the agent needs in order to **think** rather than to act: the LLM credential of its runtime. It is brokered the same way and never sits permanently in the sandbox, but it is a different kind of thing — target-system access is an *identity* that appears in the audit trail, an LLM credential is *capacity*. Which runtime an agent works on is accordingly not a technical detail but a durable, assigned property, on a par with its department and its budget: it says which contract pays for its thinking. See [`18-runtimes-capacity.md`](18-runtimes-capacity.md).

## Personality: config as code

An agent's behaviour is defined as a set of Markdown files in Git — **one source of truth per agent** (one repo or directory per agent). The platform compiles a system prompt + runtime config from it. Benefits: versioning, rollback, review. Changes to agent behaviour go through a PR, not through a deploy — GitOps for agents, with audit falling out for free.

### Config files

| File | Purpose |
|---|---|
| `SOUL.md` | Core personality: role, mission, tone, values, limits. The agent's character. |
| `CAPABILITIES.md` | What the agent may/should do: task types, responsibilities, non-responsibilities. |
| `PLAYBOOKS.md` | Concrete procedures for recurring tasks (step-by-step instructions). |
| `ACCESS.md` | **References** to required access (system names + scopes) and the tool allowlist per system (`tools:` attribute) — never secrets themselves. |
| `ORG.md` | Position in the org chart, manager, who to escalate to. |
| `EGRESS.md` | The agent's egress configuration: assigned templates + its own hosts. |
| `HEARTBEAT.md` | Recurring tasks on a schedule (interval or fixed time of day) — the control plane puts them into the backlog automatically, see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md). |

The exact file list is deliberately extensible — further sensible MD files (e.g. `TONE.md`, `ESCALATION.md`) may be added. Core rule: **`ACCESS.md` contains references, not secrets.** Alongside these files, which every run carries in full, there are **skills** for procedures that load only on demand (see below).

`ACCESS.md` and `EGRESS.md` are the **text view onto state that is also maintained through the UI** (*Tools & skills* and *Settings → Egress* respectively). So that text and UI config can never diverge, each file exists exactly once and both directions write the same store: reading renders the file live from the database, saving parses and applies it (write-through). Text edits to tools/egress are subject to the same RBAC as the UI (only `org_admin`/`security`); neither file is compiled into the system prompt.

What an agent can **do** in a connected target system is shown by the same tab under *Target systems*: the action list in the **wording of its system prompt** (the plugin's `PromptDoc`, filtered to the assigned tools for MCP), plus the access from `ACCESS.md` and the org-wide activation. A smoothed second version would be a second truth that eventually deviates from the first — the question "why doesn't the agent close the ticket?" is answered only by the text it actually reads.

`HEARTBEAT.md` too is platform config, not prompt material: it is parsed and materialised on save (table `agent_heartbeats`), and the task itself reaches the agent as a regular backlog task. Format and schedule semantics are in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).

### Skills: capabilities that load only on demand

All the prompt files listed above sit in the system prompt of **every** run, in full. For identity and limits that is right — they always apply. For procedures it is waste: an agent with five playbooks pays for all five even when the run establishes after three turns that there is nothing to do.

A **skill** inverts this. It is a directory with a `SKILL.md` and arbitrary trimmings (reference tables, templates, checklists). Only its **description** sits permanently in context — one sentence saying *when* to pull the skill; the body and any extra files are read by the runtime only when it pulls it. The daemon materialises an agent's skills before every run into `<home>/.claude/skills/<name>/`; because the run starts with `HOME=<agent home>`, these are the **personal skills of exactly this agent**, without anything having to change in the prompt.

Two levels, modelled on the secrets model ([`04-identity-secrets.md`](04-identity-secrets.md)):

- **Agent-owned skills** belong to exactly one agent.
- **Skills in the org library** are available to everyone but reach an agent **only after being explicitly linked**. This opt-in rule is not just least privilege here but also a cost question: every available description sits permanently in the context of every run, and a research agent does not need the deployment checklist.

On a name collision the agent-owned skill wins — otherwise a change to the library would overwrite a deliberate local deviation, and on disk two skills could not occupy the same directory. The name is at once the directory name and the `/slash-command` and is therefore immutable; renaming would silently point references from playbooks and other skills into the void.

Skills are **centrally managed config**, not memory: there is no way back out of the sandbox. A run that could write itself new capabilities would defeat the control that justifies the feature in the first place. Conversely, the daemon clears away revoked skills on the next run — the home survives the run, so a deleted or unlinked skill would otherwise stay effective forever.

Rule of thumb for the cut: `PLAYBOOKS.md` keeps the standard procedure that nearly every run needs. What rarely applies but is then extensive moves into a skill.

### What belongs to the config — and what to the binary

A run's system prompt has two origins, and the separation decides how changes reach a production population:

- **Agent part** (`SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`, `ORG.md` …) — that is config. It is versioned, changed via PR/UI, and can be rolled back. A change here affects exactly one agent and is a person's deliberate decision.
- **Platform part** (completion protocol, the `covey/` meta actions, stage rules — `agents.ProtocolInstructions`) — that is **code**. It describes the contract between agent and platform and changes with the binary, not with an agent config.

The prompt is therefore assembled **at dispatch time** from the config files plus the current platform part, not from the version frozen at save time. Otherwise an agent nobody has touched since the last deploy would never learn about newly added actions — every platform update would need a manual sweep across the whole population, and whoever forgets it operates agents under a contract the platform no longer honours.

The same principle already carries the target-system documentation (which mirrors the organisation's currently enabled plugins) and the team directory (which mirrors the current org chart). The stored `compiled_prompt` is retained as a **snapshot** for audit and display.

Practical consequence for operations: **a deploy rolls out platform changes by itself.** The only thing to bring along afterwards is what is genuinely config — for instance when a playbook should use a new action.

**Which configs need catching up is a question the platform answers.** A config lint checks the population against patterns that in practice have produced endless loops, burnt budgets and unusable boards: heartbeat intervals too tight for the systems in use, runs piling up at the turn limit, self-created board columns naming an item instead of a state, work that leaves no visible trace and therefore counts as open again at the next interval. They are **warnings with context**, not prohibitions — a two-minute interval is fine for a mailbox and ruinous for a repo clone, and whoever knows better must be allowed to save.

The rules are mechanical, and that is their strength and their ceiling: they see the pattern and not the case. The reader that judges the case is covey Doctor, which takes these findings as one input among several ([`21-operations-and-improvement.md`](21-operations-and-improvement.md)).

The findings sit in two places, deliberately: on the agent's own page, because that is where somebody looks who is looking after an agent because something is wrong with it — and in `covey config lint` across all organisations, which is how an instance is checked after an upgrade without a browser (exit code `1` on findings). A check that exists only as a subcommand effectively does not exist: the rule about frequent turn-limit aborts would have described one QA agent's state from day one — 22 of 23 failures at the limit, several hundred dollars spent without a single merge request tested through — and nobody ran it, because nobody runs a subcommand on a hunch.

### Export & import

An agent's complete configuration is portable as a **JSON bundle** (`GET /api/v1/agents/{id}/export`, `POST /api/v1/agents/import`; in the UI an *Export* button on the agent and *Import* on the agent overview). The bundle (`kind: covey.agent-config`, versioned) covers master data (runtime, model, turn limit, budget, manager by email), all config files including the live-rendered `ACCESS.md`/`EGRESS.md`, board columns, agent-scoped guard rails, the assigned egress templates **including their definitions** (the import creates missing ones), the agent's **skills** with full content and a provenance marker (`origin: agent|library`), and the **names** of assigned secrets.

Skills travel in full deliberately: unlike secrets there is nothing confidential about them, and a bundle that named only names would produce an agent without its procedures on import — which would not show up at import time but only during a run. On import the same separation of the two levels applies: agent-owned skills are created, while for library skills it links an **already existing version of the same name** instead of overwriting it (it may belong to other agents) and reports that as a warning.

Limits are preserved in the process: **secret values and webhook tokens never leave the platform** — the import reassigns existing org secrets by name, reports missing ones as a warning and generates a fresh token when the webhook is enabled. The import always creates a **new** agent (slug collision → `409`, `?slug=` overrides) and is subject to the same RBAC as the individual endpoints: bundles with guard rails, egress or tool allowlists are imported only by `org_admin`/`security` (fail-closed).

Besides creating anew there is **overwriting an existing agent from a bundle**, which takes over **the config files and the skills** (`POST /api/v1/agents/{id}/config/import`; in the UI the *Import bundle (config only)* button on the config tab). The skills belong to that, now that procedures are migrating there out of `PLAYBOOKS.md` — a pure file takeover would otherwise be half an import; it acts additively, and existing skills the bundle does not know about stay in place. Everything else in the bundle — master data, board columns, guard rails, egress templates, secret assignments — is ignored; the target agent keeps its identity, secrets and assignments. The storage and write-through path is identical to `PUT /config` (a new config version, the same security-role boundary for tool allowlists/egress). This is how you distribute a shared base config across several existing agents without creating them anew.

### Example: `SOUL.md` (sketch)

```markdown
# Support agent

## Role
First-level support for customer enquiries in the ticket system.

## Mission
Triage tickets, answer solvable cases yourself,
escalate complex cases to the responsible human.

## Tone
Friendly, brief, solution-oriented. German, formal address.

## Limits
- No commitments on prices or contracts.
- Always escalate legal questions.
- No actions that delete customer data without approval.
```

### Example: `ACCESS.md` (sketch)

```markdown
# Access

- system: ticketing      scope: read,write,comment   tools: all
- system: confluence     scope: read                  tools: search, get_page
- system: teams          scope: send-message
```

`tools:` is the agent's tool allowlist for that system: if the attribute is missing or says
`all`, every tool is allowed; a list fail-closed enables only the ones named
(enforcement centrally in the control plane, see [`01-architecture.md`](01-architecture.md)).

## Org chart & inter-agent communication

The org chart is more than decoration: it structures escalation and delegation. **For agents with an email address**, email as a message bus is surprisingly elegant — async, auditable, human-readable. A support agent can escalate to a "senior" agent or a real colleague without any special protocol. Where there is no email (or for more structured delegation), communication runs through a platform-internal messaging layer or **A2A/MCP**.

Where email is used twice over (bus + wake trigger), that makes event correlation a core topic — but it is designed to be channel-independent anyway (ticket updates and webhooks too). See [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).
