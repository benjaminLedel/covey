# Operations: connecting Covey to GitHub

A practical runbook for the target system **GitHub** (`github.com/benjaminLedel/covey-plugin-pack/github`).
Structure and data flow follow the GitLab adapter
([`ops-gitlab.md`](ops-gitlab.md)); the unit of work is the **issue**, and the
merge request is called a **pull request** here.

> Short version: token auth against the REST API (`https://api.github.com`, no
> `github_url` needed). The agent finds its issues **itself**
> (`list_issues`) — driven either by a `HEARTBEAT.md` entry (polling) or by the
> **webhook**, which GitHub, unlike GitLab, offers per repository. It answers
> bug reports **code-based**: `checkout` fetches the source into the sandbox,
> and a report is confirmed only with a location in the code (section 5). These
> are **configuration steps**, not a rebuild.

Two things differ from GitLab and shape everything below:

| | GitLab | GitHub |
|---|---|---|
| Repository identifier | numeric `project_id` | the name `"owner/repo"` |
| Intake | polling only | polling **and/or** webhook |
| Internal comments | yes (`internal:true`) | **no** — every comment is public |
| Attachment upload | `upload` action | **not possible** (no API) |

---

## 1. Overview of the data flow

There are two routes in, and either on its own is enough.

**a) By heartbeat (polling)** — works behind NAT/a firewall, needs no public
URL. A `HEARTBEAT.md` entry puts the task into the agent's backlog
periodically; the agent finds its working set itself:

```
Covey  ──(heartbeat tick)──►  backlog task "review GitHub issues"
                                 │
                                 ▼
                              agent (sandbox, Claude Code)
                                 │  actions through the action proxy
GitHub ◄──(REST api.github.com)──┘  list_issues → get_issue/list_comments → checkout → comment/set_state
```

**b) By webhook** — real time, needs a publicly reachable Covey
(`COVEY_PUBLIC_URL`):

```
GitHub ──(POST /api/webhooks/github/<agent>)──►  Covey
           X-Hub-Signature-256 (HMAC-SHA256)       │ verify → deduplicate → correlate
                                                   ▼
                                       new task  or  wake a blocked task
```

The division of labour between the two is deliberate and is what lets you run
both at once:

- A **newly opened** (or reopened) issue in scope becomes a **task**.
- Everything else — a comment, a review, the close of a pull request — only
  **wakes a task that is already waiting** for exactly that thread
  (`CorrelateOnly`). If nobody is waiting, it is not new work: the heartbeat's
  edge check finds the thread at its next run anyway, and a task created here
  on top of that would mean the same job done twice.

---

## 2. Step-by-step instructions

### 2.1 On GitHub: create an account + token

Give the agent an account of its own (`covey-bot`, say) and add it to the target
repositories. Then generate a token **as that account**:

- **Fine-grained token** (recommended, Settings › Developer settings ›
  Personal access tokens › Fine-grained tokens). Select the repositories in
  question and grant:

  | Permission | Level | What needs it |
  |---|---|---|
  | Metadata | read | mandatory for everything |
  | Issues | read & write | `list_issues`, `comment`, `set_labels`, `assign`, `set_state` |
  | Contents | read & write | `checkout`, `read_file`, `list_tree`, `commit` |
  | Pull requests | read & write | `create_pull_request`, `comment_pr`, `approve_pr`, `request_changes` |
  | Actions | read | `list_workflow_runs`, `get_job_log`, `rerun_failed_jobs` |

  An agent that only reads and comments does **not** need the write levels on
  Contents and Pull requests.

- **Classic token:** scope `repo` (plus `read:org` if the agent should see the
  organisation's repositories through `list_repos`).

> **Fine-grained tokens expire.** GitHub caps them at one year. Note the date —
> when it lapses, every action fails with `HTTP 401` at once. The token is
> replaced in Covey under Secrets; nothing else changes.

### 2.2 In Covey: deposit the secrets

Under **Secrets**, and assign them to the agent:

| Name | Value |
|---|---|
| `github_token` | the token from 2.1 |
| `github_url` | **only** for GitHub Enterprise Server, e.g. `https://ghe.example.com` |

`github_url` may be left out for github.com — the plugin knows its endpoint
(`BaseURLOptional`). For Enterprise Server, enter the host as it stands in the
browser; the plugin appends `/api/v3` itself.

> Assigning is a separate step from creating. A secret that exists but is not
> assigned to the agent produces an action that fails with a broker error at
> run time — the most common setup mistake.

### 2.3 In Covey: enable the target system

In the agent's `ACCESS.md`:

```markdown
- system: github scope: read,write,comment
```

Without the entry the action proxy refuses every `github:*` action
(fail-closed).

### 2.4a Setting up the intake by heartbeat

Two entries in the agent's `HEARTBEAT.md`, each gated on its own:

```markdown
- alle: 15m nur-wenn: github:issues
  titel: Review GitHub issues
  aufgabe: Find open issues (list_issues state=open), work on the new ones and
    check with list_comments whether your queries have been answered. For bugs:
    fetch the code with checkout and verify the claim against the source.

- alle: 15m nur-wenn: github:pr
  titel: Look after pull requests
  aufgabe: Check your open pull requests (list_pull_requests state=open) for new
    review feedback (list_pr_comments), work it in and react to the merge.
```

The sub-scope after the colon saves the expensive agent wake deliberately:

| `nur-wenn:` | Fires when … |
|---|---|
| `github:issues` | ANY open issue in the intake scope is waiting for a reaction |
| `github:issues:assigned` | an open issue **assigned to the bot** is waiting |
| `github:pr` | one of the bot's **own** open PRs has unanswered feedback |
| `github:review` | a PR in which the bot is **reviewer** is waiting for its review |
| `github` | issues and one's own PRs together (only for one shared task) |

> **Use `github:issues:assigned` if your playbook only works on assigned
> issues.** Otherwise every open issue of somebody else's in the scope wakes the
> agent, it looks, finds nothing for itself and ends — every 15 minutes, at full
> runtime cost.

`mr` is accepted as an alias for `pr`, so a playbook carried over from GitLab
does not silently gate on nothing.

### 2.4b Setting up the intake by webhook

In the repository under **Settings › Webhooks › Add webhook**:

| Field | Value |
|---|---|
| Payload URL | `<COVEY_PUBLIC_URL>/api/webhooks/github/<agent-slug>` |
| Content type | `application/json` |
| Secret | a random value — deposit the **same** value in Covey as the agent's webhook secret |
| Events | *Let me select individual events*: Issues, Issue comments, Pull requests, Pull request reviews, Pull request review comments |

The signature is checked as HMAC-SHA256 over the raw body
(`X-Hub-Signature-256`). **An empty secret switches the check off** — that is
for local development only; a public endpoint without a secret lets anyone
create tasks in your org.

> **Name the bot's login.** If the agent works with a personal access token
> rather than a GitHub App, GitHub sees an ordinary user in its own comments.
> Without `COVEY_GITHUB_BOT_LOGINS="covey-bot"` the agent's own reply wakes it
> again — and that is the most expensive loop in the system. GitHub Apps
> identify themselves through `sender.type` and need no entry.

### 2.5 Choosing realistic intervals

A GitHub run checks out a repo, installs dependencies and runs tests — that is
minutes, not seconds. `alle: 15m` is the realistic floor; the config lint warns
below `5m` (`heartbeat-interval-too-short`).

Also worth watching: the API rate limit. A personal access token gets 5000
requests per hour. A `nur-wenn: github:issues` check costs roughly *1 + n*
requests (the issue list plus one comment list per open issue, capped at 30) —
at a 15 minute cadence that is far from the limit, at `1m` with 30 open issues
it is not. If a limit is hit, the plugin says so in plain terms instead of
letting a bare `HTTP 403` look like a permission problem.

### 2.6 Testing

1. Open an issue in the target repository as a human.
2. Trigger the agent (webhook: immediately; heartbeat: at the next tick or by
   hand).
3. Follow the recording under **Runs** — the actions and their results stand
   there, including the request log.

---

## 3. Which issues does the agent take up?

The agent decides for itself: `list_issues` delivers open issues, and the
repository allowlist (3.2) filters the results of `list_issues`/`list_repos`.
Pull requests are **sorted out** of the issue lists — GitHub delivers them
through the issue endpoints too, and an agent that saw them there would work on
the same item twice.

If the agent should work **only on issues assigned directly to it**, there is
`list_issues {"assigned":true}`; the rule itself belongs in the agent's
`PLAYBOOKS.md`/`HEARTBEAT.md`.

Two filters work **on the fetched page**, not in the request, because GitHub's
list endpoints do not offer them:

- `search` — a substring of title/body.
- `milestone` — the milestone **title**. (GitHub's API wants the milestone
  *number*, which differs per repository and which the agent cannot know.)

Both therefore narrow the *result*, not the query. Combine them with `repo` when
you need certainty that nothing was cut off.

### 3.1 No double working

The intake is level-triggered by nature, so the gate triggers on the **edge**:
an issue counts as handled as soon as the **last comment comes from the bot**.
From that follows a contract the prompt documentation commits the agent to:

> **An agent that has worked on an issue must comment there.**

A silent run counts as "not yet worked on" and wakes again in the next interval
— on the same, long-settled matter. The config lint catches the case where a
`github`-gated heartbeat has no playbook step that comments
(`no-visible-trace`).

For pull requests the **head SHA** goes into the signature alongside the newest
comment id. GitHub, unlike GitLab, does **not** record a push in the
conversation — without the SHA a reviewer would never learn of the commit that
followed its feedback.

### 3.2 The repository allowlist

```bash
COVEY_GITHUB_INTAKE_REPOS="acme/support, acme/*"
```

If the variable is set, `list_issues`/`list_repos`, the webhook intake and the
`nur-wenn:` advance check deliver only hits from these repositories. Entries are
full names (`owner/name`) or a whole owner (`owner/*`), case-insensitive.
Empty/unset → no restriction. The primary filter nevertheless remains the
GitHub-side setup: give the bot account access only to the target repositories,
and prefer a fine-grained token that names them.

---

## 4. No internal comments, no attachment uploads

Two things the GitLab plugin can and this one cannot — both because GitHub does
not offer them, not because they were left out:

- **Every comment is public.** GitHub has no internal notes. Whatever the agent
  writes is visible to everyone who can see the repository. There is no
  `internal` parameter, and consequently no `github:comment_internal` /
  `github:comment_external` split in the guard-rail subjects — the subject is
  simply `github:comment`. Write the playbook accordingly: no working notes to
  self, no internal assessments of people.
- **The agent cannot attach a screenshot.** GitHub's attachment upload is a web
  UI feature with no API behind it. `download_attachment` fetches an image *out*
  of an issue into the sandbox so the agent can look at it (vision); the reverse
  does not exist. An agent that wants to show something describes it, or commits
  an image into the branch.

`download_attachment` only accepts GitHub's own attachment addresses
(`github.com/user-attachments/…`, `*.githubusercontent.com`). That is the point
of the action: the URL comes out of an issue body, that is out of text a
stranger wrote. Without the host check the action would be a request forgery
primitive — "download this", pointed at an internal address, carried out by the
daemon with a valid token.

---

## 5. Code-based answers: `checkout`

The agent should not answer bug reports "from memory" but check the claim
**against the source**:

```
checkout {"repo":"acme/support", "ref":"main"}    # ref optional, default: the default branch
```

Sequence and security model:

- The **daemon** (not the runtime) downloads the repository archive through the
  API (`GET /repos/:owner/:repo/tarball/:ref`) with the brokered token — the
  token stays in the daemon's RAM and **never** lands in the sandbox's file
  system (unlike a `git clone` with a credential remote, which would persist the
  token in `.git/config`).
- It is unpacked into `<home>/repos/<owner>-<repo>-<ref>/`. GitHub's archive
  carries a top level named `<owner>-<repo>-<sha>`, which changes with every
  commit; the plugin strips it, and only that makes the destination directory
  stable — the precondition for dependency caches (`node_modules`, `.venv`,
  `vendor`, …) surviving between runs.
- The directory is initialised as a **git repository** with the upstream state
  as the baseline commit, tagged `covey-baseline`. A sub-agent working in the
  checkout may commit locally; the change is still reported as a difference to
  that tag.
- Protections: path traversal is refused, symlinks are skipped, and the unpacked
  size is limited (default 512 MB, `COVEY_GITHUB_CHECKOUT_MAX_MB`).
- The home is persistent, so working copies would pile up in it without bound.
  After every checkout the least recently used ones fall away; five survive
  (`COVEY_CHECKOUT_KEEP`, `0` switches the cleanup off). Which ones went is in
  the checkout result, because the agent may be holding a path from an earlier
  run. How full the sandbox is is on the agent's **Workplace** tab.

**Large repos:** GitHub's archive endpoint, unlike GitLab's, **cannot** be
narrowed to a subdirectory. If a repo blows the limit, work without a checkout:
`list_tree` navigates and `read_file` reads selectively. The error message says
so.

---

## 6. The developer loop: `commit` + `create_pull_request`

`commit` pushes the locally edited files as **one** commit:

```
commit {"repo":"acme/support","branch":"fix/issue-42-login","message":"…",
        "checkout_path":"<the path from the checkout result>",
        "files":["internal/auth.go"],"deleted":["old.go"]}
```

It runs over GitHub's **Git Data API** — blobs, one tree, one commit, one ref
move. The alternative (the contents API) writes one file per commit, so a change
across five files would arrive as five commits and every intermediate state
would be a broken tree in the history.

Two guarantees the action enforces itself, not by prompt discipline:

- **A direct commit onto the default branch is refused.** The route into the
  main branch leads exclusively through a pull request.
- **No force push.** If the branch has moved on since the checkout, the action
  fails with a message telling the agent to fetch the branch afresh — instead of
  erasing somebody else's work.

`create_pull_request` opens the PR and enters assignee and reviewer (two
separate endpoints on GitHub — the PR exists once the first call succeeds, so a
failure on the second is reported without undoing it):

- The **assignee** is the *reporter* of the underlying issue — whoever wrote the
  need down decides on the merge. Passing `issue_number` lets Covey look them up
  itself. Entering the manager across the board makes them the bottleneck for
  work they never asked for.
- The **reviewer** is the QA/test agent from the team directory, if there is
  one. GitHub refuses to have an author review their own PR, so an assignee
  identical to the author is not entered as reviewer either.

The reviewer's side has one thing GitLab does not: **`request_changes`**. A
comment alone does not block a merge; `request_changes` does, wherever the
branch protection demands a review. Use it when real defects were found —
`approve_pr` for the green signal, and the merging itself always stays with a
human.

### 6.1 CI: GitHub Actions

`list_workflow_runs` after every push, and on a red run diagnose it rather than
guess: `list_run_jobs` → `get_job_log` (the **end** of the log, capped at 48 KB
— failures stand at the bottom, setup noise at the top) → fix → commit → check
again. `rerun_failed_jobs` restarts the failed jobs when the cause lay outside
the change and has been fixed since.

`get_pull_request` returns the merge state **and** the checks on the head
commit: `mergeable` says nothing about whether the tests are green, so the agent
has to read both.

> **Egress:** Actions hands job logs out as a redirect to Azure blob storage.
> The built-in **GitHub** egress template covers it
> (`*.blob.core.windows.net`); without that host `get_job_log` fails at the
> redirect and the agent cannot diagnose a red run.

---

## 7. Env reference (GitHub-relevant)

| Variable | Default | Meaning |
|---|---|---|
| `COVEY_GITHUB_INTAKE_REPOS` | *(empty = all)* | An allowlist of repositories (`owner/name` or `owner/*`) — filters `list_issues`/`list_repos`, the webhook intake and the `nur-wenn:` advance check |
| `COVEY_GITHUB_BOT_LOGINS` | *(empty)* | The agent's own GitHub login(s) — their contributions do not wake it (webhook intake, section 2.4b) |
| `COVEY_GITHUB_CHECKOUT_MAX_MB` | `512` | The upper bound on the unpacked size of a `checkout` (section 5) |
| `COVEY_PUBLIC_URL` | *(empty)* | The externally reachable base URL — needed for the webhook route only |
| `COVEY_EGRESS_ALLOW` | *(empty)* | Additional permitted egress hosts |

The general variables (egress, the daemon token TTL, …) are in
[`ops-zammad.md`](ops-zammad.md), section 6.

---

## 8. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Every action fails with `HTTP 401` | The token has expired or was revoked | Renew it (2.1) and replace it under Secrets |
| `HTTP 403 (rate limit exhausted …)` | The API budget is used up | Widen the heartbeat interval; the message names the reset time |
| `HTTP 403` **without** the rate limit note | A permission is missing from the fine-grained token | Compare against the table in 2.1 |
| `HTTP 404` on a repository that exists | The bot account is not a member, or the fine-grained token does not name the repository | Add it on the GitHub side |
| The agent wakes every interval on the same issue | It works without commenting | See 3.1 — whoever works, comments |
| The agent wakes on its own comment | The bot login is not named | Set `COVEY_GITHUB_BOT_LOGINS` (2.4b) |
| The webhook arrives, nothing happens | The secret does not match, or the event is `CorrelateOnly` and nobody is waiting | Check the signature; see section 1 for which events create tasks |
| `assign` reports success, nobody is assigned | GitHub silently swallows an unknown assignee | The plugin checks the login beforehand — if it still happens, the account has no repository access |
| `get_job_log` fails at the redirect | The egress allowlist lacks the blob storage host | Import the built-in **GitHub** template (6.1) |
