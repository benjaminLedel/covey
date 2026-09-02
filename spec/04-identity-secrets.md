# 04 — Identity & secrets

The hardest and at the same time most defensible part of the platform — and it sits exactly in the existing core competence (Keycloak/OAuth from the Girona setup).

## Basic rule

**Never bake long-lived secrets into the sandbox.** An agent holds no passwords or API keys for target systems. Instead it presents its identity and receives a **short-lived, scoped access token** for the respective target system at runtime.

## Components

- **Keycloak** as the agent identity provider. Every agent has a machine identity (client/service account) in the realm.
- **Secrets broker** as the intermediary between agent identity and target-system access. It uses a secret store (Vault or Infisical) for the long-term credentials/client secrets behind it, which **never** go to the agent.

> **Two identity layers.** This document covers the **agent identity**. Separate from it (but manageable in the same IdP) stands the **human identity**: humans sign in via **SSO (SAML/OIDC)**, and their RBAC hangs off that. Details in [`09-enterprise-model.md`](09-enterprise-model.md). When an agent acts on a human's behalf, the delegation chain (human → agent → target system) is preserved in the audit.

> **Pluggable — built-in as the default.** Broker and IdP are swappable behind narrow interfaces (`IdentityProvider`, `SecretStore`). The MVP ships a **simple, DB-backed built-in implementation** (signed JWTs, AES-GCM-encrypted secrets in Postgres); whoever has Keycloak or Vault configures the external provider instead. Keycloak and Vault are therefore **optional, not prerequisites**. The one limit: real RFC 8693 token exchange against third-party systems only runs through the external `oidc` provider, not through the built-in variant. Details in [`10-architecture-stack.md`](10-architecture-stack.md).

## Human credentials: session and API key

The human identity has two badges, and the second one exists because the first
one is deliberately unreachable.

- The **browser session** is a cookie — `HttpOnly`, `SameSite=Strict`, sliding
  while somebody works. Every one of those properties is right for a browser
  and useless outside one: no script can read it, which is the whole point.
- The **API key** is what everything that is not a browser uses: a pipeline, a
  script, the agent skill that creates an agent through the API. Without it,
  operational tooling ends up beside the product instead of in it — which is
  the failure mode this closes.

A key is bound to a **seat**, not merely to an account. Role and organisation
are properties of the membership, an account may hold several, and a credential
that did not name the seat it works from would have an authority nobody can
state. It carries that seat's role and no scope of its own: a scope that exists
only on paper reads like a restriction and enforces nothing.

Two moves stay with the session, and therefore with the password: **minting or
revoking a key, and changing the password**. A credential that goes astray must
not be able to entrench itself — those are exactly the two moves that would let
it. The practical side is in [`../docs/en/operations/api-keys.md`](../docs/en/operations/api-keys.md).

An API key has no business inside a sandbox. An agent talks to the control
plane through the action proxy, which needs no credential at all; a key in a
sandbox would be a long-lived secret in the one place this whole document keeps
them out of.

## Token exchange flow (RFC 8693)

The broker uses **RFC 8693 token exchange** — the same pattern as in the Girona setup (together with RFC 9728 protected resource metadata for resource discovery).

```
1. Agent (daemon) needs access to system Z
        │
        ▼  request_credential(system=Z, scope=…)
2. Control plane / broker checks:
        - is the agent entitled to Z + scope according to ACCESS.md?
        - does an approval policy apply?
        │  yes
        ▼
3. The broker exchanges the agent identity (subject_token) for a
   scoped access token for Z (RFC 8693 token exchange at Keycloak)
        │
        ▼  inject_credentials(token, ttl=short)
4. The daemon uses the token for exactly this access.
   The token expires after a short TTL.
```

Key points:

- The **subject_token** is the agent's identity, not a shared secret.
- The issued token is **limited to system + scope + a short TTL**.
- The broker decides, **never the daemon**. Entitlement is checked centrally (against `ACCESS.md` + policies).
- The target systems' long-term credentials stay in the secret store, invisible to the agent.

## Scope: organisation vs. agent

Secrets in the store have two levels; the broker resolves them per agent:

- **Org-wide secrets** are deposited centrally but only take effect through **explicit assignment** to individual agents (least privilege at the secret level). An org secret without an assignment reaches **no** agent.
- **Agent-owned secrets** hang off exactly one agent and, for the same key, take **precedence** over the assigned org secret — e.g. a target-system account of its own or a per-agent Anthropic credential.

The broker's resolution order: agent-owned secret → explicitly assigned org secret → otherwise refusal (`no secret deposited or assigned`).

Two kinds of target system are exempt from that refusal, and the difference between them matters. A system that runs **entirely inside the sandbox** (the dev toolbox, the browser) never sees a credential at all — the broker grants an empty one. A system whose secret only **raises what it may do** — a public API where a key lifts a rate limit — is usable without one: there the broker resolves best effort and grants either way, so that a missing optional key does not look like a missing mandatory one. Both stay subject to `ACCESS.md`, activation and guard rails; what is waived is the secret, not the entitlement.

## Pools: several values under one key

A key may carry **several values**. The occasion is the runtime credential — whoever holds several Claude subscription seats wants them spread across the agents instead of pushing every agent through one token and running into its limit — but the mechanism sits under *every* key, so several bot accounts for GitLab or GitHub work the same way. The explicit assignment stays at **key level**: which value an agent then gets is decided by the choice, not by the administration, otherwise every added token would mean reworking every assignment.

> **What a pool is *for* differs by what it holds, even though the mechanics are identical.** An LLM credential is **capacity**: fungible, invisible outside, more of it means more throughput. A target-system token is an **identity**: it stands in the audit trail and carries permissions, so more of them means more identities, and rotating between them costs traceability rather than buying anything. The commercial model above LLM credentials — which contracts an organisation holds, which agent works on which one, what each one costs — is described in [`18-runtimes-capacity.md`](18-runtimes-capacity.md). Everything below here is the mechanism both share.

The choice is **sticky**. An agent keeps its value as long as that value is healthy, for two reasons that both cost real money if ignored: the runtime caches the prompt prefix per credential, so a value swapped at every wake throws the cache away each time; and in a target system the token *is* the visible identity, so rotating it makes the audit trail unreadable. The seat is therefore stored (`secret_bindings`) and not computed from a hash of the agent id — a computed seat would reshuffle every agent the moment a value is added, and every cache would go cold as a side effect of an addition that should have hurt nobody.

An agent moves only for a reason, and the reason is recorded:

- **soft** — the value has used up its **limit** in the current rolling window. Two units, because "consumption" differs by credential: for an API key it is money (real billing), for a subscription token money is notional and tokens are the closer proxy for the provider's own window.
- **hard** — the target system **rejected** the credential (rate limit, expired, revoked). That beats every estimate the limit makes.

Both park the value; the agent then takes the least loaded healthy one (at equal load: the one fewest agents sit on) and keeps its previous seat as its **home**, so it returns there once that value is healthy again instead of the pool redistributing at every choice. Is **no** value usable, the broker refuses with the moment the pool frees up again, and the control plane **postpones** the wake rather than starting a run that cannot work — a rate limit thereby becomes a delay and not a failed task.

> Least-loaded is the right rule for a pool of **like** values, which is what a set of subscription seats is. It is the wrong one as soon as a pool mixes paid-for quota with metered capacity, because the two pull in opposite directions — see the merit order in [`18-runtimes-capacity.md`](18-runtimes-capacity.md).

> **Where the line runs.** Of all of this, only **one** thing is really a property of a secret: that a key can carry several values. That is a storage statement, and it belongs here, next to the encryption, the AAD and the sensitivity rule. Choosing among the values — stickiness, cooldown, limits — is capacity policy and belongs to the runtime ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)). It currently sits here because that layer does not exist yet, and the seams are already visible: the selection has to be handed a usage function because this store does not own the data its own decision needs, and its cooldown is triggered by an LLM API error, of which a secret store should know nothing. The precedence rule and the assignment check stay here in any case — those *are* secret concerns.

For the limit to be measurable at all, consumption is booked **against the value** it ran on (`cost_entries.secret_key/secret_slot`). Without that attribution there is no per-value limit, no utilisation, and no answer to whether one seat is too few or one too many. Where the engine can report the provider's own utilisation figure instead, that beats the platform's estimate; both, and the difference between them, are covered in [`18-runtimes-capacity.md`](18-runtimes-capacity.md).

An agent-owned secret takes precedence over the pool as before; such an agent does not take part in the distribution.

 By default a secret is a simple **variable** (server name, URL) and readable through the API. Values marked **sensitive** (tokens, passwords) are write-only with a prefix preview — the marking is deliberately one-way (lifting the protection would mean disclosing the value after all; the way back is deletion and recreation). Both apply at both levels; in the built-in implementation the AES-GCM AAD additionally binds the ciphertext to org, agent and key.

## The life of a value

A target-system token has a lifetime the store did not use to know about. Atlassian ends every Cloud API token after a year at most; a Data Center PAT ends after what the instance allows; GitLab and GitHub tokens carry a date when their owner set one. The first sign used to be a 401 inside a recording, filed under the action that hit it — which reads as a permission problem three weeks later, to somebody who has to work out that it is not (#176).

The store now keeps **what the platform learns about a value beside the value**: when the system will stop accepting it (`expires_at`), whether it already has (`rejected_at` with the reason), what the last connection test saw (`probed_at`, the identity, the error), the system's own id for it, and whether the plugin can mint its successor. These are storage *about* the value, not a policy about choosing between values — that distinction is the one drawn under *Pools* above, and it still holds. Every field is cleared when the value is overwritten: a new value is a new credential, and the state of the old one would be a lie beside it.

Two signals write there:

- **The hard one — a run was refused.** The daemon reads a plugin's action error for the one statement worth passing on, *the target system refused the credential itself*: the plugin says so in the error's type (`target.CredentialRejectedError`, a 401 — never a 403, which is a permission), or, for a plugin that only formats what it saw, the text does. The action event carries the flag, the control plane marks the secret the run was given (the agent's own before the assigned org-wide one, as the broker chose it), and the daemon forgets its cached copy so the next action asks again instead of walking into the same wall for the rest of the TTL. This is the counterpart of the runtime seat's rejection under *Pools*: the same signal, on the other kind of credential.
- **The soft one — a daily probe.** The connection test the setup wizard runs once (`Prober`) runs for every stored token once a day, without anybody pressing. Where the plugin implements `CredentialInspector`, the probe also learns the expiry and the id; where it does not, it learns whether the value works. A probe that works clears a rejection — a credential that works is not a rejected one — and a probe that fails for another reason (no route, a 502) does not unsay one that stands.

What the store knows is then **acted on, in this order**:

1. A value that can mint its successor (`Rotator`; a Data Center PAT creates a PAT) and is inside a month of its date is **rotated**: mint, verify the successor, store it, and only then revoke the old one by the id kept from the probe. Revoke last — a rotation that revoked first and then failed to store would leave the agent with nothing. The successor takes the old value's place under the same key and slot, so assignments and the agent's seat are untouched; the rotation stands in the recording as a credential event.
2. A value that is refused, has run out, or is inside a fortnight of its date and cannot be rotated — Cloud tokens, which Atlassian offers no API to renew — is **reported**: a lint finding on the agent (`credential-rejected`, `credential-expired`, `credential-expiring`; [`02-agent-model.md`](02-agent-model.md)), and a notification of the `ops` class to the people who can replace it, **once** — the store remembers that they were told, and forgets it when the state changes. A finding here ends the way it is fixed: with a new value under the same key, or a rotation that worked.

Where the system does not state the date — Jira Cloud states nothing about its tokens — a person enters it on the secret, and the platform warns from that. The plugin's word wins over the entered date where both exist, because the system is the one that will act on it.

## Threat model

Agents with real access are an attack surface. This has to be thought through from the start, otherwise you build a wonderful exfiltration machine.

### Main threat: prompt injection → data exfiltration through legitimate access

A support agent has legitimate access to tickets + Confluence + Teams. If it is prompt-injected through **prepared input** (e.g. a malicious ticket, a manipulated mail), it can be made to siphon data out through its *legitimate* channels or to carry out harmful actions. That is a genuine security incident, not an edge case.

### Countermeasures

The guiding principle behind all countermeasures: **do not rely on the agent behaving well.** Since it is precisely the agent's reasoning that may be compromised, the protective boundaries have to take effect *outside* the runtime — as central, platform-enforced **guard rails** (see [`06-observability-control.md`](06-observability-control.md)). What is written as a "limit" in `SOUL.md` is not sufficient for that; it is behaviour steering, not a security boundary.

1. **Central guard rails (platform-enforced).** Forbidden systems/tools, egress rules, forbidden actions and mandatory approvals are enforced at the broker, at the egress and in the tool layer — not circumventable by the agent. Fail-closed.
2. **Least privilege / narrow scopes.** An agent gets only the minimum necessary rights, with the narrowest possible scope and a short TTL. No permanent full access.
3. **Approval gates for risky actions.** Outbound external mail, deletion, bulk operations need approval (see [`06-observability-control.md`](06-observability-control.md)).
4. **Egress control.** The agent's outbound channels (above all outward communication) are monitored and gated where necessary — not every recipient is allowed.
5. **Separation of data and instruction.** Incoming content (ticket text, mail body) is presented to the runtime as *data*, not as instruction; adapter and prompt design have to keep that up.
6. **Complete recording + supervisor.** Every action is traceable; a supervisor agent flags anomalous behaviour (unusual access patterns, atypical recipients).
7. **Kill switch.** An agent running out of control can be stopped immediately — individually or fleet-wide.

### Further vectors (short list)

- **Token leakage** out of the sandbox → limited by short TTL + scope.
- **Privilege creep** over time → periodic access review against `ACCESS.md`.
- **Compromised sandbox** → isolation + ephemeral compute limit the blast radius; critical state does not live in the sandbox.
- **Agent-to-agent abuse** (an injected agent maliciously delegates to others) → inter-agent messages are subject to the same recording/policy rules.

## Defensibility

Secure agent identity with token exchange is a hard problem most platforms do not solve cleanly — and it sits exactly where deep experience already exists (Keycloak, RFC 8693, RFC 9728). Together with the knowledge-graph memory (see [`05-memory.md`](05-memory.md)) these are the two building blocks that make the platform defensible.
