---
slug: target-systems
title: Target systems & plugins
description: 'Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Teams, SharePoint, Nextcloud, email, browser and MCP: how Covey connects agents to foreign systems through plugins.'
faq:
  - q: Can I connect a system that has no plugin?
    a: 'Yes — fastest through an MCP server, whose tools the agent can then use. If you need wake events, scopes and actions in the recording, write a plugin: as a manifest (JSON, installable with no rebuild), as a WebAssembly module, or compiled in Go. The ones that ship sit in the plugin pack and are the template.'
  - q: Does every target system need a webhook?
    a: No. Without one the agent works from a heartbeat, ideally with `nur-wenn:` — the control plane then checks cheaply in advance whether there is any work, instead of waking the agent for nothing.
  - q: How does the agent get to a Git repository?
    a: 'Through the GitHub or GitLab plugin: the checkout happens inside the sandbox with brokered, short-lived access. The working directory stays in the agent''s home and is there again on the next run.'
---

# Target systems & plugins

An agent becomes useful when it works in the systems where the work already happens. In Covey each of them is a **plugin**: it declares its actions, its scopes and its wake events, and the core knows no special cases.

## What ships with it

- **Zammad** — triage tickets, add internal notes, reply externally; webhook as a wake source
- **Salesforce Service Cloud** — cases with their whole conversation; reply as a note, a portal comment or a mail
- **GitHub** and **GitLab** — issues, pull and merge requests, pipelines, checkout inside the sandbox
- **Jira** — the ticket beside the repository: search by JQL, take it on, move it through its workflow; Cloud and Data Center
- **Confluence** — the documentation both of them hang off: pages read and written as Markdown. Wakes nobody — an agent comes here while it works on something else
- **Email (IMAP/SMTP)** — a mailbox as a wake source, replies in-thread
- **Microsoft Teams** — chat as the channel between human and agent
- **SharePoint** via Microsoft Graph and **Nextcloud** via WebDAV — files
- **Browser** — headless Chrome for interfaces without an API, with screenshots in the recording
- **MCP** — any Model Context Protocol server as a source of tools
- **Kubernetes** — a cluster read out: why a pod restarts, what it said before it died, where an Ingress points
- **VulnDB** — known vulnerabilities in dependencies (npm, Composer, Dart/Flutter)

## Where they come from

Most of them are compiled in and are simply there. Three are not: **Zammad**,
**Kubernetes** and **VulnDB** arrive from the **catalogue** as WebAssembly
modules, and they have to be installed once — *Store → Catalogue → Install*.
Covey verifies the digest the catalogue pins before storing the module.

The difference is deliberate rather than historical: a plugin from the
catalogue is upgraded without a new Covey release, and a third party can
publish one on exactly the terms we do. Anyone upgrading an installation
across 0.6.0 installs those three once afterwards — the agents keep their
access.

## The action proxy

The agent does not call a target system directly. It names an action, the control plane executes it and returns the result. Three things fall out of that which you would otherwise have to build: the token stays outside, the guard-rails apply in one place, and the action lands in the recording.

## Wake events

The best route is the webhook: when something happens in the ticket system, it calls Covey and the responsible agent wakes up. Where there is no webhook, a heartbeat with `nur-wenn:` helps — the control plane checks cheaply whether there is work and otherwise wakes nobody.

Events carry a correlation key so that a waiting task is resumed instead of a new one being created.

## Access in the agent

Which systems an agent may use is stated in its `ACCESS.md`:

```
- system: zammad scope: read,write,comment
- system: gitlab scope: read,write
```

The values behind them — URL, token — belong to the organisation, not to the agent, and are brokered at runtime.

## Connecting your own system

Four routes, depending on how far the connection has to carry. The quickest is an **MCP server** — the agent gets its tools without anything in Covey changing. A **manifest** describes a REST API as a JSON file and installs at runtime, with no rebuild. A **WebAssembly module** brings logic of its own and comes from the catalogue too. And a **compiled plugin** in Go is the route when the integration has to translate what the foreign system stores — Jira and Confluence do exactly that with their document formats. All four serve the same interface; the ones that ship sit in the plugin pack and are the template.

## Next

- [Guard-rails & control](../concepts/guard-rails.md) — approvals for critical actions
- [Identity & secrets](../concepts/identity-and-secrets.md) — how access is provided
- [Backlog & lifecycle](../concepts/backlog-and-lifecycle.md) — what a wake event triggers
