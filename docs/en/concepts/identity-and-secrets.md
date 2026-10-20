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

Conversations are the exception to "the role decides": a conversation is read and written by its members, whatever their role, and by nobody else. Only the org admin and the auditor read and export conversations they are not in, in the audit, next to the shared per-agent threads from before conversations had members.

Every member may write to AI colleagues directly. Whom they reach is a setting of the organisation (*Administration → Who writes to which colleague*): any colleague, or only those of their own department; the colleague's supervisor and the org admin always. When it narrows, a conversation that exists stays readable and takes no new message. Adding an AI colleague to a group stays with the roles that may hand over work by hand, and so does answering a parked task that is not in one's own conversation.

Sign-in is built in via JWT and Argon2id. If you want a company login, hang an OIDC provider off the same interface — Keycloak, Entra, whatever the house runs.

## How secrets are stored

In Postgres columns, encrypted with AES-GCM. The key is the instance's `COVEY_MASTER_KEY`; it lives in the environment, not in the database. The encryption additionally binds every value to its organisation, so a ciphertext cannot be read inside a different one.

Practical consequence: the master key matters as much as the database password, and losing it is final. Back it up.

## No plaintext in the interface

A stored secret can be replaced but not shown again. That is deliberate — a value you can read out ends up in a chat message sooner or later.

## Checked when deposited

Known credentials are verified against their system the moment they are saved. An expired subscription token therefore shows up where you type it — not an hour later as a puzzling failure inside an agent's run.

## Secrets in an action's parameters

Some credentials belong to no target system: a login for a staging environment, a token for a private package registry. Store such a value as a secret of the agent, or as an organisation secret assigned to it, and write the placeholder `{{secret:<key>}}` into the parameters of an action. The action proxy replaces it inside the sandbox, after the model has committed to the call and before the plugin runs. The model only ever sees the placeholder, and so does the audit log. Nothing resolves for an agent the secret was not stored or assigned for.

The most common case is installing a project's dependencies from a private source. `dev:exec` runs its command through a shell, so the value ends up in the environment of that one process, and nothing is written to the home:

```json
{"action": "dev:exec", "params": {"cmd": "NPM_TOKEN={{secret:npm_token}} npm ci"}}
```

```json
{"action": "dev:exec", "params": {"cmd": "COMPOSER_AUTH='{{secret:composer_auth}}' composer install"}}
```

A `git:` dependency in a private repository (in `pubspec.yaml`, `package.json` or `composer.json`) gets its credential for one git call through git's environment configuration, not through `.gitconfig`:

```json
{"action": "dev:exec", "params": {"cmd": "GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0='url.https://oauth2:{{secret:git_deps_token}}@gitlab.example.org/.insteadOf' GIT_CONFIG_VALUE_0='https://gitlab.example.org/' flutter pub get"}}
```

Three limits:

- **Only in actions.** The placeholder is resolved in actions that go through the action proxy (`dev:exec`, `dev:start`, `browser:type`, …). In the runtime's own shell tool it reaches the command literally.
- **The output is not masked.** What the command prints goes back to the model. A tool that echoes its credential, such as an error message that repeats the URL, puts the value into the model's context.
- **The process can read it.** The install scripts of the repository run in the same environment and can read the token. Use a token that can only read packages.

## Capacity instead of a list of passwords

Several credentials for the same engine — three subscription seats and an API key — are not a security question but a commercial one. covey models them as a [workplace with a merit order](../introduction/core-concepts.md): capacity already paid for first, metered billing as peak load.

## Next

- [Guard-rails & control](guard-rails.md) — what may be done with that access
- [Target systems & plugins](../integrations/target-systems.md) — which systems need access
- [Operations & deployment](../operations/operations.md) — master key, HTTPS, backups
