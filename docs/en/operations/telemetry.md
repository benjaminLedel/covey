---
slug: telemetry
title: Telemetry, and the channel to the project
description: 'What a covey installation sends to the project once a day, what it never sends, and how to switch it off — plus the channel that carries a platform fault to the tracker.'
faq:
  - q: Is telemetry on by default?
    a: 'Yes. One setting turns it off (Platform → Settings → telemetry.mode = off), an empty telemetry.url does the same, and COVEY_TELEMETRY=off in the environment wins over both — that one works before the first start.'
  - q: What exactly is sent?
    a: 'Counts and version strings: how many organisations, humans, agents, how many tasks ran in the last day and how they ended, which runtimes and which target systems are switched on, whether a runner is connected. Never a task title, an agent slug, a prompt, a result, an address or an organisation name.'
  - q: Can somebody tell who my installation is?
    a: 'Not from the data. The identity is a UUID the installation generated for itself; it ties yesterday''s counts to today''s and carries no name, no domain and no address. Clearing the row makes it a new installation.'
  - q: Do bug reports from my agents go out automatically?
    a: 'No. Only what covey Doctor deliberately files with covey/create_issue, and only when the destination is the project''s own repository. An organisation that files into its own GitLab never goes through this channel at all.'
---

covey talks to the project it comes from in two places, and both are the same
channel with the same switch in front of them.

## 1. Telemetry: a handful of counts, once a day

**On by default**, and that is a decision rather than an oversight: without it
the project knows nothing about how covey is actually run — which version
installations are on, whether a release is being adopted, how many agents a
typical instance holds — and every answer to that becomes a guess.

**What goes out**, in full:

| | |
|---|---|
| `version`, `commit` | which build this is |
| `orgs`, `humans`, `agents`, `agents_hired` | how many of each exist |
| `tasks_24h`, `tasks_done_24h`, `tasks_failed_24h`, `tasks_blocked` | how much ran in the last day, and how it ended |
| `runtimes` | which engines the agents use, and how many of each |
| `targets` | which target systems are switched on, by name |
| `runners` | how many runners are registered |

**What never goes out:** a task title, an agent slug, a prompt, a result, a
mail address, a repository, a URL, an organisation name, a customer name.
Nothing an agent produced and nothing anybody typed. The assembly is one short
function — `internal/telemetry`, `Zahlen` — and the test beside it
(`TestTelemetrieSchicktNurZahlen`) takes a real task title and a real agent slug
out of a running instance and fails if either appears in what is sent.

**The identity** is a UUID the installation generates for itself on first use
(`telemetry.id`). It exists so that yesterday's counts and today's are the same
installation. It carries no name and no address.

### Switching it off

Three ways, and each of them means *nothing leaves the machine*:

```bash
# 1. The setting, under Platform → Settings
telemetry.mode = off

# 2. No destination
telemetry.url = (empty)

# 3. The environment — this one works before the first start
COVEY_TELEMETRY=off
```

## 2. The channel for a platform fault

covey Doctor's job includes the finding that no configuration fixes: three
agents died at the turn limit this week and none of them was misconfigured. That
is a bug report, and it belongs in the tracker of the repository covey is
maintained in.

Two ways lead there, and the platform picks whichever is available:

- **Your own account.** Store the target system's token as an organisation
  secret (`github_token`, `gitlab_token`, …) and set *Organisation → This
  platform's source*. The control plane files under that account. Nothing
  leaves the installation except the report the agent wrote.
- **The project's channel**, when the destination is the project's own
  repository and no account of your own is stored. The report goes to
  covey.work, where a person reads it before it becomes an issue —
  installations the project knows are released automatically, everything else
  waits. Nothing reaches a public tracker unread.

Either way the agent never sees a credential, the destination is master data
rather than a parameter, and the same report is recorded in your own inbox so
that you see what went out.

An organisation that files into its own GitLab (its own target system, its own
project) never uses the project's channel: its findings stay in the house, which
is what that setting is for.

### What is in such a report

What covey Doctor wrote: a title, and a body with its evidence — which agents,
which runs, what it cost, and what it found in the source where it may read it.
It is a text an agent composed about the platform. If your instance handles
material that must not leave it, store an account of your own for your own
tracker, or switch the layer off entirely with `-` under *Organisation → This
platform's source*.
