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
| `coding-agent.bundle.json` | `covey-dev` | Developer: pick up issues **assigned to it** (`nur-wenn: gitlab:issues:assigned`), verify bugs against the code, have fixes developed, open merge requests and live the review loop. |
| `qa-agent.bundle.json` | `covey-qa` | QA/test: accept others' merge requests end to end as the reviewer and give feedback; additionally take in bug reports by email and file them as GitLab tickets (`create_issue`). |
| `delivery-lead.bundle.json` | `covey-lead` | Delivery lead: drive a GitLab milestone to its deadline — make tickets implementable (acceptance criteria, affected code locations), keep dependent tickets in order, dispatch work to the developers within a WIP limit, report the state, escalate open subject-matter questions to the human. |
| `log-triage-agent.bundle.json` | `covey-logtriage` | Log triage: analyse logs reported by email, check for duplicates before filing (`list_issues search=…`, bundle occurrences onto the existing ticket), file tickets for relevant findings and hand real code bugs to a developer agent by `assignee`. |
| `web-researcher.bundle.json` | `covey-webresearch` | Web researcher: research questions on the open web with a real browser, capture evidence as screenshots and deliver a concise, sourced answer. |

Together they form the two-agent setup from
[`docs/ops-gitlab.md`](../docs/ops-gitlab.md) §2.7: the developer agent enters
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
reasoning behind the fields: [`docs/ops-gitlab.md`](../docs/ops-gitlab.md)
§2.9.1. **One lead drives exactly one engagement**; for a second one a second
lead is created (the same config, its own brief), because its heartbeats name no
milestone and it could not tell two briefs apart.

**A bundle carries only the config files**, no master data. Still necessary
after the import (see `docs/ops-gitlab.md` 2.2, 2.7 and — for the lead — 2.9):

- Assign the secrets `gitlab_token` + `gitlab_url`, enable the GitLab and `dev`
  target systems. The developer, QA and lead agents each need **their own
  GitLab bot user** (separate tokens) — not only so that the review loop
  distinguishes "author" from "reviewer": the wake logic decides on it whether
  the last comment came from its own bot. With a shared token two agents switch
  each other's alarm clock off.
- Enter the **GitLab identifier** (`gitlab: covey-dev` or `covey-qa`,
  `covey-lead`) and the **responsibility** in every agent's profile.
- Assign all the agents to **the same department** — then the developer sees the
  QA agent as `DEIN TEAM` and prefers them as the reviewer, and the lead finds
  its developers.
- **For the QA agent's mail intake** additionally set up a mailbox of its own
  and assign the secrets `email_url` + `email_token` (see
  `docs/ops-email.md`). So that the agent can assign bug reports to the right
  GitLab project, deposit the **product→project mapping** in the QA agent's
  profile (which mailbox/product belongs to which GitLab project); if the
  mapping is unclear it asks the reporter rather than ticketing into the wrong
  project.

Additionally for the **delivery lead** only:

- Create a **report ticket** in the milestone, assign it to the lead and leave
  it permanently open — that is where it writes the daily state. Its IID (with
  the project ID) belongs in the brief.
- Enter a **human manager** **whose GitLab identifier has to be deposited in
  their profile**. Without it, `assign` fails on every open subject-matter
  question — that is, in exactly the path that involves humans.
- Create the wiki page with the brief (template: `docs/ops-gitlab.md`
  §2.9.1) before the first heartbeat fires.
