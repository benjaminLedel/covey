---
name: covey-agent
description: Build, design, update and share Covey agents. Use this skill when a Covey agent (developer, QA, researcher, support …) is to be designed from scratch, its config (SOUL/PLAYBOOKS/ACCESS/HEARTBEAT) changed, or exported/imported as a bundle. Produces a covey.agent-config bundle following the repo conventions and optionally creates the agent directly through the API.
---

# Building Covey agents

A Covey agent is **config as code**: its behaviour sits in a few Markdown files,
packed together as a **bundle** (`covey.agent-config`, version 1). This skill leads through
design, creation, update and sharing. Details of the bundle schema and the target-system
catalogue are in [reference.md](reference.md) — look them up there, do not guess.

**Language & tone:** the config files are written in **the language of the agent's
organisation** — English by default, in the style of the bundled templates under
`examples/*.bundle.json`; the German counterparts sit next to them as
`examples/*.de.bundle.json`. Sober and precise, technical terms kept as they are
(*merge request*, *backlog*, *heartbeat*, *done*, *blocked*). Whichever language you choose,
keep the whole bundle in it — a German `SOUL.md` under an English playbook is the worse half
of both.

## Always first: orient yourself on a template

NEVER start from zero. Pick the closest template from `examples/` as the starting point and
adapt it:

| Template | Role |
|---|---|
| `coding-agent.bundle.json` | Developer: fix GitLab issues, open MRs, review loop |
| `qa-agent.bundle.json` | Tester: accept others' MRs, check web UIs in the browser, bug intake by mail |
| `web-researcher.bundle.json` | Researcher: research on the open web with a real browser |
| `log-triage-agent.bundle.json` | Log triage: reported logs → GitLab tickets |
| `delivery-lead.bundle.json` | Delivery lead: drive a GitLab milestone to its deadline — prepare tickets, keep the order, dispatch to developer colleagues, report the state. What is engagement-specific sits in a brief in the wiki (template: `docs/ops-gitlab.md` §2.9.1), not in the config |

Read the chosen template in full before you change anything — it shows the proven structure.

## Workflow A — Designing a new agent

1. **Interview** (short, targeted): what is the agent for? Which **target systems** does it need
   (gitlab / email / dev / browser / mcp — see reference.md)? **Cadence/trigger** (heartbeat
   interval, `nur-wenn`)? **Team** (manager, a QA reviewer where applicable)? **Project scope**?
   Does it need a **warm sandbox** (holding a dev server/build between runs → test/developer
   agents)?
2. **Design the files** — the five config files following the conventions below:
   `SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`, `ACCESS.md`, `HEARTBEAT.md`. What rarely applies
   but is then extensive becomes a **skill** instead (see below and reference.md).
3. **Assemble the bundle** (schema in reference.md), validate the JSON
   (`python3 -c "import json;json.load(open('<file>'))"`).
4. **Create it** (workflow D) or put it down as a file so that the user imports it through the UI.
5. **Name the follow-up work:** the import creates the config but **no secrets** — tell the user
   which secret names the agent needs (from `ACCESS.md`, e.g. `gitlab_token`, `gitlab_url`)
   and that they have to assign them in Covey. Likewise the egress allowlist where relevant
   (browser/HTTP).

## Workflow B — Updating an existing agent

1. **Fetch the current state:** `GET /api/v1/agents/{id}/export` → the current bundle. (Or look at
   the config in the UI under Settings → Config.)
2. Change the affected file(s) — **minimally invasive**, preserving structure and tone.
3. **Play only the config back:** `POST /api/v1/agents/{id}/config/import` with the bundle —
   that overwrites SOUL/CAPABILITIES/PLAYBOOKS/ACCESS/HEARTBEAT as a **new version** (versioned,
   master data/secrets/guard rails untouched) and brings the bundle's `skills` along
   (additively — existing skills the bundle does not know about stay in place).

## Workflow C — Sharing (export/import by third parties)

- **Export:** `GET /api/v1/agents/{id}/export` delivers the bundle as a JSON download. Secret
  **values** are NEVER included (only names, and only for entitled roles). A third party can pass
  this file on / download it.
- **Import at the third party:** `POST /api/v1/agents/import` with the bundle JSON creates a new
  agent in their org. A slug collision → `?slug=<new>` attaches it under a different name.
  Afterwards the third party has to set the named secrets themselves (values never travel).

## Workflow D — Creating it live (API)

Ask for the **Covey base URL** and **auth** (an admin session cookie OR a bearer token of a
manage role) — never hardcode credentials. Then:

```bash
curl -sS -X POST "$COVEY_URL/api/v1/agents/import" \
  -H "Authorization: Bearer $COVEY_TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @agent.bundle.json
# Slug already taken? -> ...?slug=<new-slug>
```

The import is RBAC-protected (manage role). The answer may contain warnings (a missing
supervisor, secrets to be added by hand) — pass them on to the user.

## Conventions & hard-won lessons (keep to these)

These rules come from real mistakes — always take them into account when designing
`SOUL.md`/`PLAYBOOKS.md`/`HEARTBEAT.md`:

- **Always end with `done`, never `blocked`** on polling target systems without a webhook
  (GitLab, email): the heartbeat picks open work back up. `blocked` is only for genuine external
  waiting events.
- **Idempotency against loops:** a heartbeat fires repeatedly. The agent has to check BEFORE
  acting whether it has already done the work (e.g. `list_notes`: its own comment/MR link
  present, no new human input) → then **skip**. **Never post the same comment again.**
  (GitLab does throttle identical follow-up comments server-side, but the prompt still has to do
  it cleanly — otherwise re-checkout/re-fix costs.)
- **Hand off instead of hoarding:** once the work has been handed over (an MR opened), pass the
  ticket on (`assign` to the manager) and/or put `Closes #<iid>` in the MR description so that it
  closes on the merge — no ticket stays with the agent endlessly.
- **Work visibly, or it wakes you again.** The `nur-wenn:` condition triggers on the **edge**:
  an issue/MR counts as finished as soon as the **last non-system comment is from the bot**. A
  run that does something without commenting leaves no edge — at the next interval the same work
  counts as open again. So: **whoever works, comments.**
- **Measure the interval by the duration of a run, not by the wish for a reaction time.** An issue
  end to end (checkout, analysis, fix, MR) takes minutes to quarter-hours — `alle: 15m` is
  realistic, anything under 5m is wrong for agents that touch code.
- **Break up assignments that are too big instead of running into the turn limit:**
  `covey/create_task` files a subtask (without `agent`) or delegates to a colleague
  (`"agent":"<slug>"`). The playbook step reads: close the partial result off cleanly, file the
  rest as a task. If an agent runs into the limit regularly, the assignment is cut too large or
  `max_turns` is too small.
- **Stages are states, not headlines.** `covey/set_stage` creates missing columns automatically —
  prescribe a **fixed, small** set of column names in the playbook (e.g. `Triage`, `Analysis`,
  `Waiting for review`). Never the item in the column name (`#83 CSV import`), never synonyms for
  the same state; otherwise the board grows a dozen dead columns within days.
- **Rare procedures belong in a skill, not in `PLAYBOOKS.md`.** The config sits in the prompt on
  EVERY run — an agent with five playbooks pays for all five even for a run that establishes
  after three turns that there is nothing to do. Of a skill only the `description` sits
  permanently in context; the body and any extra files are read by the runtime only when it pulls
  it. Rule of thumb: the standard procedure stays in the playbook, special cases/checklists/
  templates/reference tables become skills. Schema and rules: reference.md.
- **`ACCESS.md` syntax:** one line per system: `- system: <name> scope: <scope1>,<scope2>`.
  Unlock only what the agent really needs (least privilege).
- **`HEARTBEAT.md` syntax:** one line per trigger:
  `- alle: <interval> nur-wenn: <system>:<kind> titel: <short> aufgabe: <what exactly is to be done>`.
  The keys stay German — a parser reads them, they are the data format, not text. `nur-wenn` only
  wakes when there is work (a cheap advance check by the control plane). Keep `aufgabe:` texts
  tight and concrete — the agent reads them as its assignment.
- **A warm sandbox** (`agent.warm_sandbox: true`) only for agents that bring a dev server up or
  have heavy builds/dependencies (QA/developer) — it saves the cold setup but occupies resources
  permanently. Research/triage agents stay ephemeral.
- **Browser agents:** the actions `navigate/content/screenshot/click/type`. Selectors are CSS plus
  `:has-text("…")` (the innermost visible text hit). `screenshot` can take `highlight`+`label` to
  mark a defect visually. Reachability depends on the **egress allowlist**.
- **Never put secrets in the bundle:** only names (`ACCESS.md` / the `secrets` block). Values are
  assigned separately in Covey and brokered at runtime.
- **Team reference:** refer in `SOUL.md`/`PLAYBOOKS.md` to "the team (AI colleagues)" instead of
  fixed names — Covey plays the team directory into the prompt (e.g. who gets entered as the
  reviewer).
- **Every agent reports upwards.** `PLAYBOOKS.md` gets a section telling the agent to report what
  held it up when it was NOT the assignment — a missing action, a tool it is not allowed, a limit
  it keeps hitting — to the improvement engineer by `covey/create_task`, instead of quietly
  working around it (spec/21). Write it so it degrades: the agent looks for such a colleague in
  its prompt, and puts the report into its result if there is none. The platform can see THAT a
  run went wrong; why it went wrong only the agent knows, and only while it still knows. The
  shipped bundles carry the section verbatim — copy it from one of them.

## Self-check before creating

- [ ] Bundle JSON valid, `kind`/`version` set, `agent.slug` + `agent.display_name` present?
- [ ] All five files present, in one language throughout, in the template's tone?
- [ ] Does `ACCESS.md` cover exactly the actions used in `PLAYBOOKS.md` — no more?
- [ ] Rarely used but extensive procedures moved into a skill instead of the playbook?
- [ ] `HEARTBEAT.md` describes the end-with-`done` logic + idempotency/skip?
- [ ] Loop protection (idempotency, no double comment, hand-off) anchored in the prompt?
- [ ] Does every run leave a visible trace (a comment) — otherwise the edge wakes it again?
- [ ] Does `PLAYBOOKS.md` have the section on reporting what held the agent up (spec/21)?
- [ ] Does the interval fit the duration of a run (code agents ≥ 5m, realistically 15m)?
- [ ] A fixed, small set of stage names in the playbook instead of freely invented columns?
- [ ] `warm_sandbox` set deliberately (only for dev/test)?
- [ ] The necessary secrets + egress hosts named to the user?
