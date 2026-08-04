# 04 — Identity & secrets

The hardest and at the same time most defensible part of the platform — and it sits exactly in the existing core competence (Keycloak/OAuth from the Girona setup).

## Basic rule

**Never bake long-lived secrets into the sandbox.** An agent holds no passwords or API keys for target systems. Instead it presents its identity and receives a **short-lived, scoped access token** for the respective target system at runtime.

## Components

- **Keycloak** as the agent identity provider. Every agent has a machine identity (client/service account) in the realm.
- **Secrets broker** as the intermediary between agent identity and target-system access. It uses a secret store (Vault or Infisical) for the long-term credentials/client secrets behind it, which **never** go to the agent.

> **Two identity layers.** This document covers the **agent identity**. Separate from it (but manageable in the same IdP) stands the **human identity**: humans sign in via **SSO (SAML/OIDC)**, and their RBAC hangs off that. Details in [`09-enterprise-model.md`](09-enterprise-model.md). When an agent acts on a human's behalf, the delegation chain (human → agent → target system) is preserved in the audit.

> **Pluggable — built-in as the default.** Broker and IdP are swappable behind narrow interfaces (`IdentityProvider`, `SecretStore`). The MVP ships a **simple, DB-backed built-in implementation** (signed JWTs, AES-GCM-encrypted secrets in Postgres); whoever has Keycloak or Vault configures the external provider instead. Keycloak and Vault are therefore **optional, not prerequisites**. The one limit: real RFC 8693 token exchange against third-party systems only runs through the external `oidc` provider, not through the built-in variant. Details in [`10-architecture-stack.md`](10-architecture-stack.md).

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

The broker's resolution order: agent-owned secret → explicitly assigned org secret → otherwise refusal (`no secret deposited or assigned`). By default a secret is a simple **variable** (server name, URL) and readable through the API. Values marked **sensitive** (tokens, passwords) are write-only with a prefix preview — the marking is deliberately one-way (lifting the protection would mean disclosing the value after all; the way back is deletion and recreation). Both apply at both levels; in the built-in implementation the AES-GCM AAD additionally binds the ciphertext to org, agent and key.

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
