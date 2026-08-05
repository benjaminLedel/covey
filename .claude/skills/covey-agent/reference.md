# Reference: bundle schema & target systems

## Bundle schema (`covey.agent-config`, version 1)

An agent bundle is a JSON file. Mandatory are `kind`, `version`, `agent.slug`,
`agent.display_name` and `files`. Everything else is optional.

```json
{
  "kind": "covey.agent-config",
  "version": 1,
  "agent": {
    "slug": "covey-support",              // unique per org, [a-z0-9-]
    "display_name": "Covey Support",
    "runtime": "claude-code",             // currently the only runtime
    "model": "",                          // optional, empty = the runtime default
    "max_turns": 0,                       // optional, 0 = default (30)
    "budget_usd": 0,                      // optional, 0 = no cap (kill switch on exceeding it)
    "supervisor_email": "",               // optional, assigns the manager
    "webhook_enabled": false,             // optional, generates a fresh token on import
    "warm_sandbox": false                 // optional, keeps the sandbox live between runs (dev/test only)
  },
  "files": {
    "SOUL.md": "...",                     // role, mission, tone, limits
    "CAPABILITIES.md": "...",             // what it can do / is not responsible for
    "PLAYBOOKS.md": "...",                // step by step per assignment
    "ACCESS.md": "...",                   // access (system + scope)
    "HEARTBEAT.md": "..."                 // triggers (interval, nur-wenn, aufgabe)
  },
  "stages": [ { "name": "In progress", "color": "#..." } ],       // optional: backlog columns
  "guardrails": [ ... ],                                          // optional: policy rules
  "egress_templates": [ { "name": "...", "hosts": [ ... ] } ],    // optional: permitted hosts
  "skills": [ { "name": "...", "description": "...",              // optional: capabilities
                "origin": "agent",
                "files": { "SKILL.md": "...", "reference.md": "..." } } ],
  "secrets": { "org_keys": [], "agent_keys": [] }                // optional: ONLY NAMES, never values
}
```

**For a config update `files` and `skills` suffice** (`POST /agents/{id}/config/import`);
the remaining fields only take effect when creating anew (`POST /agents/import`).

### Skills — procedures that not every run pays for

Everything in `files` sits in the system prompt on **every** run. For identity and limits that
is right, for procedures it is not: an agent with five playbooks pays for all five even when the
run establishes after three turns that there is nothing to do. A skill inverts that — only its
`description` sits in context permanently; `SKILL.md` and the extra files are read by the runtime
only when it pulls the skill.

- `name` — `[a-z0-9-]`, max. 63 characters. Becomes the directory name in the agent home and
  therefore the `/slash-command`. **Not renameable** (references would point into the void); to
  change it, create a new one and delete the old.
- `description` — the only text that costs context permanently. One sentence saying **when**
  to pull the skill ("Use this when: …"), max. 500 characters.
- `origin` — `"agent"`: belongs to this agent only. `"library"`: sits in the org library and is
  linked to it; on import an already existing version of the same name there is **linked instead
  of overwritten** (it may belong to other agents) — the import reports that as a warning. If the
  field is missing, `"agent"` applies.
- `files` — `SKILL.md` is mandatory (without it the runtime does not recognise the directory as a
  skill). Max. 32 files of 256 KB each, relative paths without `..`. The `SKILL.md` frontmatter is
  cut off on import and may fill `name`/`description`; what is stored is the body, so that the
  description does not sit in two places.

**Rule of thumb:** `PLAYBOOKS.md` keeps what the agent needs in nearly every run (the standard
procedure). What rarely applies but is then extensive moves into a skill —
special cases, checklists, templates, reference tables.

### The five config files

- **SOUL.md** — identity and mission. The templates' structure: `## Role`, `## Assignment`,
  `## Tone`, `## Limits`. This is where the behavioural rules belong (done-not-blocked,
  idempotency, a visible trace per run, breaking up instead of the turn limit, hand-off).
- **CAPABILITIES.md** — a terse capability list + `## Not responsible for`.
- **PLAYBOOKS.md** — numbered procedures per assignment; the concrete action calls.
- **ACCESS.md** — access, one line per system: `- system: <name> scope: <a>,<b>`.
- **HEARTBEAT.md** — triggers, one line per trigger:
  `- alle: <interval> nur-wenn: <system>:<kind> titel: <short> aufgabe: <concrete>`.
  The keys are German because a parser reads them — they are the data format, not text.

## Target-system catalogue

Registered built-in systems (as of this repo): `gitlab`, `email`, `dev`, `browser`, `teams`,
`zammad`, `sharepoint`, `mcp`. **Authoritative** are the `SetupDoc()`/`PromptDoc()` in the
respective `internal/target/<name>/plugin.go` — the exact scopes and actions are there.
Frequently used:

| System | Scopes (ACCESS) | What for / core actions |
|---|---|---|
| `gitlab` | `read,write,comment` | Issues/MRs: list_issues (including `milestone`), get_issue, checkout, read_file, create_issue, comment, list_notes, assign, set_labels, set_state, commit, create_merge_request, comment_mr, approve_mr, upload, download_upload |
| `email` | `read,write` | IMAP/SMTP: list_unread, get_message, reply, mark_seen, get_attachment (the attachment into the sandbox, then the read tool/vision) |
| `dev` | `exec,processes` | The sandbox shell: exec, start/stop/logs/list (bring a dev server up) |
| `browser` | `navigate,content,screenshot,click,type` | Headless Chrome; CSS + `:has-text("…")`; screenshot with `highlight`+`label` |
| `teams` | see SetupDoc | Microsoft Teams |
| `zammad` | see SetupDoc | Zammad tickets (the first built-in, spec/13) |
| `sharepoint` | see SetupDoc | SharePoint / Teams files |
| `mcp` | see SetupDoc | The generic MCP adapter |

The secrets a system needs (e.g. `gitlab_token` + `gitlab_url`, IMAP/SMTP access) are in its
`SetupDoc()` — name them to the user for assignment after the import; values never travel in the
bundle.

## Platform actions (`covey/…`)

Besides the target systems, the action proxy knows the pseudo-system `covey` — actions on the
control plane itself. They need **no `ACCESS.md` entry** (no credential, no egress) and are open
to every agent; the compiled system prompt explains them to it.
It is still worth anchoring them in `PLAYBOOKS.md` at the right places:

| Action | Params | What for |
|---|---|---|
| `set_stage` | `{"stage":"<name>"}` | The kanban column of the running task (purely presentational) |
| `add_note` | `{"content":"<text>"}` | An interim state on the task |
| `remember` | `{"content":"<fact>"}` | A single fact into memory |
| `wiki_search/read/write/delete` | see the prompt | The linked long-term memory (spec/05) |
| `org_chart` | `{}` | Look responsibilities/escalation paths up at runtime |
| `create_task` | `{"title":…,"body":…,"agent":"<slug>","priority":1..9}` | A subtask (without `agent`) or a delegation to a colleague |

Two of them need care when designing:

- **`create_task`** is the way out of assignments that are too big: close the partial result off,
  file the rest as a task — instead of carrying on to the turn limit. As the only `covey` action
  it runs through the guard rails (`covey:create_task`, on delegation
  `covey:create_task:foreign`), so it can return `denied`/`pending`. The platform refuses
  duplicates of the same title, chains that are too deep and too many tasks per run — the
  playbook should not run into that in the first place.
- **`set_stage`** creates missing columns automatically. Prescribe a **fixed, small** set of
  names in the playbook that name working *states* (`Triage`, `Analysis`, `Waiting for
  review`) — never the item (`#83 CSV import`), never synonyms for the same state. Otherwise the
  board grows a dozen dead columns within days.

## API endpoints

| Purpose | Call |
|---|---|
| Create anew | `POST /api/v1/agents/import` (body = the bundle; `?slug=` overrides on a collision) |
| Update the config | `POST /api/v1/agents/{id}/config/import` (body = the bundle; only `files` take effect) |
| Export/share | `GET /api/v1/agents/{id}/export` (a bundle download, without secret values) |
| Diagnostics (runtime) | `GET /api/v1/agents/{id}/diagnostics` (the full state including the recording) |

All are RBAC-protected (manage role; export/diagnostics also security). Auth by an admin session
or a bearer token. Always ask the user for the base URL and auth, never hardcode them.
