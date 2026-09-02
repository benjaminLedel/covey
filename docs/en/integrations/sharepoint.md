---
slug: sharepoint
title: SharePoint / Teams files
description: A document library behind a share link — a SharePoint site or the files tab of a Teams channel — through the Microsoft Graph API.
---

A practical runbook for the `sharepoint` plugin: a **document library provided
through a share link** (a SharePoint site or the files tab of a Teams channel)
in which the agent lists, reads, edits and deposits files. The technical basis
is the Microsoft Graph API; a Teams channel's files live in the team's
SharePoint site anyway — the same mechanism covers both cases.

> Short version: register an Entra ID app, copy the share link, set two
> secrets, unlock it in ACCESS.md. SharePoint has **no webhook** here — if the
> agent needs an inbox folder as its working set, the intake runs by polling
> through `HEARTBEAT.md`.

---

## 1. Overview of the data flow

```
SharePoint /   ◄──(Microsoft Graph, HTTPS)──  agent (sandbox, Claude Code)
Teams files         list, read, write,          │ actions through the action proxy,
                    download, upload,           │ credentials brokered per call
                    mkdir, delete               │ (sharepoint_url + sharepoint_token)
Entra ID       ◄──(client credentials flow)────┘ bearer token, cached in the daemon
```

The deposited **share link** is resolved to the document library through the
Graph `/shares` endpoint; all paths in the actions are **relative to that
root**. An escape via `..` is refused daemon-side — the agent sees only what
the link releases (plus what the app permission technically allows, see least
privilege below).

---

## 2. Step-by-step instructions

### 2.1 In Entra ID: an app registration for the agent

1. Azure portal → **App registrations** → a new app (e.g. `covey-agent`).
2. **API permissions** → Microsoft Graph → **Application permissions**
   (application, not delegated) → `Files.ReadWrite.All` → **grant admin
   consent**.
3. **Certificates & secrets** → create a new **client secret** and note the
   value immediately (it cannot be viewed later). Note the expiry date in the
   calendar — after expiry all actions fail with `invalid_client`.
4. Note down: the **directory (tenant) ID**, the **application (client) ID**
   and the secret.

**Least privilege:** `Files.ReadWrite.All` technically allows the app access to
all sites in the tenant. Whoever does not want that takes `Sites.Selected`
instead and grants the app targeted access to the one site through Graph
(`POST /sites/{site-id}/permissions` with the app as `grantedToIdentities`,
role `write`) — the plugin works unchanged with either variant.

### 2.2 In SharePoint / Teams: copy the share link

- **SharePoint:** open the folder or document library →
  **Copy link**.
- **Teams:** open the **Files** tab in the channel (switching into the desired
  subfolder if necessary) → **Copy link**.

The link has to point at a **folder** — a link to an individual file is refused
during resolution. A plain browser URL of the folder works too; the Graph
`/shares` endpoint accepts both forms.

### 2.3 In covey: deposit the secrets

| Secret | Value | Purpose |
|---|---|---|
| `sharepoint_url` | the share link from 2.2 | the root of the store |
| `sharepoint_token` | `tenant-id:client-id:client-secret` | the client credentials triple |

**Assign** both secrets to the agent (org-wide secrets only reach an agent on
explicit assignment).

Optional endpoint overrides in `sharepoint_url` (separated by spaces, for
national clouds or tests):

```
sharepoint_url = https://contoso.sharepoint.com/:f:/s/TeamX/AbCdEf…
                 graph=https://graph.microsoft.de login=https://login.microsoftonline.de
```

For demos/tests against a Graph double, `sharepoint_token` can also be a
**ready-made bearer token** instead of the triple (any value without the two
colons) — the OAuth flow is then skipped.

### 2.4 The agent's ACCESS.md

```
- system: sharepoint scope: read,write
```

### 2.5 Egress release

The following have to be reachable from the sandbox:

- `graph.microsoft.com` (or the `graph=` override)
- `login.microsoftonline.com` (or the `login=` override)
- `*.sharepoint.com` — file downloads run through a redirect to a
  pre-authorised SharePoint URL

### 2.6 Optional: intake by heartbeat

SharePoint has **no webhook intake** in covey (Graph change notifications need
a publicly validated HTTPS subscription — deliberately not in the MVP). If the
agent should work through an inbox folder on its own, in the
`HEARTBEAT.md`:

```
- alle: 30m titel: Ablage sichten aufgabe: Liste mit list den Ordner
  "Eingang", verarbeite neue Dateien nach Playbook und verschiebe
  Erledigtes per download + upload nach "Archiv" (Original mit delete
  entfernen).
```

Because there is no webhook, the same applies as for the email plugin: **no
`blocked` status** on SharePoint events — the run ends regularly with `done`,
and the next heartbeat looks again.

---

## 3. The actions at a glance

| Action | Parameters | Effect |
|---|---|---|
| `list` | `path` (optional) | List files/folders (max. 200 per call) |
| `read` | `path` | Deliver a text file directly (up to 1 MB, UTF-8 only) |
| `write` | `path`, `content` | Create/overwrite a text file |
| `download` | `path`, `to` (optional) | Fetch a file into the sandbox (default `sharepoint/<path>`) |
| `upload` | `from`, `to` (optional) | Deposit a file from the sandbox (replaces what is there) |
| `mkdir` | `path` | Create a folder path (`mkdir -p`) |
| `delete` | `path` | Delete a file/folder (the root is off limits) |

Binary and Office files (docx, xlsx, pdf, …) always go through
`download` → edit locally → `upload`; `read`/`write` are meant for plain
text files. Simple uploads are limited to 250 MB
(`COVEY_SHAREPOINT_UPLOAD_MAX_MB` in the daemon env overrides the limit;
larger files would need a Graph upload session — not in the MVP).

---

## 4. Security model

- **No long-lived secret in the sandbox:** the triple and the bearer token live
  only in the daemon's RAM; the token is cached with a safety margin and
  renewed before expiry. The runtime (the LLM process) never sees a secret.
- **Guard rails per action:** every subject is called `sharepoint:<action>` —
  `sharepoint:delete` or `sharepoint:write` can therefore be put behind an
  approval requirement specifically, while `sharepoint:list`/`read` stay free.
- **Path hardening:** remote paths are normalised (`..` refused), local paths
  resolved against the sandbox working directory — no escape into `/home` or
  the host file system.

---

## 5. Typical failure patterns

| Symptom | Cause | Remedy |
|---|---|---|
| `invalid_client` when fetching the token | The client secret is wrong or expired | create a new secret, update `sharepoint_token` |
| `HTTP 403 accessDenied` during resolution | Admin consent is missing, or `Sites.Selected` without a site grant | grant consent or create the site permission |
| "The share link points at a file" | A link to a file instead of a folder was copied | deposit a folder/library link |
| `itemNotFound` on actions | The path does not exist (relative to the link root!) | check the actual tree with `list` |
| A download breaks off | `*.sharepoint.com` is not released in the egress | add the egress release |
