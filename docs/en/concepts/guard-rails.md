---
slug: guard-rails
title: Guard-rails & control
description: 'Approvals, recording and a kill switch: how Covey enforces boundaries for AI agents outside the runtime — fail-closed, not through the prompt, and traceable for every run.'
faq:
  - q: Can an agent change its own guard-rails?
    a: No. They live in the control plane, not in its configuration, and are enforced outside the runtime. A human with the right role can change them — and that change appears in the audit trail.
  - q: What is the difference between an egress allowlist and an approval?
    a: The allowlist decides which hosts the sandbox may talk to at all — technically, on the network. An approval decides a single business action, such as a reply to a customer. One is a wall, the other is four eyes.
  - q: Does this help against prompt injection from a ticket?
    a: It is precisely the reason for the design. Text from a target system can mislead the model — but it cannot change a rule that lives outside the model. The action then fails at the guard-rail and lands in the recording as a refused attempt.
  - q: How long are recordings kept?
    a: As long as you configure — it is your database. Retention is configurable, and what your audit needs is your decision, not the platform's.
---

# Guard-rails & control

The question that decides whether agents get used in a company is not "can it do this?" but "what happens when it gets it wrong?". Covey answers with three things: enforced boundaries, approvals in the right places, and a recording that holds up afterwards.

## Why not in the prompt

"You must not delete customer data" in the prompt is a request. It helps in the normal case and fails exactly when it matters: on an unusual phrasing, on text from a ticket that reads like an instruction, on a change of model.

Guard-rails therefore sit **outside the runtime**. The agent cannot read them, talk them round or bypass them — all it notices is that an action was refused. In case of doubt it is refused, not let through.

## The three places it applies

- **Secrets broker** — which access, at which scope, for which run. A token that never enters the sandbox cannot be carried out of it.
- **Egress** — where the sandbox may talk at all. With `COVEY_EGRESS_ENFORCE` and hard network isolation, a proxy sits between agent and internet and enforces the allowlist.
- **Action layer** — which action is allowed, which needs an approval, which is forbidden. Rules apply globally, per department or per agent.

## Approvals

Critical actions stop and wait for a human. The defaults after installation show the pattern: an outbound reply to a customer needs an approval, HR systems are off limits, anything called `delete` is hard-denied.

Meanwhile the agent sits in `blocked` — it waits without consuming compute and continues as soon as the approval arrives.

## Recording

Every run is written down: tool calls with arguments, target-system actions, approvals with the person who decided, screenshots for browser work, plus tokens and cost per model. This is the part audit and data protection want to see — and the part that lets you understand a misstep afterwards.

## Kill switch

One switch stops an agent. A second stops the organisation's entire workforce. Both take effect immediately and running sandboxes are torn down. It is the answer to the question that comes up in every security review: "And if we have to stop all of this?"

## Cost as a boundary

A budget per agent is a guard-rail too — the one against the mistake that hurts nobody except the invoice. Once it is used up, the agent stops working.

## Next

- [Identity & secrets](identity-and-secrets.md) — how access is brokered
- [Operations & deployment](../operations/operations.md) — switching on egress isolation
- [Target systems & plugins](../integrations/target-systems.md) — which actions exist in the first place
