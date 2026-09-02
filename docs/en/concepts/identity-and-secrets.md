---
slug: identity-and-secrets
title: Identity & secrets
description: 'Agent identity, RBAC and the secrets broker: access is issued at runtime, short-lived and scoped. Long-lived secrets never enter the sandbox; storage is AES-GCM encrypted.'
faq:
  - q: Can I use HashiCorp Vault instead of the built-in store?
    a: 'The store sits behind a narrow interface drawn for exactly that: builtin (AES-GCM in Postgres) or an external provider. The built-in route is the default so that an installation without Vault is complete.'
  - q: Does the agent see my API key?
    a: 'The runtime is given the model key for the run — without it, it cannot call the model. Access to target systems, by contrast, goes through the action proxy: the agent names the action, the control plane inserts the token.'
  - q: What happens with an expired token?
    a: 'It is checked when deposited and detected in operation: a rejected value is parked, the agent moves to other capacity if there is any, and the recording states the reason.'
---

# Identity & secrets

The most awkward question you can ask any agent installation is: where exactly is the password the agent uses to get into the ticket system? The usual answer — in an environment variable inside the container — is why the security team says no.

## The principle

**Never put long-lived secrets in the sandbox.** Access is brokered at runtime, short-lived and cut to purpose. What the agent gets applies to this run and this system — not beyond it.

## Agent identity

An agent is a subject of its own, not a process under a shared account. It has an identity that carries its access, its position in the org chart and every trace it leaves. In target systems it appears under its own name — traceable right down to the comment column of a ticket.

## People and roles

People with different roles work on the same organisation: platform admin, agent owner, viewer, audit. Who sees which agents, who may deposit secrets, who may grant approvals — the role decides, and every administrative action lands in the audit trail.

Sign-in is built in via JWT and Argon2id. If you want a company login, hang an OIDC provider off the same interface — Keycloak, Entra, whatever the house runs.

## How secrets are stored

In Postgres columns, encrypted with AES-GCM. The key is the instance's `COVEY_MASTER_KEY`; it lives in the environment, not in the database. The encryption additionally binds every value to its organisation, so a ciphertext cannot be read inside a different one.

Practical consequence: the master key matters as much as the database password, and losing it is final. Back it up.

## No plaintext in the interface

A stored secret can be replaced but not shown again. That is deliberate — a value you can read out ends up in a chat message sooner or later.

## Checked when deposited

Known credentials are verified against their system the moment they are saved. An expired subscription token therefore shows up where you type it — not an hour later as a puzzling failure inside an agent's run.

## Capacity instead of a list of passwords

Several credentials for the same engine — three subscription seats and an API key — are not a security question but a commercial one. covey models them as a [workplace with a merit order](../introduction/core-concepts.md): capacity already paid for first, metered billing as peak load.

## Next

- [Guard-rails & control](guard-rails.md) — what may be done with that access
- [Target systems & plugins](../integrations/target-systems.md) — which systems need access
- [Operations & deployment](../operations/operations.md) — master key, HTTPS, backups
