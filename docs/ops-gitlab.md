# Operations: connecting Covey to a GitLab

A practical runbook for the target system **GitLab** (`internal/target/gitlab`).
Structure and data flow follow the Zammad adapter
([`ops-zammad.md`](ops-zammad.md)) — the unit of work here is the
**issue** instead of the ticket.

> Short version: token auth against the REST API (`/api/v4`). The agent finds
> its issues **itself** (`list_issues`), driven by a
> `HEARTBEAT.md` entry — GitLab takes up work **purely by polling**, there is
> no webhook (deliberately: with several agents per project a webhook setup is
> laborious and error-prone). It answers bug reports **code-based**:
> `checkout` fetches the source into the sandbox, and a report is confirmed
> only with a location in the code (section 4). These are **configuration
> steps**, not a rebuild.

---

## 1. Overview of the data flow

**Intake exclusively by heartbeat (polling).** A `HEARTBEAT.md` entry puts the
task "review issues" into the agent's backlog periodically. The agent finds its
working set itself through the discovery actions:

```
Covey  ──(heartbeat tick)──►  backlog task "review GitLab issues"
                                 │
                                 ▼
                              agent (sandbox, Claude Code)
                                 │  actions through the action proxy
GitLab  ◄──(REST /api/v4)────────┘  list_issues → get_issue/list_notes → checkout → comment/set_state/escalate
```

No inbound traffic, no public URL, no webhook secret — it works even when Covey
runs behind NAT/a firewall.

> **No webhook for GitLab.** Unlike Zammad, the GitLab plugin has **no**
> webhook intake (`/api/webhooks/gitlab/…` answers with 404). The reason: a
> webhook would have to be set up per target project and would address a single
> agent — with several agents on the same projects that quickly becomes
> ambiguous and hard to maintain. GitLab is therefore operated entirely by
> polling; the review loop (section 2.7) likewise runs through the heartbeat
> instead of `blocked`+wake. (The generic per-agent trigger
> `/api/trigger/<token>` for third-party systems is unaffected by this — it is
> not a target-system webhook.)

Auth (outbound only, Covey → GitLab): REST with a brokered API token
(the secret `gitlab_token`) that is never persisted in the sandbox.

---

## 2. Step-by-step instructions

### 2.1 In GitLab: create a user + token

1. Create **a user of its own** for the Covey agent (e.g. `covey-bot`) —
   do not use a person's token. All the agent's comments and commits run
   through this user; by `list_notes` it recognises its own last state at the
   next run and does not work on anything twice.
2. Add the user to the target projects with the **Reporter** role (enough for
   comments including internal notes; for closing issues, Developer depending
   on the project setup).
3. As that user create a **personal access token** with the scope `api` — or a
   **project access token** per project (least privilege). Note the token down
   (it is shown only once).

### 2.2 In Covey: deposit the secrets

Set per agent in the SecretStore (UI: agent page → Secrets, or via the API):

| Secret | Value | Purpose |
|---|---|---|
| `gitlab_url` | `https://gitlab.example.com` | **without** `/api/v4` — the client appends that |
| `gitlab_token` | the token from 2.1 | outbound auth (the `PRIVATE-TOKEN` header) |
| `anthropic_api_key` *or* `claude_code_oauth_token` | an API key or `claude setup-token` | the runtime in the sandbox |

### 2.3 In Covey: enable the target system

The target system `gitlab` has to be **enabled** for the org (UI: Target
systems). In addition the agent has to be allowed to access `gitlab` according
to its `ACCESS.md`, and the guard rails must not forbid `gitlab` /
`gitlab:comment_external`.

### 2.4 Setting up the intake by heartbeat

Create **two separate** entries in the agent's `HEARTBEAT.md` — one job each
(issue triage, MR review), each gated on its own:

```
- alle: 15m nur-wenn: gitlab:issues titel: GitLab-Issues sichten aufgabe: Finde offene Issues (list_issues state=opened), bearbeite neue und prüfe per list_notes, ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code per checkout holen und die Behauptung am Quelltext verifizieren.
- alle: 15m nur-wenn: gitlab:mr titel: Merge Requests betreuen aufgabe: Prüfe deine offenen Merge Requests (list_merge_requests state=opened) auf neues Review-Feedback (list_mr_notes), arbeite es ein und reagiere auf Merge bzw. Close.
```

**Why two heartbeats with a sub-scope instead of one.** Because the agent does
**not** block after `create_merge_request` but ends with `done`, a heartbeat has
to pick its open MRs back up by polling — the review loop is a job of its own.
The sub-scope after the colon (`gitlab:issues` or `gitlab:mr`) ensures that each
of the two heartbeats fires **only for its own work**:

- `nur-wenn: gitlab:issues` → only when an open issue in the intake scope is
  **waiting** for a reaction (see "edge instead of level" below).
- `nur-wenn: gitlab:issues:assigned` → the same, but only for the issues
  **assigned** to the bot user (`scope=assigned_to_me`) — for agents whose
  playbook works exclusively on their own issues.
- `nur-wenn: gitlab:mr` → only when one of the open merge requests opened by the
  bot itself has **unanswered review feedback** (the last non-system comment in
  the thread is not from the bot).
- `nur-wenn: gitlab:review` → only when an MR in which the bot is entered as a
  **reviewer** is waiting for its review.

Without a sub-scope (`nur-wenn: gitlab`) a heartbeat checks **both together** —
but then two such heartbeats would each fire for the other's work as well (the
MR task would run on pure issue work and vice versa). The sub-scope avoids
exactly that waste; use `nur-wenn: gitlab` only when you deliberately want to
bundle both jobs into **one** task.

The merge completion needs no trigger of its own: if the associated issue is
still open, it wakes through the issue heartbeat; if it was closed automatically
on merge, there is nothing left to do.

**Edge instead of level — and what that demands of the agent.** An open issue
or an open MR is *not* permanently "work". An item counts as work only as long
as the **last non-system comment is not from the bot** (or there is none yet —
then the first triage is outstanding). If the bot wrote last, the item rests
until someone answers.

From that follows a contract the playbook has to keep: **whoever has worked on
an issue comments there.** A silent run leaves no edge, counts as unworked again
at the next interval and wakes the agent anew — at `alle: 2m` that is 30 runs
per hour on the same long-finished item. Exactly this level trigger was the
cause of an endless loop in practice, see
[heartbeat intervals](#25-choosing-realistic-intervals).

The advance check is cheap (a few REST calls: open issues or your own open MRs
and their notes) — negligible compared with an LLM turn.

The agent then discovers its working set itself: `list_projects` delivers the
projects the bot user is a member of, `list_issues` the open issues (without
`project_id`: all the ones the token may see). So that recurring runs do not
work on anything twice, the agent checks by `list_notes` whether its own
comment is already the last state — the plugin's prompt documentation points it
at that.

An optional project filter (it takes effect for `list_issues`/`list_projects`
and the `nur-wenn:` advance check):

```bash
COVEY_GITLAB_INTAKE_PROJECTS="group/support"   # empty = all projects
```

### 2.5 Choosing realistic intervals

The interval has to fit the **duration of a run**, not the desired reaction
time. Working an issue end to end (clone the repo, read the code, fix, MR)
takes minutes to quarter-hours; `alle: 2m` then does not mean "faster" but
"the next run begins before the previous one has understood what it is about".
**15m for issue triage, 15m for the MR loop** are a good starting value;
nothing that touches code should be below 5m.

Two built-in brakes dampen this but do not replace a sensible interval:

- A heartbeat does not fire while the task from its last run is still
  open (no stacking).
- The `nur-wenn:` condition checks the edge, not the level (see above).

What neither catches: a run that ends without a result at the turn limit.
Covey recognises that (`max_turns`), has the run summarise its own interim
state and creates a **follow-up task** from it that continues the session —
instead of letting the next heartbeat start from zero. After several
continuations in a row the task escalates to the manager instead of continuing.
If that happens regularly, the assignment is cut too large or `max_turns` is too
small.

### 2.6 Testing

1. Create an issue in the target project → at the next heartbeat run the agent
   takes it up.
2. If the agent replies, its comment has to appear under the `covey-bot` user.
3. On a follow-up question the agent does **not** go `blocked`: it puts the
   question as a comment, closes its run with `done` and checks at the next
   heartbeat by `list_notes` whether an answer is there.

### 2.7 The review loop: the agent as a developer

If the agent fixes a bug itself, it works like a flesh-and-blood developer — but
waiting for the review runs **by polling**, not through `blocked`:

1. `checkout` of the project into the sandbox, **set the project up** (install
   dependencies, run the build and tests once in the initial state — the
   necessary package registries are released by the egress through the built-in
   templates, e.g. npm/PyPI/Go).
2. Develop the fix, run the tests, push onto a feature branch by `commit`,
   `create_merge_request` to the manager, comment the link in the issue.
3. The agent ends its run with `done` — **no `blocked`**. (Without a webhook, a
   `blocked` would never be woken and would occupy the heartbeat task
   permanently, so that no new "review issues" runs would arise.)
4. At the next heartbeat run the agent checks its open MRs
   (`list_merge_requests state=opened` → `list_mr_notes`) for new review
   feedback. If it demands changes, it checks the source branch out again, works
   the points in, runs the tests, pushes onto the same branch and answers by
   `comment_mr` — then `done` again.
5. If an MR has been **merged** (`list_merge_requests state=merged` /
   `get_merge_request`), the agent comments the result in the issue; if it was
   **closed** without a merge, it checks by `list_mr_notes` why and escalates if
   that is unclear.

For this loop to run reliably, the MR heartbeat
(`nur-wenn: gitlab:mr`, section 2.4) belongs in the `HEARTBEAT.md`. It wakes the
agent exactly when one of its open MRs has unanswered review feedback —
regardless of whether an issue happens to be open.

### 2.8 The QA/test agent: testing other people's MRs end to end

The review loop from 2.7 waits by default for a **human** (the manager, who is
entered as the MR assignee). But the review can also be given to a **second
agent** — a QA/test agent that accepts the feature and gives the developer agent
feedback. Both are normal Covey agents; they work together through GitLab
(Covey knows no direct agent-to-agent task handover — the collaboration runs
through the shared target system). The trick: the developer agent already has
the MR review loop (2.7) — if the QA agent comments defects on the MR, the
developer picks them up **automatically** at its next `gitlab:mr` run. So only
the QA agent's intake side is needed.

**Sequence:**

1. The developer agent **finds the QA agent itself** — it does not have to know
   a user name. At dispatch time its prompt contains the section
   **"Team (AI colleagues)"** with all the organisation's other agents, their
   GitLab identifier, responsibility and department; colleagues from **its
   team** (the same department) are marked as `YOUR TEAM`. It picks from that
   the colleague responsible for testing — preferably from its own team — and
   enters them as the `reviewer` on `create_merge_request` (the manager stays
   the `assignee`):
   `create_merge_request {"project_id":N,"source_branch":"fix/…","title":"…","assignee":"leaddev","reviewer":"covey-qa"}`.
   It hands an existing MR over with
   `set_reviewer {"project_id":N,"mr_iid":N,"username":"covey-qa"}` and explains
   the handover in a `comment_mr`. If there is no QA colleague, the previous
   behaviour stands (the manager as both assignee and reviewer).
2. The QA agent finds its review queue through the sub-scope
   **`nur-wenn: gitlab:review`**: it fires only when an MR in which the QA bot
   is entered as a reviewer is waiting for its review (no comment, or the last
   non-system comment is not from the QA bot). A freshly handed-over MR without
   a comment **does** count as work here — it is waiting for the first review
   (unlike in the developer loop, where a fresh MR waits for the reviewer).
3. The QA agent brings its **working tree for the project** to the source branch
   (one tree per project, kept across acceptances — not one per MR), **starts
   the application and plays the feature through end to end** in the browser
   (not just reading the diff) and supports states and defects with screenshots
   it attaches to the MR. The full test suite runs as a **job** (`dev start`):
   its result is recorded in the sandbox home and stays readable in the next run
   even if this one ends at the turn limit.
4. The result by `comment_mr`: on defects concretely with file:line and a
   reproduction (the developer agent works them in through its `gitlab:mr`
   loop); if everything is green, the QA agent says so explicitly and releases
   it with `approve_mr`.
5. **The merge**, where the tool `merge_mr` is assigned to the agent: it merges
   only its own acceptance. The action checks fail-closed beforehand that the MR
   is open and free of conflicts, every blocking discussion is resolved, the
   pipeline of the head commit is green and the agent's own approval is on
   record — and merges exactly the commit it saw. Without `merge_mr` the
   approval stays its last word and the merging is done by the human.

**Setting up the QA agent.** A ready-made example sits under
`examples/qa-agent.bundle.json` (SOUL/CAPABILITIES/PLAYBOOKS/ACCESS/HEARTBEAT
including `nur-wenn: gitlab:review`). For the automatic assignment from step 1
to take hold, five steps are needed — the bundle supplies the first two, the
remaining three are **master data** the bundle deliberately does not carry
(profile identifiers and department are never exported):

1. **Create the agent:** import the bundle
   (`POST /api/v1/agents/import`, or "Agent from bundle" in the UI) — it creates
   the agent `covey-qa` with all its config files.
2. **Assign secrets:** deposit `gitlab_token` + `gitlab_url` as in 2.2 and
   assign them to the QA agent; enable the GitLab and `dev` target systems for
   it.
3. **Set the GitLab identity:** enter the GitLab identifier in the QA agent's
   profile (e.g. `gitlab: covey-qa`) — **only then** does it appear in the other
   agents' "Team (AI colleagues)" directory with a user name the developer can
   enter as `reviewer`. Without a GitLab identity it is not addressable by the
   colleagues.
4. **Set the responsibility:** in the profile, `Responsibilities` = "tests merge
   requests / QA" or similar — that is the criterion by which the developer
   agent recognises the QA colleague (a job title "QA agent" helps too).
5. **Put it in the same team:** assign the QA agent to **the same department**
   as the developer agents (org chart → department). It is then marked as
   `YOUR TEAM` in their prompt and preferred. (If it is in no department or a
   different one, it is still found organisation-wide by responsibility — just
   not preferentially.)

Its own bot user has to exist in GitLab (e.g. `covey-qa`, the Reporter role is
enough for commenting; for `approve_mr` Reporter suffices too, provided the
project allows approvals). For `merge_mr` it needs at least **developer**, and
`maintainer` on protected target branches. Its token sits in the QA agent's
`gitlab_token` — so the developer and QA agents write under **different** GitLab
users, and the review loop distinguishes "author" from "reviewer" cleanly. That
separation is what carries the merge gate: GitLab lets nobody approve their own
merge request, so a developer agent can never merge its own work, whatever its
tool assignment says.

Whoever does not want an autonomous merge takes the tool `merge_mr` out of the
QA agent's ACCESS.md (then only `approve_mr` remains) or denies the subject
`gitlab:merge_mr` organisation-wide by a guard rail — with the decision
`ask` the merge lands on the Approvals page and a human clicks it through.

> **Egress:** for the QA agent to really be able to start the application, its
> sandbox needs the same package registries as the developer agent (npm/PyPI/Go
> through the built-in egress templates) — see `docs/ops-deployment.md`.

### 2.9 The delivery lead: leading a whole undertaking

Developer and QA agents work ticket by ticket. If the work hangs off a
**milestone with a deadline** — a tender, a release — a level above them comes
in: the delivery lead (`examples/delivery-lead.bundle.json`).
It makes tickets implementable (read the requirement in the original, comment
checkable acceptance criteria, name the affected places by
`list_tree`/`read_file`), holds dependent tickets back until their foundation is
merged, and hands out work according to a WIP limit.

Four things distinguish its setup from the others:

1. **The developer agents have to be on `nur-wenn: gitlab:issues:assigned`**
   (that is how the template is cut). If they reach freely for open issues, the
   lead's preparation comment already wakes them — the WIP limit and the order
   are then ineffective, and the lead is decoration.
2. **The lead's `ACCESS.md` carries a `tools:` allowlist.** Its role is defined
   by a long list of prohibitions (do not commit, no MRs, do not merge, do not
   close tickets); that is enforceable only centrally, not in the prompt (spec:
   guard rails fail-closed outside the runtime). `commit`,
   `create_merge_request`, `approve_mr`, `set_state` and `checkout` are
   therefore not unlocked, even though `scope: write` would allow them.
3. **Nothing undertaking-specific sits in its config.** Project, milestone,
   target branch, deadline, requirements path, WIP limit, dependencies and the
   report ticket sit in a wiki page, the **undertaking profile** (2.9.1). One
   lead leads exactly one undertaking — for a second one, a second lead.
4. **Its craft sits in skills, not in `PLAYBOOKS.md`.** The four procedures —
   `ruecklaeufer`, `arbeit-vergeben`, `ticket-aufbereiten`, `tagesbericht` — sit
   as skills on the agent (tab *Tools & skills* → *Skills*); the playbook only
   says in what order they come up. The reason is the prompt arithmetic: the
   config sits in the prompt on EVERY run, and the lead runs every 30 minutes,
   mostly without finding anything. This way an empty run costs around
   3,000 instead of 5,100 tokens, and a procedure's full text is only read when
   it is needed. So whoever adapts the procedures does it in the skill —
   `PLAYBOOKS.md` is only changed by whoever wants to change the order.

In addition to its own bot user it needs a permanently open **report ticket**
assigned to it and a human manager **whose GitLab identifier is deposited in
their profile** — without it, `assign` fails in exactly the path that hands open
subject-matter questions to humans.

#### 2.9.1 The undertaking profile

At the beginning of every run the lead reads a page from its wiki memory
(`covey/wiki_search` → `covey/wiki_read`) and writes what it has learned back
there: a dependency that only showed up during preparation, a decision by the
client. That keeps the **config** generic — the same template leads every
undertaking, because it knows none of them.

Create it through the agent's memory view in the UI or as a task to it. The page
title: `Engagement brief <milestone title>`.

```markdown
# Engagement brief <milestone title>

## Mandatory
- **Project ID:** <the numeric GitLab project id, not the path>
- **Milestone title:** <exactly as in GitLab — the filter matches literally>
- **Target branch:** <the branch that is developed against>
- **Deadline:** <date> — <what it derives from>

## Requirements in the original
- **Path in the repository:** <e.g. docs/requirements/criteria.md>
- **What is authoritative:** <which document wins on a contradiction with the ticket text>

## Steering
- **WIP limit:** <tickets at a time per developer; without a value 1 applies>
- **Report ticket:** project <id> / #<iid> — this is where the lead writes the
  daily state. It has to be assigned to the lead and permanently open.
- **Responsible human:** <name>, GitLab identifier <username> — receives open
  questions and the report. Without a deposited GitLab identifier `assign` fails.

## Order and dependencies
- #<iid> before #<iid>, #<iid>, … — <reason: a shared foundation>

## Decisions
<What the client has settled, with a date. The lead enters every answered
question here — otherwise it asks it again at the next ticket.>

## Open questions
<What nobody has answered, with the ticket and how long it has been waiting.
The daily report reads from here.>
```

The German bundle (`delivery-lead.de.bundle.json`) uses German headings
(`Vorhaben-Steckbrief`, `## Pflicht`, `## Steuerung` …) — the brief and the
skills that read it have to speak the same language, so take the headings from
the bundle you actually instantiated.

Why these fields:

- **The project ID and milestone title** are the cut of the working set
  (`list_issues {"project_id":N,"milestone":"…"}`). Without them the lead either
  grabs nothing or the whole project.
- **The path to the requirements** is the difference between a lead that sorts
  tickets and one that makes them implementable: a ticket text is a summary, and
  the acceptance criteria have to come from the original. The documents
  therefore belong in the repository — versioned and readable by all colleagues.
  If the path is wrong, the lead searches for the file once with `list_tree` and
  corrects the profile itself; if it does not find it, it **aborts the
  preparation** and reports that once in the report ticket instead of guessing
  criteria from ticket titles. An undertaking that does not get going is to be
  checked here first.
- **The WIP limit and the order** are the brake against the most common error:
  several agents work simultaneously on tickets that share the same foundation
  and produce contradictory implementations on one branch. When in doubt, set it
  lower.
- **Decisions** prevent an answered question from being asked again in every
  further ticket. An individual ticket's comment history is the wrong place for
  that — at the next ticket it is out of sight.

---

## 3. Which issues does the agent take up?

The agent decides for itself: `list_issues` delivers only open issues, and the
project allowlist (3.3) filters the results of `list_issues`/`list_projects`
server-side. If the agent should work **only on issues assigned directly to it**,
there is `list_issues {"assigned":true}` (GitLab `scope=assigned_to_me`,
relative to the token's bot user) — the rule itself belongs in the agent's
`PLAYBOOKS.md`/`HEARTBEAT.md`; in addition every issue delivers its
`assignees` along, so the agent can also check the assignment case by case.

If the assignment hangs off an **undertaking** rather than individual tickets —
a tender, a release — the milestone is the more robust cut:
`list_issues {"project_id":15,"milestone":"ECA-2026-045 Bundesdruckerei LMS"}`
filters GitLab-side on the milestone **title**, and every issue carries its
`milestone` (with `due_date`) back. Labels remain usable alongside
(`{"labels":"MUSS-Kriterium"}`) but carry no deadline.

### 3.1 No double working

Because the intake runs by polling, the agent sees the same open working set
again on every run. So that recurring runs do not work on anything twice, it
checks by `list_notes` (or `list_mr_notes` for MRs) whether its own comment is
already the last state, and reacts only to answers newly added since then. The
plugin's prompt documentation commits it to that.

### 3.2 Long comment threads

`list_notes`/`list_mr_notes` deliver a **window at the new end** of a thread: by
default the newest 20 comments, `limit` up to 100, `page` counts backwards into
the history (`page=2` the 20 before them). The answer describes itself —
`window`, `total`, `has_more`, and `truncated` where something is missing — so
that an agent can tell a full window from a complete history.

That matters for tickets which run for months: an issue taking a daily report
carries hundreds of comments, and whoever loads them all pays for them in the
context of every single call. Comments longer than 4000 characters therefore
arrive cut off as well (`body_truncated`); `get_note` fetches an individual one
in full.

The internal readers — the duplicate check and the `nur-wenn:` advance check —
are not affected by the small window: their answer never reaches an agent, so
they read the last 100 comments of a thread.

### 3.3 The project allowlist

```bash
COVEY_GITLAB_INTAKE_PROJECTS="group/support, 42"
```

If the variable is set, `list_issues`/`list_projects` and the `nur-wenn:`
advance check deliver only hits from these projects (the project path
`path_with_namespace` case-insensitively, or the numeric project id).
Empty/unset → no restriction. The primary filter nevertheless remains the
GitLab-side setup: enter the bot user only in the target projects.

---

## 4. Code-based answers: `checkout`

The agent should not answer bug reports "from memory" but check the claim
**against the source**. The action for that is

```
checkout {"project_id":15, "ref":"main"}     # ref optional, default: the default branch
```

Sequence and security model:

- The **daemon** (not the runtime) downloads the repository archive through the
  API (`GET /projects/:id/repository/archive.tar.gz`) with the brokered token —
  the token stays in the daemon's RAM and **never** lands in the sandbox's file
  system (unlike a `git clone` with a credential remote, which would persist the
  token in `.git/config`).
- It is unpacked into `<home>/repos/p<project>-<ref>/`; the action returns that
  directory as `path`, and the agent then works locally with grep/read/bash. A
  renewed checkout replaces the old state (always fresh code). A **partial**
  checkout (`path`) lands underneath, at the place it occupies upstream, so
  several partial checkouts of one ref grow into ONE working tree — `path` in
  the result stays the repository root, `local_path` names the subtree.
- The home is persistent, so working copies would pile up in it without bound.
  After every checkout the least recently used ones fall away; five survive
  (`COVEY_CHECKOUT_KEEP`, `0` switches the cleanup off). Which ones went is in
  the checkout result, because the agent may be holding a path from an earlier
  run. How full the sandbox is, and which working copies are eating it, is on
  the agent's **Workplace** tab.
- Protections: path traversal is refused, symlinks are skipped, and the unpacked
  size is limited (default 512 MB, `COVEY_GITLAB_CHECKOUT_MAX_MB`).
- Guard-rail subjects: `gitlab:checkout`, `gitlab:list_tree`,
  `gitlab:read_file` (all read-only towards GitLab; whoever wants to restrict
  them puts rules on them).

**Large repos:** if the archive blows the limit, there are two ways out — both
are also in the error message the agent sees:

- **A partial checkout**: `checkout {"project_id":N, "path":"web/upload"}` loads
  only the subdirectory (the `path` parameter of the GitLab archive API).
  Several of them share one working tree, so everything the project needs to
  build has to be fetched BEFORE the work starts — every checkout redraws the
  baseline commit and would swallow changes made in between.
- **Browsing without a checkout**: `list_tree {"project_id":N, "path":"...",
  "recursive":true}` lists the repository tree (max. 100 entries per
  call), `read_file {"project_id":N, "file_path":"path/to/file"}` reads a
  single file (up to 512 KB, above that `truncated:true`).

**History and MRs — "has that already been fixed?":** a checkout is an archive
without `.git` — the agent sees no history through it. There are four further
read-only actions for that (guard-rail subjects `gitlab:list_commits`,
`gitlab:get_commit`, `gitlab:list_merge_requests`, `gitlab:list_branches`):

```
list_branches       {"project_id":N, "search":"..."}          # the default branch is marked
list_commits        {"project_id":N, "ref":"...", "path":"...", "since":"2026-07-15T00:00:00Z"}
get_commit          {"project_id":N, "sha":"..."}             # a diff, truncated to 16 KB per file
list_merge_requests {"project_id":N, "state":"merged", "search":"...", "target_branch":"..."}
```

The plugin's prompt documentation commits the agent to this way of working:
**first** check whether the reported error has already been fixed since the
issue was created (`list_commits` with `since`, `list_merge_requests`; verify
suspicious commits with `get_commit`) — if so, it reports exactly that with a
commit reference instead of confirming the bug again. Only then: **confirm** a
bug only with a location (file:line) in the checked-out code, and only after the
reported route has been followed completely (UI → endpoint → processing — the
error can be in the frontend even when the backend looks suspicious); if it does
not find the place, it describes what it has checked and asks a targeted
follow-up question. Answers without evidence in the code are permissible only
for purely organisational issues. Prerequisite: the token from 2.1 needs read
access to the repository (the scope `api` covers that; the Reporter role
suffices for the archive download on private projects).

**Reading screenshots and image attachments:** bug reports often attach a
screenshot — in the issue description (or a comment) it sits as a Markdown
upload:

```
![Fehlermeldung](/uploads/0123456789abcdef0123456789abcdef/login-fehler.png)
```

The agent only gets this text, **not** the image — the content cannot be derived
from the reference alone. The action `download_upload` exists for that:

```
download_upload {"project_id":15, "url":"/uploads/0123…/login-fehler.png"}
```

- The **daemon** loads the upload brokered through
  `GET /projects/:id/uploads/:secret/:filename` (the token stays in the daemon
  and never lands in the file system) and puts the file under `<home>/uploads/`;
  the action returns the local path.
- The agent then looks at the image with the **read tool (vision)** — so it can
  actually evaluate the screenshot instead of passing over it. The prompt
  documentation commits it to that: if it sees an image attachment in the
  Markdown, it **always** downloads it and looks at it first, before taking it
  into account in its analysis. As the `url` it passes the reference exactly as
  it stands between the Markdown brackets (a bare `/uploads/…` path or a full
  web URL — both are mapped onto the upload endpoint).
- Protections: the file name is nailed to the basename (no path traversal), and
  the size is limited to 25 MB. Guard-rail subject:
  `gitlab:download_upload` (read-only towards GitLab).
- Prerequisite: the upload API endpoint needs **GitLab ≥ 16.6**; on older
  instances it returns `404`, which the action reports with a corresponding
  note.

---

## 5. Internal vs. public comments

The adapter distinguishes — analogous to `reply` at Zammad:

- **internal** (`comment` with `internal:true`, the default) → a GitLab
  "internal note", visible only to project members from Reporter upwards.
- **external** (`comment` with `internal:false`) → a public comment, visible to
  external reporters too. Guard-rail subject: `gitlab:comment_external` — this
  is typically where an approval rule takes hold.

`escalate` puts an internal note and removes the issue's assignment so that a
human takes over. `set_state` knows `close` and `reopen`
(the GitLab `state_event`).

### 5.1 The working state on the board: `set_labels`

`set_labels {"project_id":15,"issue_iid":739,"add_labels":["in progress"],"remove_labels":["ready"]}`
changes the labels of an **existing** issue. Deliberately additive/subtractive
(the GitLab `add_labels`/`remove_labels`) rather than as a full list: an agent
that maintains the working state would otherwise wipe the ticket's subject-matter
labels — component, type, procurement procedure — on every change. At least one
of the two lists has to be set; the answer contains the label state reached.

The state thereby belongs visibly on the board rather than only in comments — a
human sees without asking what is ready, what is running and what is waiting for
acceptance. The prerequisite is that the agent does **both** on every change:
remove the old state label, set the new one. Guard-rail subject:
`gitlab:set_labels`.

Two idiosyncrasies you have to know when designing such an agent:

- **GitLab silently creates unknown labels when setting them.** A typo by the
  model (`lead::in_progress` instead of `lead::in-progress`) therefore produces a
  permanent project label that nobody clears away again — the same trap as with
  freely invented board columns. The playbook therefore has to prescribe a
  **fixed, small** set of state names character by character. The plugin's
  prompt documentation points the agent at this; a label with a comma in it is
  refused instead of silently falling apart into two labels.
- **`::` turns them into GitLab scoped labels** — but mutually exclusive only in
  Premium/Ultimate. On Free they are normal labels with `::` in the name.
  An agent therefore must not rely on GitLab removing the old state label
  automatically; it has to include it in `remove_labels` itself. That is exactly
  why the rule "both in the same call" stands above.

---

## 6. Env reference (GitLab-relevant)

| Variable | Default | Meaning |
|---|---|---|
| `COVEY_GITLAB_INTAKE_PROJECTS` | *(empty = all)* | An allowlist of projects (path or id) — filters `list_issues`/`list_projects` and the `nur-wenn:` advance check |
| `COVEY_GITLAB_CHECKOUT_MAX_MB` | `512` | The upper bound on the unpacked size of a `checkout` (section 4) |
| `COVEY_CHECKOUT_KEEP` | `5` | How many working copies survive under `<home>/repos`; `0` switches the cleanup off (section 4) |
| `COVEY_EGRESS_ALLOW` | *(empty)* | Additional permitted egress hosts, e.g. the GitLab host |

GitLab has no webhook intake — the former variables `COVEY_PUBLIC_URL`
(for GitLab only), `COVEY_GITLAB_WEBHOOK_SECRET` and
`COVEY_GITLAB_AGENT_USERNAMES` no longer apply to this plugin.

The general variables (egress, the daemon token TTL, …) are in
[`ops-zammad.md`](ops-zammad.md), section 6.
