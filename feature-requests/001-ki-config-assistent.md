# FR-001 — An AI assistant for adapting agents (the "config copilot")

Status: **Implemented** · As of: 2026-07-24

> Feature requests are proposals, not yet settled spec. If a request is accepted,
> its content moves into the responsible `spec/` document and this document is
> set to *accepted* / *rejected*.

> **Implemented** in the control plane (`internal/httpapi/assist.go`) and the web UI
> (`web/src/pages/Agent.tsx`, the component `ConfigAssistant`). Endpoints:
> `GET /api/v1/assist/status` (gating) and
> `POST /api/v1/agents/{id}/config/assist` (the dialogue). The assistant uses the
> org-wide Claude credential server-side, proposes config files as a diff
> and saves nothing itself.

## In short

As soon as an **org-wide Claude credential** is deposited in the organisation
(`anthropic_api_key` or `claude_code_oauth_token` — the same credentials that
already feed the agent runtime), the control plane offers an **AI assistant
for adapting agents**: a config copilot directly in the agent editor that
writes and reworks the config files (`SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`,
`ORG.md`, `HEARTBEAT.md`) in dialogue — instead of a human formulating the
Markdown files by hand out of nothing.

## Motivation

An agent behaves only as well as its config. Today an agent owner has to write
`SOUL.md` & co. by hand: an empty text field, no template in mind, no
feedback on whether the phrasing carries, until the agent runs for the first time. That is
the biggest friction when onboarding a new agent and the most common reason for
bad behaviour (a vague role, missing playbooks, contradictory instructions).

The platform already has the tool to solve that in the house: the org-wide
Claude credential. It is there anyway (otherwise no agent would run) and can be
reused server-side for a meta assistant that helps with formulating the
config — an LLM supporting the human in configuring the other LLMs.

## Trigger condition (gating)

The assistant appears **only** when the credential is resolvable org-wide
(the `SecretStore` delivers `anthropic_api_key` or `claude_code_oauth_token` for the
org). If it is missing, the function is not offered in the UI at all — there would be no LLM
to carry it. The feature is thereby strictly coupled to a capability that is already
present and causes neither dead UI nor cost without it.

The credential is used **exclusively server-side** by the control plane for the
assistant call and never passed to the browser or into a sandbox
(design principle 6 — *never put long-lived secrets in the sandbox*). The assistant runs
in the control plane, not in an agent sandbox.

## What the assistant does

A chat panel next to the config editor on the agent page. The human describes
in prose what the agent should do or what is wrong with its behaviour; the assistant
answers with **concrete proposed changes to the config files**:

- **Create anew:** "A support agent for invoice questions, answers in German,
  escalates to the accounting team" → a draft for `SOUL.md`, `CAPABILITIES.md`,
  `PLAYBOOKS.md`.
- **Rework:** "It is too chatty and forgets to close tickets" →
  targeted diffs on the affected sections.
- **Explain:** "Why does the agent not escalate here?" → it reads the current config
  and shows the gap instead of writing blindly.

While formulating, the assistant knows the **platform context**: the
Covey platform protocol (`agents.ProtocolInstructions`), the target systems connected
to the agent including their available actions, the guard rails in force and
the org embedding. That way it only proposes behaviour the platform actually permits
(it invents no action the action proxy does not offer).

## Embedding into the existing architecture

- **UI docking point:** `web/src/pages/Agent.tsx` — the config editor that today
  holds `files` (`SOUL.md`, `HEARTBEAT.md`, …) and saves via `PUT /agents/{id}/config`.
  The assistant delivers proposals into the same `files` draft; the human
  reviews them as a diff and saves deliberately.
- **Backend docking point:** a new control-plane endpoint (e.g.
  `POST /api/v1/agents/{id}/config/assist`) that resolves the org-wide Claude key through the
  `SecretStore`, assembles the context (the current `files`, target-system manifests,
  guard rails, `ProtocolInstructions`) and asks Claude.
- **Config as code is preserved (design principle 5):** the assistant **commits
  nothing itself**. It produces proposals; they only take effect through the regular,
  humanly accountable saving of the config — with the existing diff/version
  mechanism. The assistant does not replace the review, it accelerates the
  draft.
- **RBAC:** the same roles that may edit the agent config today
  (agent owner) may use the assistant. No new permission needed.

## Non-goals / delimitation

- **No autonomy over the config.** The assistant writes no config live,
  triggers no deployments and changes no guard rails or secrets.
- **No second runtime path.** It uses the existing org credential through a
  simple control-plane call, no sandbox, no daemon.
- **Not a supervisor.** To be distinguished from the supervisor agent in
  [`spec/06-observability-control.md`](../spec/06-observability-control.md), which
  reviews *running* agents. This assistant helps with *configuring*, not with
  monitoring.

## Open questions

- **The cost view:** assistant calls run on the same org credential as the
  agents. Should they be shown separately in cost control
  ([`spec/06-observability-control.md`](../spec/06-observability-control.md))
  (cost centre "platform/tooling" instead of the agent)?
- **Model choice:** a fixed model (e.g. the most current Opus at any time) or derived from the
  runtime configuration?
- **Template coupling:** the relationship to the existing templates
  (`web/src/pages/Templates.tsx`) — does the assistant preferably start from a
  template and refine it, rather than from a blank page?

## Acceptance criteria (definition of done)

1. If no org-wide Claude credential is deposited, the assistant does not appear;
   the agent page behaves unchanged.
2. If one is deposited, an agent owner can have a new `SOUL.md` (plus
   supplementary files) designed in dialogue and take it over as a config draft.
3. Proposals appear as a diff against the current `files` and only take effect through
   deliberate saving — the assistant never commits itself.
4. The Claude credential does not leave the control plane (not into the browser,
   not into a sandbox).
5. Proposed behaviour refers only to genuinely connected target systems/
   actions and respects the guard rails in force.
