# Example agents (bundles)

Importable agent configurations (`kind: covey.agent-config`). Import through
`POST /api/v1/agents/import` or in the UI ("Agent from bundle").

These bundles are at the same time the **bundled template library**:
`builtin.go` pulls them into the binary via `//go:embed`, so that they appear in
the UI under **Templates** (read-only, across organisations) and can be
instantiated there directly — without importing anything first. A new example
bundle becomes a template by putting it here and entering it in the `manifest`
in `builtin.go`.

**Every bundle exists in two languages.** `<name>.bundle.json` is the English
version (the base language), `<name>.de.bundle.json` the German one. Which one
you get on instantiating depends on the language you are reading the library in —
an English display name above a German `SOUL.md` would be the worse half of
both. Whoever adds a bundle may start in English alone; the German version is
optional.

| Bundle | Slug | Role |
|---|---|---|
| `improvement-engineer.bundle.json` | `covey-doctor` | Covey Doctor, the operations engineer — the name and the slug are fixed by the platform and cannot be renamed. It reads a colleague's **work record** (`covey/work_record`), tell apart the three causes an agent underdelivers for — its configuration, its assignment, or the platform underneath it — and propose a config change as a stored, inactive version a human accepts (`covey/propose_agent_config`). Weekly, one task per cycle. |
| `coding-agent.bundle.json` | `covey-dev` | Developer: pick up issues **assigned to it** (`nur-wenn: gitlab:issues:assigned`), verify bugs against the code, have fixes developed, open merge requests and live the review loop. |
| `qa-agent.bundle.json` | `covey-qa` | QA/test: accept others' merge requests end to end as the reviewer — set the project up once per project and keep it, operate the application in the browser, support states and defects with screenshots, run the test suite as a job that outlives the run, and close a green acceptance with `approve_mr` + `merge_mr`. |
| `delivery-lead.bundle.json` | `covey-lead` | Delivery lead: drive a GitLab milestone to its deadline — make tickets implementable (acceptance criteria, affected code locations), keep dependent tickets in order, dispatch work to the developers within a WIP limit, report the state, escalate open subject-matter questions to the human. |
| `log-triage-agent.bundle.json` | `covey-logtriage` | Log triage: analyse logs reported by email, check for duplicates before filing (`list_issues search=…`, bundle occurrences onto the existing ticket), file tickets for relevant findings and hand real code bugs to a developer agent by `assignee`. |
| `web-researcher.bundle.json` | `covey-webresearch` | Web researcher: research questions on the open web with a real browser, capture evidence as screenshots and deliver a concise, sourced answer. |
| `dependency-security-agent.bundle.json` | `covey-depsec` | Dependency security: scan the lock files of the projects in its register (`vulndb scan_lockfile`), assess every hit against the project — direct or transitive, which fix branch applies — and file traceable GitLab tickets with evidence after a mandatory duplicate check; hand the upgrade to a developer agent. |

Together they form the two-agent setup from
[`docs/en/integrations/gitlab.md`](../docs/en/integrations/gitlab.md) §2.7: the developer agent enters
the QA agent as the `reviewer` of its MRs (it finds them automatically in the
prompt section "Team (AI colleagues)"), the QA agent tests and comments, and the
developer works the feedback in through its `gitlab:mr` loop.

## A whole engagement instead of individual tickets

The **delivery lead** starts one level above. It does not take in tickets but a
milestone: it reads the requirement in the original, writes checkable acceptance
criteria into the ticket, holds dependent tickets back until their foundation is
merged, and only then gives work to the developer colleagues — never more at a
time than the WIP limit allows.

The reason for the separate role is not the distribution — GitLab carries that
itself as soon as the developers only work on their assignments. It is the two
things individually working agents structurally cannot do: turn a requirement
into an implementable ticket, and prevent several colleagues from building the
same foundation at the same time.

For that to hold, the developer agent is cut to
`nur-wenn: gitlab:issues:assigned`: **it works exclusively on issues assigned to
it.** That is the seam between the two templates — if it reached for every open
issue as before, the lead's preparation comment alone would trigger it, and the
WIP limit and the order would be ineffective. The price: a developer agent
**without** a lead needs an assignment to get going — either from a human or by
`assign` from another agent. Whoever wants to use the template solo with a free
intake changes the first heartbeat line back to `nur-wenn: gitlab:issues` and
playbook step 0 accordingly.

If the developer finds a preparation in the ticket (the sections "Requirement",
"Acceptance criteria", "Affected", "Not part of this"), that counts as its
assignment — not the ticket title.

So that the same agent can drive the next engagement, nothing
engagement-specific sits in its config — project, milestone, deadline, order and
WIP limit sit in a wiki page, the engagement brief. The template and the
reasoning behind the fields: [`docs/en/integrations/gitlab.md`](../docs/en/integrations/gitlab.md)
§2.9.1. **One lead drives exactly one engagement**; for a second one a second
lead is created (the same config, its own brief), because its heartbeats name no
milestone and it could not tell two briefs apart.

**A bundle carries only the config files**, no master data. Still necessary
after the import (see `docs/en/integrations/gitlab.md` 2.2, 2.7 and — for the lead — 2.9):

- Assign the secrets `gitlab_token` + `gitlab_url`, enable the GitLab and `dev`
  target systems. The developer, QA and lead agents each need **their own
  GitLab bot user** (separate tokens) — not only so that the review loop
  distinguishes "author" from "reviewer": the wake logic decides on it whether
  the last comment came from its own bot. With a shared token two agents switch
  each other's alarm clock off.
- Enter the **GitLab identifier** (`gitlab: covey-dev` or `covey-qa`,
  `covey-lead`) and the **responsibility** in every agent's profile.
- Assign all the agents to **the same department** — then the developer sees the
  QA agent as `YOUR TEAM` and prefers them as the reviewer, and the lead finds
  its developers.
- **The QA agent takes in no bug reports** — it accepts merge requests and
  nothing else. Reports from outside belong to an intake agent of its own
  (`log-triage-agent.bundle.json` as the pattern, or one with an email access);
  the QA agent finds it through the team directory. The separation is
  deliberate: intake fires every few minutes, an acceptance runs for a quarter
  of an hour with a checked-out project — in one agent the cheap job keeps
  interrupting the expensive one.
- **The QA agent needs a warm sandbox** (it is set in the bundle). It keeps one
  working tree per project including `node_modules`/`vendor`; without it every
  acceptance starts with a cold setup and the run is over before the test suite
  has anything to say.
- **If the QA agent is to merge itself**, its GitLab bot user needs at least
  `developer` (better `maintainer`) on the target projects and the tool
  `merge_mr`. The bundle carries it; without it the approval is its last word.

Additionally for the **delivery lead** only:

- Create a **report ticket** in the milestone, assign it to the lead and leave
  it permanently open — that is where it writes the daily state. Its IID (with
  the project ID) belongs in the brief.
- Enter a **human manager** **whose GitLab identifier has to be deposited in
  their profile**. Without it, `assign` fails on every open subject-matter
  question — that is, in exactly the path that involves humans.
- Create the wiki page with the brief (template: `docs/en/integrations/gitlab.md`
  §2.9.1) before the first heartbeat fires.

Additionally for the **dependency security agent** only:

- Enable the **`vulndb` target system**. It needs no secret — all four sources
  (OSV.dev, the GitHub Advisory Database, NVD, the package registries) are
  publicly reachable. Only NVD limits noticeably (5 requests per 30 seconds
  anonymously, 50 with a key); whoever scans many projects requests a key at
  <https://nvd.nist.gov/developers/request-an-api-key> and assigns it as
  `vulndb_token`.
- Import the **egress template "Vulnerability databases"** from the built-in
  catalogue and assign it to the agent. The vulndb actions run in the sandbox
  and therefore go through the egress proxy — without the hosts every action
  fails. The template is deliberately not part of the bundle: egress is a
  security-role matter, and a bundle carrying it could only be instantiated by
  `platform_admin`/`security` instead of by `manage`.
- Create the wiki page **"Dependency scan register"** before the first heartbeat
  fires — project ID, branch and expected lock files per watched project. Which
  projects are scanned is not in the config on purpose: it changes far more
  often than the agent's behaviour. Without the page the agent proposes a
  register instead of scanning into the blue.
- Give it a **developer agent in the same department**. It hands upgrades over
  by `assign`; without a developer colleague every finding lands with the human
  manager.

Its intake needs no bot user of its own for the wake logic (its scan runs on a
fixed schedule, `täglich: 06:00`, not on a `nur-wenn` edge) — but a separate
GitLab token is still the better choice, so that the security tickets are
attributable and the second heartbeat (`nur-wenn: gitlab:issues:assigned`) only
sees what really belongs to it.

Additionally for the **Covey Doctor** only:

- Its `ACCESS.md` carries one line, `- system: covey scope: agents:review`, and
  that is everything it needs to read colleagues and propose configurations.
  Nothing else has to be set up for the review cycle.
- **Reading the platform's own source is two settings, and both are needed.**
  Under *Organisation → Source of this platform* enter the target system and the
  project Covey itself lives in — and add **that same system to the agent's
  `ACCESS.md`**, scoped to reading the code and filing issues. The master datum
  alone is half the setup: without the access line the section stays out of the
  agent's prompt entirely, because an agent that reads it may check out and then
  runs into the broker's refusal.
- The system is deliberately not in the bundle: an instance on GitLab and one on
  GitHub need different lines, and a bundle that guessed would be wrong for half
  its readers.
